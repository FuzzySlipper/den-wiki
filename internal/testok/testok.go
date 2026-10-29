// Package testok starts a real, pinned OpenKnowledge server for integration
// tests (build tag okintegration). It never vendors OK: the binary comes
// from scripts/install-ok.sh.
package testok

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var (
	okBin = flag.String("ok-bin", "", "OpenKnowledge binary from scripts/install-ok.sh (<dir>/node_modules/.bin/ok); required")
	// OK writes ~/.ok/local under the real home directory whatever HOME
	// says (#10462). -ok-isolate-home runs it under bwrap with a throwaway
	// tmpfs over the home directory so a developer machine stays clean.
	isolateHome = flag.Bool("ok-isolate-home", false, "run OK under bwrap with a tmpfs over $HOME")
)

// Stack is a running OK on a git-initialised content directory.
type Stack struct {
	// MCP is OK's MCP endpoint.
	MCP string
	// ContentDir is the content repository OK serves.
	ContentDir string
	// Seed is the content repo's first commit.
	Seed string
	Log  *bytes.Buffer
}

// Start copies the content template and the lint fixture into a new git
// repository, starts OK on it and waits until its MCP answers. repoRoot is
// the den-wiki checkout.
func Start(t *testing.T, repoRoot string) *Stack {
	t.Helper()
	if *okBin == "" {
		t.Fatal("-ok-bin is required: install OK with scripts/install-ok.sh <dir> and pass -ok-bin <dir>/node_modules/.bin/ok")
	}
	bin, err := filepath.Abs(*okBin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("-ok-bin: %v", err)
	}
	work := t.TempDir()
	content := filepath.Join(work, "content")
	home := filepath.Join(work, "home")
	for _, d := range []string{content, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyTree(t, filepath.Join(repoRoot, "templates/content"), content)
	copyTree(t, filepath.Join(repoRoot, "internal/lint/testdata/valid"), content)
	git(t, content, "init", "-q", "-b", "main")
	git(t, content, "add", "-A")
	git(t, content, "commit", "-q", "-m", "Seed from den-wiki template and lint fixtures")
	seed := strings.TrimSpace(git(t, content, "rev-parse", "HEAD"))

	port := freePort(t)
	args := []string{"start", "--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--no-open-browser", "--idle-shutdown", "off", "--only", "server"}
	var cmd *exec.Cmd
	if *isolateHome {
		realHome, _ := os.UserHomeDir()
		pkg := filepath.Dir(filepath.Dir(filepath.Dir(bin))) // <dir> above node_modules/.bin/ok
		wrap := []string{"--dev-bind", "/", "/", "--tmpfs", realHome, "--bind", work, work, "--ro-bind", pkg, pkg, "--chdir", content, "--die-with-parent", "--", bin}
		cmd = exec.Command("bwrap", append(wrap, args...)...)
	} else {
		cmd = exec.Command(bin, args...)
	}
	cmd.Dir = content
	// OK forks a server child; give it its own process group so cleanup can
	// stop all of it, and stop waiting on its pipes once it has exited.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "HOME="+home, "NO_COLOR=1")
	log := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start OK: %v", err)
	}
	t.Cleanup(func() {
		pgid := -cmd.Process.Pid
		_ = syscall.Kill(pgid, syscall.SIGINT)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(pgid, syscall.SIGKILL)
			<-done
		}
		// The forked server detaches (it was seen reparented to init), so
		// also stop anything still running in the content directory.
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		killByCwd(content)
		if t.Failed() {
			t.Logf("OpenKnowledge log:\n%s", log.String())
		}
	})
	s := &Stack{MCP: fmt.Sprintf("http://127.0.0.1:%d/mcp", port), ContentDir: content, Seed: seed, Log: log}
	waitReady(t, s, cmd)
	return s
}

func waitReady(t *testing.T, s *Stack, cmd *exec.Cmd) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"den-wiki-probe","version":"1"}}}`
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil {
			t.Fatalf("OK exited early:\n%s", s.Log.String())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.MCP, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				// Give the index a moment to load the seeded docs.
				time.Sleep(2 * time.Second)
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("OK did not answer at %s within 90s:\n%s", s.MCP, s.Log.String())
}

// Git runs git in the content repo and returns its output.
func (s *Stack) Git(t *testing.T, args ...string) string { return git(t, s.ContentDir, args...) }

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=seed", "-c", "user.email=seed@test", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// killByCwd kills every process whose working directory is dir (Linux).
func killByCwd(dir string) {
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid := 0
		if _, err := fmt.Sscan(e.Name(), &pid); err != nil || pid <= 1 || pid == os.Getpid() {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err == nil && (cwd == dir || strings.HasPrefix(cwd, dir+"/")) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
