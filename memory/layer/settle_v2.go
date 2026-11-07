package layer

import (
	"errors"
	"fmt"
	"os"
	"slices"
)

// The layer's part of decision 24's settlement rules (docs/brain-memory.md; docs/memory-v2.md,
// "Settling a definition"): the read-time scope a settlement records on its put, the hold that
// keeps a security-weakening definition out of the layer, the re-hold of an active definition
// whose target another one just settled on, and the rollback a security hit makes. rules.SettleV2
// decides; these only keep what it decided.

// OpHoldInsecure records a model-written definition that matches the security-weakening patterns
// (contracts/insecure.md; decision 19): its content is kept in the model's blobs (Digest) with
// the pattern it matched (Reason), it is never held as a file, so no pass runs or settles it, and
// /om:status lists it (HeldInsecure). It changes no file of the layer.
const OpHoldInsecure = "hold-insecure"

// Scope is where a settled definition applies: the repositories (frame.repoId, "" for none) and
// task families (the commonest path extension of a compaction's records, "" for none) of the
// compactions that settled it. An empty list does not restrict.
type Scope struct {
	Repos    []string `json:"repos,omitempty"`
	Families []string `json:"families,omitempty"`
}

// Covers reports whether a compaction in repo and family is inside the scope.
func (s *Scope) Covers(repo, family string) bool {
	if s == nil {
		return true
	}
	return (len(s.Repos) == 0 || slices.Contains(s.Repos, repo)) && (len(s.Families) == 0 || slices.Contains(s.Families, family))
}

// String is the scope as /om:status and notices show it.
func (s *Scope) String() string {
	if s == nil {
		return "everywhere"
	}
	name := func(v string) string {
		if v == "" {
			return "none"
		}
		return v
	}
	repos, families := []string{}, []string{}
	for _, r := range s.Repos {
		repos = append(repos, name(r))
	}
	for _, f := range s.Families {
		families = append(families, name(f))
	}
	return fmt.Sprintf("repositories %v, task families %v", repos, families)
}

// scopes maps each kind/name of model's written layer to the scope of the put that wrote its
// current content (Generation.Scope), when that put recorded one.
func (s Store) scopes(model string) (map[string]*Scope, error) {
	gens, err := s.Generations(model)
	if err != nil {
		return nil, err
	}
	all := states(gens)
	current := all[len(all)-1]
	out := map[string]*Scope{}
	for _, g := range gens {
		key := SeedKey(g.Kind, g.Name)
		if g.Op == OpPut && g.Digest != "" && g.Digest == current[key] {
			out[key] = g.Scope
		}
	}
	return out, nil
}

// InScope is h with every written definition whose scope does not cover repo and family left
// out, so its name resolves to what stands under it (the seed, or nothing): a settled definition
// applies only where its evidence certified it (decision 24, read-time scope). Held definitions
// and written ones without a scope stay.
func (h Harness) InScope(repo, family string) Harness {
	out := Harness{Model: h.Model, defs: make(map[string]Definition, len(h.defs)), seeds: h.seeds, first: h.first}
	for k, d := range h.defs {
		if d.State == StateWritten && !d.Scope.Covers(repo, family) {
			continue
		}
		out.defs[k] = d
	}
	return out
}

// OutOfScope are h's written definitions InScope(repo, family) leaves out, by kind and name.
func (h Harness) OutOfScope(repo, family string) []Definition {
	out := []Definition{}
	for _, d := range h.List("") {
		if d.State == StateWritten && !d.Scope.Covers(repo, family) {
			out = append(out, d)
		}
	}
	return out
}

// HoldInsecure keeps d, a model-written definition that matches the security-weakening pattern
// rule, out of the layer (OpHoldInsecure): its content goes to the model's blobs and a generation
// records it, so nothing runs, settles or proposes over it. It returns the generation.
func (s Store) HoldInsecure(d Definition, rule string) (Generation, error) {
	var g Generation
	err := s.locked(func() error {
		var err error
		g, err = s.HoldInsecureLocked(d, rule)
		return err
	})
	return g, err
}

// HoldInsecureLocked is HoldInsecure for a caller that holds the store's lock.
func (s Store) HoldInsecureLocked(d Definition, rule string) (Generation, error) {
	if !ValidKind(d.Kind) || !validName.MatchString(d.Name) {
		return Generation{}, fmt.Errorf("%s/%s is not a definition name", d.Kind, d.Name)
	}
	d.Engine, d.Time = s.Engine, s.now()
	content := encodeFile(d, "")
	blob := s.blobPath(d.Model, Digest(content))
	if err := writeAtomic(blob, content); err != nil {
		return Generation{}, err
	}
	return s.appendGeneration(d.Model, Generation{Op: OpHoldInsecure, Kind: d.Kind, Name: d.Name, Digest: Digest(content), Session: d.Session,
		Reason: "it weakens security (" + rule + "): held, never applied, settled or proposed over without a user's fresh quoted words"})
}

