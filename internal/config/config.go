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
	// Media is the media store for video and very large files.
	Media Media `yaml:"media"`
	// Publish is where wiki_publish sends a page to be shared on the blog.
	Publish Publish `yaml:"publish"`
}

// Publish locates doc-publish, the den-services service that writes shared
// pages to the blog.
type Publish struct {
	// DocPublishURL is doc-publish's publications endpoint, e.g.
	// https://den.dragonden.stream/api/v1/blog/publications. Empty disables
	// wiki_publish.
	DocPublishURL string `yaml:"doc_publish_url"`
	// Timeout bounds one doc-publish call; default 60s (a publish fetches the
	// page and its images, commits and pushes).
	Timeout time.Duration `yaml:"timeout"`
}

// Media configures the content-addressed media store: files wiki_attach
// keeps out of git, served at /media/<sha256>.<ext>.
type Media struct {
	// Dir holds the files. It must already exist (it is normally a NAS
	// mount, and den-wiki must not fill the directory under a missing
	// mount). Empty disables the store.
	Dir string `yaml:"dir"`
	// GitMaxBytes is the size above which an attached file goes to the store
	// instead of git; default 50 MiB. Video always goes to the store.
	GitMaxBytes int64 `yaml:"git_max_bytes"`
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

	DefaultPublishTimeout = 60 * time.Second

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
	s.Media.Dir = resolve(s.Media.Dir)
	if s.OpenKnowledge.Timeout == 0 {
		s.OpenKnowledge.Timeout = DefaultTimeout
	}
	if s.Publish.Timeout == 0 {
		s.Publish.Timeout = DefaultPublishTimeout
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

// Validate checks the listen address and the URLs.
func (s Server) Validate() error {
	if err := CheckListen(s.Listen); err != nil {
		return err
	}
	if s.Media.GitMaxBytes < 0 {
		return fmt.Errorf("media.git_max_bytes must not be negative")
	}
	if s.UICommits.Interval < 0 || s.UICommits.Quiet < 0 {
		return fmt.Errorf("ui_commits interval and quiet must not be negative")
	}
	if u := s.OpenKnowledge.URL; u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("openknowledge.url %q must be an http(s) URL", u)
	}
	if u := s.Publish.DocPublishURL; u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("publish.doc_publish_url %q must be an http(s) URL", u)
	}
	if s.Publish.Timeout < 0 {
		return fmt.Errorf("publish.timeout must not be negative")
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
