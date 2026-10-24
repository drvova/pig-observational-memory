package layer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// heldDef is a held definition with the fallback its first hold recorded: the digest of the
// written file that stood then, or noFallback.
type heldDef struct {
	def      Definition
	fallback string
}

// Hold holds d: it applies to its model's next pass (Load returns it over the written one) and
// is not written to the layer until Claim finds that pass clean. Re-holding a held name keeps
// the ORIGINAL fallback: the definition standing at the first hold, never the unproven one
// held since. It is checked like a definition put (Check).
func (s Store) Hold(d Definition) (Definition, error) {
	var out Definition
	err := s.locked(func() error {
		var err error
		out, err = s.HoldLocked(d)
		return err
	})
	return out, err
}

// HoldLocked is Hold for a caller that already holds the store's lock.
func (s Store) HoldLocked(d Definition) (Definition, error) {
	d, err := s.stamp(d)
	if err != nil {
		return Definition{}, err
	}
	fallback := ""
	heldPath := s.heldPath(d.Model, d.Kind, d.Name)
	if data, err := os.ReadFile(heldPath); err == nil {
		// Only a held file that decodes and records a fallback passes it on; a torn or
		// corrupt one falls through to the written file standing now.
		if _, previous, err := decodeFile(string(data)); err == nil && previous != "" {
			fallback = previous
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Definition{}, err
	}
	if fallback == "" {
		f, err := s.standingFallback(d.Model, d.Kind, d.Name)
		if err != nil {
			return Definition{}, err
		}
		fallback = f
	}
	content := encodeFile(d, fallback)
	if err := writeAtomic(heldPath, content); err != nil {
		return Definition{}, err
	}
	// The hold is recorded only after its file is in place: a crash between the two leaves an
	// untracked held file, which is never loaded, rather than a record with no file.
	if err := s.recordHeld(d.Model, d.Key(), Digest(content)); err != nil {
		return Definition{}, err
	}
	d.State = StateHeld
	return d, nil
}

// HoldPass holds a build or revision pass's definitions (defs, all for model) and appends its
// mark (Mark) in one critical section, so another process never sees some of them held with no
// mark, and a caller that checks its context once before calling keeps the pass whole: all of it
// lands or none. A definition the checks refuse is skipped, with its error at its index in
// refused (nil for one held); held are the definitions held. An error writing the layer leaves
// what was held before it, and no mark.
func (s Store) HoldPass(model string, defs []Definition, mark Generation) (held []Definition, refused []error, gen Generation, err error) {
	if mark.Op != OpBuild && mark.Op != OpRevise {
		return nil, nil, Generation{}, fmt.Errorf("only a build or revision pass is marked, not %q", mark.Op)
	}
	refused = make([]error, len(defs))
	err = s.locked(func() error {
		for i, d := range defs {
			if d.Model != model {
				refused[i] = fmt.Errorf("a definition for %s in %s's pass", d.Model, model)
				continue
			}
			h, err := s.HoldLocked(d)
			if err != nil {
				refused[i] = err
				continue
			}
			held = append(held, h)
		}
		var err error
		gen, err = s.appendGeneration(model, mark)
		return err
	})
	return held, refused, gen, err
}

// standingFallback is the fallback a first hold records: the digest of the written file
// standing now, whose blob is kept for a revert to put back, or noFallback when none stands.
func (s Store) standingFallback(model, kind, name string) (string, error) {
	data, err := os.ReadFile(s.Path(model, kind, name))
	if errors.Is(err, os.ErrNotExist) {
		return noFallback, nil
	}
	if err != nil {
		return "", err
	}
	content := string(data)
	fallback := Digest(content)
	blob := s.blobPath(model, fallback)
	if _, err := os.Stat(blob); err != nil {
		if err := writeAtomic(blob, content); err != nil {
			return "", err
		}
	}
	return fallback, nil
}

// heldIndexPath is model's held index: each held kind/name with the digest of the held file
// its hold wrote. A held file it does not record (written outside the store) is never loaded.
func (s Store) heldIndexPath(model string) string {
	return filepath.Join(s.modelPath(model), "held", "index.json")
}

// heldIndex reads model's held index; a missing one is empty, and an unreadable one records
// nothing, so every held file is untracked until held again.
func (s Store) heldIndex(model string) (map[string]string, error) {
	data, err := os.ReadFile(s.heldIndexPath(model))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if json.Unmarshal(data, &out) != nil {
		return map[string]string{}, nil
	}
	return out, nil
}

// recordHeld records key's held file as holding the content digest names in model's held index
// ("" removes it), written atomically; the caller holds the store's lock.
func (s Store) recordHeld(model, key, digest string) error {
	index, err := s.heldIndex(model)
	if err != nil {
		return err
	}
	if digest == "" {
		if _, ok := index[key]; !ok {
			return nil
		}
		delete(index, key)
	} else {
		index[key] = digest
	}
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return writeAtomic(s.heldIndexPath(model), string(data)+"\n")
}

// dropHeld removes model's held definitions but those kept reports (nil keeps none), with their
// records in the held index; the caller holds the store's lock.
func (s Store) dropHeld(model string, kept func(key string) bool) error {
	if kept == nil {
		return os.RemoveAll(filepath.Join(s.modelPath(model), "held"))
	}
	for _, kind := range Kinds {
		names, err := rowsNames(filepath.Join(s.modelPath(model), "held", kind))
		if err != nil {
			return err
		}
		for _, name := range names {
			key := SeedKey(kind, name)
			if kept(key) {
				continue
			}
			if err := os.Remove(s.heldPath(model, kind, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := s.recordHeld(model, key, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// held reads model's held definitions; a held file the kernel or the store's checks refuse is
// reported and left for Claim to revert, and one the held index does not record is reported
// as untracked and never loaded.
func (s Store) held(model string) ([]heldDef, []Report, error) {
	index, err := s.heldIndex(model)
	if err != nil {
		return nil, nil, err
	}
	out, reports := []heldDef{}, []Report{}
	for _, kind := range Kinds {
		names, err := rowsNames(filepath.Join(s.modelPath(model), "held", kind))
		if err != nil {
			return nil, nil, err
		}
		for _, name := range names {
			d, fallback, report := s.load(s.heldPath(model, kind, name), model, kind, name, index)
			if report != nil {
				reports = append(reports, *report)
				continue
			}
			d.State = StateHeld
			out = append(out, heldDef{def: d, fallback: fallback})
		}
	}
	return out, reports, nil
}

// Held are model's held definitions, by kind and name.
func (s Store) Held(model string) ([]Definition, error) {
	held, _, err := s.held(model)
	if err != nil {
		return nil, err
	}
	out := make([]Definition, 0, len(held))
	for _, h := range held {
		out = append(out, h.def)
	}
	return out, nil
}

// Notice is what Claim did with one held definition, for the user to be told.
type Notice struct {
	Model, Kind, Name string
	// Written marks a definition the clean pass proved and the layer now holds.
	Written bool
	// Generation is the generation the write or the revert appended, 0 for none.
	Generation int
	// Reason is why a revert happened.
	Reason string
	// Restored says what stands after a revert: "the previous definition", "the seed" or
	// "nothing".
	Restored string
}

// Message is the notice as the user is told it.
func (n Notice) Message() string {
	if n.Written {
		return fmt.Sprintf("Observational memory: held %s definition %s for %s passed a clean pass and is written to the layer (generation %d)", n.Kind, n.Name, n.Model, n.Generation)
	}
	return fmt.Sprintf("Observational memory: held %s definition %s for %s was reverted (%s); %s stands", n.Kind, n.Name, n.Model, n.Reason, n.Restored)
}

// Claim settles model's held definitions after a pass, once each: when the pass was clean (no
// kernel error, no decode failure, no invariant refusal) each is written to the layer, as Put
// writes it; otherwise each is dropped and what stood before its first hold is put back (the
// previous definition, else the seed), with reason. A held file that no longer passes the
// checks is reverted whatever the pass. It returns what to notify.
func (s Store) Claim(model string, clean bool, reason string) ([]Notice, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "the pass was not clean"
	}
	var notices []Notice
	err := s.locked(func() error {
		for _, kind := range Kinds {
			names, err := rowsNames(filepath.Join(s.modelPath(model), "held", kind))
			if err != nil {
				return err
			}
			for _, name := range names {
				n, err := s.claimOne(model, kind, name, clean, reason)
				if err != nil {
					return err
				}
				notices = append(notices, n)
			}
		}
		return nil
	})
	return notices, err
}

// ClaimHeld is Claim for the held definitions one pass ran with (held, from the pass's layer
// snapshot), after nodecode's claim-batch: each is settled only while its held file still
// holds the text the pass ran with, so a definition held again (or first) after the pass read
// the layer waits for a pass of its own. Definitions not held are ignored.
func (s Store) ClaimHeld(held []Definition, clean bool, reason string) ([]Notice, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "the pass was not clean"
	}
	var notices []Notice
	err := s.locked(func() error {
		for _, d := range held {
			if d.State != StateHeld || !ValidKind(d.Kind) || !validName.MatchString(d.Name) {
				continue
			}
			data, err := os.ReadFile(s.heldPath(d.Model, d.Kind, d.Name))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if now, _, err := decodeFile(string(data)); err == nil && now.Text != strings.TrimRight(d.Text, "\n") {
				continue
			}
			n, err := s.claimOne(d.Model, d.Kind, d.Name, clean, reason)
			if err != nil {
				return err
			}
			notices = append(notices, n)
		}
		return nil
	})
	return notices, err
}

// claimOne settles one held file; the caller holds the store's lock.
func (s Store) claimOne(model, kind, name string, clean bool, reason string) (Notice, error) {
	notice := Notice{Model: model, Kind: kind, Name: name}
	heldPath := s.heldPath(model, kind, name)
	index, err := s.heldIndex(model)
	if err != nil {
		return Notice{}, err
	}
	d, fallback, report := s.load(heldPath, model, kind, name, index)
	if report != nil && report.Untracked {
		// No hold wrote it: it is removed, and nothing it names is restored (its fallback is as
		// untrusted as its text).
		if err := os.Remove(heldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Notice{}, err
		}
		notice.Reason, notice.Restored = report.Reason, s.standing(model, kind, name)
		return notice, nil
	}
	if report != nil {
		clean, reason = false, report.Reason
		// An unreadable held file has no fallback to trust but what stands written now.
		if data, err := os.ReadFile(heldPath); err == nil {
			if _, recorded, err := decodeFile(string(data)); err == nil {
				fallback = recorded
			}
		}
	}
	if clean {
		if _, err := s.PutLocked(d); err != nil {
			return Notice{}, err
		}
		if err := os.Remove(heldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Notice{}, err
		}
		if err := s.recordHeld(model, SeedKey(kind, name), ""); err != nil {
			return Notice{}, err
		}
		gens, err := s.Generations(model)
		if err != nil {
			return Notice{}, err
		}
		notice.Written, notice.Generation = true, len(gens)
		return notice, nil
	}
	notice.Reason = reason
	if err := os.Remove(heldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Notice{}, err
	}
	if err := s.recordHeld(model, SeedKey(kind, name), ""); err != nil {
		return Notice{}, err
	}
	if fallback == "" {
		// A held file without a recorded fallback leaves the written layer as it is.
		notice.Restored = s.standing(model, kind, name)
		return notice, nil
	}
	target := fallback
	if target == noFallback {
		target = ""
	}
	change, err := s.restore(model, kind, name, target)
	if err != nil {
		return Notice{}, err
	}
	if change != nil {
		g, err := s.appendGeneration(model, Generation{Op: OpRevert, Kind: kind, Name: name, Digest: change.Digest, Reason: reason})
		if err != nil {
			return Notice{}, err
		}
		notice.Generation = g.N
	}
	notice.Restored = s.standing(model, kind, name)
	return notice, nil
}

// standing names what stands for kind/name in model's layer: a written definition, the seed,
// or nothing.
func (s Store) standing(model, kind, name string) string {
	if _, err := os.Stat(s.Path(model, kind, name)); err == nil {
		return "the previous definition"
	}
	if _, ok := s.seeds()[SeedKey(kind, name)]; ok {
		return "the seed"
	}
	return "nothing"
}
