// Package config reads den-wiki's typed YAML configuration. den-wiki takes
// its settings from flags and this file, never from environment variables.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Server configures `den-wiki serve`.
type Server struct {
	// Listen is host:port. It must be a loopback or private-LAN address:
	// den-wiki has no auth of its own.
	Listen string `yaml:"listen"`
	// Database is the SQLite file holding the ledger and the read log.
	Database string `yaml:"database"`
	// ContentDir is the den-wiki-content checkout; it enables lint and the
	// trail-page check on job_finish.
	ContentDir string `yaml:"content_dir"`
	// OpenKnowledge is the OK server the wiki tools pass through to.
	OpenKnowledge OpenKnowledge `yaml:"openknowledge"`
	// UICommits commits changes made in OK's web UI. It needs content_dir.
	UICommits UICommits `yaml:"ui_commits"`
}

// UICommits configures the background commits of content changes that did
// not come through the facade: edits in OK's web UI, pages its editor
// re-saved when viewed, and facade writes whose own commit failed.
type UICommits struct {
	// Disabled turns the background commits off.
	Disabled bool `yaml:"disabled"`
	// Author is the git author of UI commits; default "ui".
	Author string `yaml:"author"`
	// Interval is how often to look for changes; default 30s.
	Interval time.Duration `yaml:"interval"`
	// Quiet is how long a file must go unchanged before it is committed;
	// default 60s.
	Quiet time.Duration `yaml:"quiet"`
}

// OpenKnowledge locates the OK server.
type OpenKnowledge struct {
	// URL is OK's MCP endpoint, e.g. http://127.0.0.1:4800/mcp. Empty
	// disables the wiki tools.
	URL string `yaml:"url"`
	// Timeout bounds one OK call; default 30s.
	Timeout time.Duration `yaml:"timeout"`
}

// Defaults.
const (
	DefaultListen   = "127.0.0.1:7341"
	DefaultDatabase = "den-wiki.db"
	DefaultTimeout  = 30 * time.Second

	DefaultUIAuthor   = "ui"
	DefaultUIInterval = 30 * time.Second
	DefaultUIQuiet    = 60 * time.Second
)

// LoadServer reads path strictly (unknown keys are errors) and applies
// defaults. Relative paths resolve against the file's directory.
func LoadServer(path string) (Server, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Server{}, err
	}
	var s Server
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) { // an empty file is an empty config
		return Server{}, fmt.Errorf("%s: %w", path, err)
	}
	base := filepath.Dir(path)
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	if s.Listen == "" {
		s.Listen = DefaultListen
	}
	if s.Database == "" {
		s.Database = DefaultDatabase
	}
	s.Database = resolve(s.Database)
	s.ContentDir = resolve(s.ContentDir)
	if s.OpenKnowledge.Timeout == 0 {
		s.OpenKnowledge.Timeout = DefaultTimeout
	}
	if s.UICommits.Author == "" {
		s.UICommits.Author = DefaultUIAuthor
	}
	if s.UICommits.Interval == 0 {
		s.UICommits.Interval = DefaultUIInterval
	}
	if s.UICommits.Quiet == 0 {
		s.UICommits.Quiet = DefaultUIQuiet
	}
	return s, s.Validate()
}

// Validate checks the listen address and OK URL.
func (s Server) Validate() error {
	if err := CheckListen(s.Listen); err != nil {
		return err
	}
	if s.UICommits.Interval < 0 || s.UICommits.Quiet < 0 {
		return fmt.Errorf("ui_commits interval and quiet must not be negative")
	}
	if u := s.OpenKnowledge.URL; u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("openknowledge.url %q must be an http(s) URL", u)
	}
	return nil
}

// CheckListen accepts only loopback and private-LAN listen addresses.
func CheckListen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("listen %q: give an IP address, not a hostname", addr)
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return nil
	}
	return fmt.Errorf("listen %q is not loopback or private LAN; den-wiki has no auth and must not listen publicly", addr)
}
