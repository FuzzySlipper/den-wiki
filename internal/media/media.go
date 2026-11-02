// Package media is den-wiki's content-addressed media store: files too big
// for the content repo's git history, and every video, kept outside git as
// <sha256>.<ext> and served at /media/<sha256>.<ext>. The content repo holds
// only the reference, whose name is the file's hash.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// URLPrefix is where the store's files are served, and how content refers
// to them: /media/<sha256>.<ext>.
const URLPrefix = "/media/"

// Kind groups file types by how the wiki treats them.
type Kind string

const (
	Image    Kind = "image"
	Video    Kind = "video"
	Document Kind = "document"
)

// Type is one accepted file type.
type Type struct {
	Ext  string
	MIME string
	Kind Kind
}

// Types are the file types den-wiki accepts as media, by extension. They
// match what OpenKnowledge renders: its img and video components and its
// Pdf embed.
var Types = []Type{
	{"png", "image/png", Image},
	{"jpg", "image/jpeg", Image},
	{"jpeg", "image/jpeg", Image},
	{"gif", "image/gif", Image},
	{"webp", "image/webp", Image},
	{"avif", "image/avif", Image},
	{"svg", "image/svg+xml", Image},
	{"mp4", "video/mp4", Video},
	{"webm", "video/webm", Video},
	{"pdf", "application/pdf", Document},
}

// TypeOf returns the accepted type of a file name, by its extension.
func TypeOf(name string) (Type, bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	for _, t := range Types {
		if t.Ext == ext {
			return t, true
		}
	}
	return Type{}, false
}

// Extensions lists the accepted extensions, for messages.
func Extensions() string {
	var out []string
	for _, t := range Types {
		out = append(out, t.Ext)
	}
	return strings.Join(out, ", ")
}

// Hash returns the lowercase hex SHA-256 of b.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var nameRe = regexp.MustCompile(`^([0-9a-f]{64})\.([a-z0-9]+)$`)

// ParseName splits a store file name, <sha256>.<ext>, and checks the type.
func ParseName(name string) (sum string, t Type, err error) {
	m := nameRe.FindStringSubmatch(name)
	if m == nil {
		return "", Type{}, fmt.Errorf("%q is not a media store name (<sha256>.<ext>)", name)
	}
	t, ok := TypeOf(name)
	if !ok || t.Ext != m[2] {
		return "", Type{}, fmt.Errorf("%q has an extension the media store does not accept (%s)", name, Extensions())
	}
	return m[1], t, nil
}

// RefRe matches a media store reference in content: /media/<sha256>.<ext>,
// alone or at the end of a URL.
var RefRe = regexp.MustCompile(`/media/([0-9a-f]{64}\.[a-z0-9]+)`)

// Store is a media store directory.
type Store struct {
	Dir string
}

// Put stores b under its hash and returns the file name. Storing the same
// bytes again is a no-op. The write goes through a temporary file, so a
// reader never sees a partial file.
func (s Store) Put(ext string, b []byte) (string, error) {
	name := Hash(b) + "." + strings.ToLower(ext)
	if _, _, err := ParseName(name); err != nil {
		return "", err
	}
	dst := filepath.Join(s.Dir, name)
	if _, err := os.Stat(dst); err == nil {
		return name, nil
	}
	tmp, err := os.CreateTemp(s.Dir, ".upload-*")
	if err != nil {
		return "", fmt.Errorf("media store: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return "", fmt.Errorf("media store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("media store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("media store: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", fmt.Errorf("media store: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", fmt.Errorf("media store: %w", err)
	}
	return name, nil
}

// Has reports whether the store holds a file name.
func (s Store) Has(name string) bool {
	if _, _, err := ParseName(name); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(s.Dir, name))
	return err == nil
}

// Verify rehashes a stored file and reports whether it still matches its
// name.
func (s Store) Verify(name string) (bool, error) {
	sum, _, err := ParseName(name)
	if err != nil {
		return false, err
	}
	f, err := os.Open(filepath.Join(s.Dir, name))
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == sum, nil
}

// Handler serves GET and HEAD /media/<sha256>.<ext> with range support (so
// video seeks), the file type's MIME type and a long, immutable cache life:
// a name never changes its bytes. The sandbox policy keeps an SVG opened on
// its own from running script on the wiki's origin.
func (s Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, URLPrefix)
		_, t, err := ParseName(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(s.Dir, name))
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "media store unavailable", http.StatusServiceUnavailable)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.Error(w, "media store unavailable", http.StatusServiceUnavailable)
			return
		}
		h := w.Header()
		h.Set("Content-Type", t.MIME)
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox")
		http.ServeContent(w, r, name, st.ModTime(), f)
	})
}
