package media

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPutIsContentAddressedAndIdempotent(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	b := []byte("clip bytes")
	name, err := s.Put("MP4", b)
	if err != nil {
		t.Fatal(err)
	}
	if name != Hash(b)+".mp4" || !s.Has(name) {
		t.Fatalf("name %q", name)
	}
	again, err := s.Put("mp4", b)
	if err != nil || again != name {
		t.Fatalf("second put: %q, %v", again, err)
	}
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 1 {
		t.Fatalf("store holds %d entries, want 1 (no temporary files left)", len(entries))
	}
	if ok, err := s.Verify(name); !ok || err != nil {
		t.Fatalf("verify: %v, %v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, name), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Verify(name); ok {
		t.Fatal("verify passed a tampered file")
	}
	if _, err := s.Put("exe", b); err == nil {
		t.Fatal("put accepted an unknown extension")
	}
}

func TestParseName(t *testing.T) {
	h := Hash([]byte("x"))
	for name, ok := range map[string]bool{
		h + ".png": true, h + ".webm": true,
		h + ".PNG": false, h + ".exe": false, "abc.png": false, h + ".png/../x": false,
	} {
		if _, _, err := ParseName(name); (err == nil) != ok {
			t.Errorf("ParseName(%q) err = %v, want ok=%v", name, err, ok)
		}
	}
}

func TestHandler(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	svg, err := s.Put("svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.StripPrefix("", s.Handler()))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/media/" + svg)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/svg+xml" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("svg: %d %v", resp.StatusCode, resp.Header)
	}
	for path, want := range map[string]int{
		"/media/" + Hash([]byte("absent")) + ".png": 404,
		"/media/../go.mod":                          404,
		"/media/not-a-hash.png":                     404,
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/media/"+svg, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}
}
