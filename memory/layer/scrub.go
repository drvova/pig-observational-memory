package layer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScrubReport is what one Scrub changed: files rewritten through the store (a prose definition,
// written or held, with a put generation or its held index entry), code definitions forgotten
// (code is never rewritten), blobs removed, and other files (the manifest, the held index)
// rewritten in place.
type ScrubReport struct {
	Rewritten, Forgotten, Blobs, Other int
}

// Files is every file the scrub changed or removed.
func (r ScrubReport) Files() int { return r.Rewritten + r.Forgotten + r.Blobs + r.Other }

// Scrub purges what match finds from every model's layer under the store's lock (decisions 10
// and 11; docs/memory-v2.md, "Erasure and the layer purge"). A written or held prose definition
// (ProseKind) holding it is rewritten with fix applied to its text, through the store, so its
// manifest or held index records the new content; a code or query definition holding it is
// forgotten whole, since a rewritten literal would change what it does; a blob holding it is
// removed, so a rollback to that generation is refused rather than restoring it; any other file
// (the manifest's lines, the held index) is rewritten with fix applied. session names the
// generation's writer.
func (s Store) Scrub(match func(string) bool, fix func(string) string, session string) (ScrubReport, error) {
	var r ScrubReport
	err := s.locked(func() error {
		models, err := os.ReadDir(s.Dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, m := range models {
			if !m.IsDir() {
				continue
			}
			if err := s.scrubModel(filepath.Join(s.Dir, m.Name()), match, fix, session, &r); err != nil {
				return err
			}
		}
		return nil
	})
	return r, err
}

func (s Store) scrubModel(dir string, match func(string) bool, fix func(string) string, session string, r *ScrubReport) error {
	var paths []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && !strings.Contains(d.Name(), ".tmp") && d.Name() != ".lock" {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	var other []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || !match(string(data)) {
			continue
		}
		rel, _ := filepath.Rel(dir, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		switch {
		case parts[0] == "blobs":
			if err := os.Remove(path); err != nil {
				return err
			}
			r.Blobs++
		case len(parts) == 2 && ValidKind(parts[0]) && strings.HasSuffix(parts[1], ".rows"):
			d, _, err := decodeFile(string(data))
			if err != nil {
				other = append(other, path)
				continue
			}
			if !ProseKind(d.Kind) {
				if _, _, err := s.ForgetLocked(d.Model, d.Kind, d.Name, session); err != nil && err != ErrNotFound {
					return err
				}
				r.Forgotten++
				continue
			}
			d.Text, d.Session = fix(d.Text), session
			if _, err := s.putStampedScoped(d, nil); err != nil {
				return err
			}
			r.Rewritten++
		case len(parts) == 3 && parts[0] == "held" && ValidKind(parts[1]) && strings.HasSuffix(parts[2], ".rows"):
			d, fallback, err := decodeFile(string(data))
			if err != nil {
				other = append(other, path)
				continue
			}
			if !ProseKind(d.Kind) {
				if err := os.Remove(path); err != nil {
					return err
				}
				if err := s.recordHeld(d.Model, d.Key(), ""); err != nil {
					return err
				}
				r.Forgotten++
				continue
			}
			d.Text = fix(d.Text)
			content := encodeFile(d, fallback)
			if err := writeAtomic(path, content); err != nil {
				return err
			}
			if err := s.recordHeld(d.Model, d.Key(), Digest(content)); err != nil {
				return err
			}
			r.Rewritten++
		default:
			other = append(other, path)
		}
	}
	// The manifest and the held index last, after the writes above appended to them.
	for _, path := range other {
		data, err := os.ReadFile(path)
		if err != nil || !match(string(data)) {
			continue
		}
		if err := writeAtomic(path, fix(string(data))); err != nil {
			return err
		}
		r.Other++
	}
	return nil
}
