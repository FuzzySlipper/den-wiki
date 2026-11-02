package lint

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/FuzzySlipper/den-wiki/internal/media"
)

// checkSourceMedia checks a binary capture: the file its media field names
// exists and matches media_sha256. A media store file is named by its hash,
// so its name is checked against media_sha256; when lint knows the store, the
// file must be there and its bytes must still hash to that name.
func (l *linter) checkSourceMedia(rel string, f map[string]any) {
	ref, ok := stringField(f, "media")
	if !ok {
		return
	}
	want, _ := stringField(f, "media_sha256")
	want = strings.ToLower(want)
	if name, inStore := strings.CutPrefix(ref, media.URLPrefix); inStore {
		sum, _, err := media.ParseName(name)
		if err != nil {
			return // the schema reports a malformed reference
		}
		if want != "" && sum != want {
			l.add(newFinding(codeSourceMediaHash, rel, "media "+quote(ref)+" is named for sha256 "+sum+" but media_sha256 says "+want))
		}
		if l.mediaDir == "" {
			return
		}
		store := media.Store{Dir: l.mediaDir}
		if !store.Has(name) {
			l.add(newFinding(codeSourceMedia, rel, "media "+quote(ref)+" is not in the media store"))
			return
		}
		if intact, err := store.Verify(name); err != nil || !intact {
			l.add(newFinding(codeSourceMediaHash, rel, "media "+quote(ref)+" in the media store no longer hashes to its name; captures are never edited, so restore the file or capture a new source"))
		}
		return
	}
	b, err := os.ReadFile(filepath.Join(l.root, filepath.FromSlash(ref)))
	if err != nil {
		l.add(newFinding(codeSourceMedia, rel, "media file "+quote(ref)+" does not exist; attach it with wiki_attach to this source"))
		return
	}
	if got := media.Hash(b); want != "" && got != want {
		l.add(newFinding(codeSourceMediaHash, rel, "media file "+quote(ref)+" has sha256 "+got+" but media_sha256 says "+want+"; captures are never edited, so capture a new source if the file changed"))
	}
}

// checkMediaRefs reports /media/ references in a body that the media store
// does not hold. References in fenced code are examples, not embeds.
func (l *linter) checkMediaRefs(rel string, body []byte) {
	if l.mediaDir == "" {
		return
	}
	store := media.Store{Dir: l.mediaDir}
	seen := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(string(body), "\n") {
		if fenceLine.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range media.RefRe.FindAllStringSubmatch(line, -1) {
			name := m[1]
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, _, err := media.ParseName(name); err != nil || !store.Has(name) {
				l.add(newFinding(codeMediaMissing, rel, "refers to /media/"+name+", which the media store does not hold"))
			}
		}
	}
}