// HeldInsecure are model's OpHoldInsecure generations, oldest first: what /om:status lists.
func (s Store) HeldInsecure(model string) ([]Generation, error) {
	gens, err := s.Generations(model)
	if err != nil {
		return nil, err
	}
	out := []Generation{}
	for _, g := range gens {
		if g.Op == OpHoldInsecure {
			out = append(out, g)
		}
	}
	return out, nil
}

// putGeneration is the latest put generation of key whose content is digest, and the index in
// gens it sits at; -1 for none.
func putGeneration(gens []Generation, key, digest string) int {
	at := -1
	for i, g := range gens {
		if g.Op == OpPut && SeedKey(g.Kind, g.Name) == key && g.Digest == digest {
			at = i
		}
	}
	return at
}

// Recheck holds model's written definition of kind named name again, with the text it stands
// with, so the next compactions run it as held and rules.SettleV2 judges it afresh (decision 24,
// the active set: a definition sharing a target with one that just settled re-enters a hold
// window). A revert then puts back what stood before the put that wrote it, not itself. false
// when nothing is written for the name, or it is already held.
func (s Store) Recheck(model, kind, name string) (Definition, bool, error) {
	var out Definition
	var ok bool
	err := s.locked(func() error {
		var err error
		out, ok, err = s.RecheckLocked(model, kind, name)
		return err
	})
	return out, ok, err
}

// RecheckLocked is Recheck for a caller that holds the store's lock.
func (s Store) RecheckLocked(model, kind, name string) (Definition, bool, error) {
	if !ValidKind(kind) || !validName.MatchString(name) {
		return Definition{}, false, nil
	}
	if _, err := os.Stat(s.heldPath(model, kind, name)); err == nil {
		return Definition{}, false, nil
	}
	data, err := os.ReadFile(s.Path(model, kind, name))
	if errors.Is(err, os.ErrNotExist) {
		return Definition{}, false, nil
	}
	if err != nil {
		return Definition{}, false, err
	}
	d, _, err := decodeFile(string(data))
	if err != nil {
		return Definition{}, false, nil
	}
	gens, err := s.Generations(model)
	if err != nil {
		return Definition{}, false, err
	}
	fallback := noFallback
	if at := putGeneration(gens, SeedKey(kind, name), Digest(string(data))); at >= 0 {
		if before := states(gens)[at][SeedKey(kind, name)]; before != "" {
			fallback = before
		}
	} else {
		// No put recorded it: what stands now is the only thing a revert can trust.
		fallback = Digest(string(data))
		if err := writeAtomic(s.blobPath(model, fallback), string(data)); err != nil {
			return Definition{}, false, err
		}
	}
	d.Engine, d.Time = s.Engine, s.now()
	content := encodeFile(d, fallback)
	if err := writeAtomic(s.heldPath(model, kind, name), content); err != nil {
		return Definition{}, false, err
	}
	if err := s.recordHeld(model, d.Key(), Digest(content)); err != nil {
		return Definition{}, false, err
	}
	d.State = StateHeld
	return d, true, nil
}

// RollbackBefore rolls model's layer back to the generation before the one that wrote its
// current definition of kind named name, leaving every name keep reports as it is, with reason
// recorded on the rollback: decision 24's answer to a security hit, since evolving further does
// not clean what entered. It is code acting on code evidence, not a model's or a tool's request,
// so no quote gate applies (decision 2 gates the memory tool's layer writes). When that rollback
// is refused (it would restore something a revert took away) the name alone is removed. It
// returns the generation that changed the layer, and false when nothing is written for the name.
func (s Store) RollbackBefore(model, kind, name, reason string, keep func(kind, name string) bool) (Generation, bool, error) {
	var gen Generation
	var done bool
	err := s.locked(func() error {
		data, err := os.ReadFile(s.Path(model, kind, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		gens, err := s.Generations(model)
		if err != nil {
			return err
		}
		if at := putGeneration(gens, SeedKey(kind, name), Digest(string(data))); at >= 0 {
			keeping := func(k, n string) bool { return (k != kind || n != name) && keep != nil && keep(k, n) }
			g, _, err := s.rollbackLocked(model, at, keeping, reason)
			if err == nil {
				gen, done = g, true
				return nil
			}
		}
		if err := os.Remove(s.Path(model, kind, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		g, err := s.appendGeneration(model, Generation{Op: OpRevert, Kind: kind, Name: name, Reason: reason})
		gen, done = g, err == nil
		return err
	})
	return gen, done, err
}

// rollbackLocked is rollbackKeeping for a caller that holds the store's lock.
func (s Store) rollbackLocked(model string, n int, keep func(kind, name string) bool, reason string) (Generation, []Change, error) {
	locked := s
	locked.Lock = heldLock{}
	return locked.rollbackKeeping(model, n, "", keep, reason)
}

// heldLock is a Locker for code already inside the store's lock.
type heldLock struct{}

func (heldLock) Locked(fn func() error) error { return fn() }
