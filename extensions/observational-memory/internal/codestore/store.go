// Package codestore is the memory tool's per-project store of kept code units
// (docs/code-tool.md, "Persistence"): top-level functions, methods, types and constants the
// agent's calls compiled, one unit per bound name, shared by every PiG process on one project
// and replayed into every new namespace.
//
// The store is one file, units.json, an array of {name, src, digest, written}. Writes are
// read-merge-write under a file lock (units.lock), then a temporary file and a rename, so
// several processes may keep and forget at once, last writer winning by name. Only the
// extension reads and writes it; the child process never learns its path.
//
// Every bound is derived, none tuned: a unit is at most one recall page (om.RecallPageChars), so
// kept(name) shows it whole; a store holds at most om.MaxLimit units, so kept() can list every
// one to be forgotten; kept() pages om.DefaultLimit rows at a time.
//
// Every unit is checked when it is kept (the code policy, the bounds, the variable check and the
// injected provenance check) and again on every load (the code policy, the bounds, the variable
// check against the store and the digest; provenance needs a branch, so it is not repeated). A
// unit that fails at load is listed as refused with the rule it broke, is never sent to the
// child, and stays in the store so forget can remove it.
//
// Kept units load in every project, trusted or not (decision 8): they are written by the model
// under PiG's config root, never shipped in the repository.
//
// The package imports codepolicy (the policy, unit extraction and free names) and om (the
// bounds); provenance arrives as an injected func, so it depends on neither rules nor ledger.
package codestore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// The store's derived bounds.
const (
	// UnitChars is the most characters one unit's source holds: one recall page, so
	// kept(name) can show it whole in one result.
	UnitChars = om.RecallPageChars
	// MaxUnits is the most units one store holds: every unit must be listable by kept() (at
	// most om.MaxLimit rows) so it can be forgotten.
	MaxUnits = om.MaxLimit
	// PageRows is how many units one page of kept() lists.
	PageRows = om.DefaultLimit
)

// The store's file names inside its directory.
const (
	unitsFile = "units.json"
	lockFile  = "units.lock"
)

// Unit is one stored unit as units.json holds it.
type Unit struct {
	Name string `json:"name"`
	Src  string `json:"src"`
	// Digest is the hex SHA-256 of Src; a unit whose digest does not match is refused at load.
	Digest string `json:"digest"`
	// Written is when it was kept, RFC 3339 in UTC.
	Written string `json:"written"`
}

// Store is one project's units store.
type Store struct {
	// Dir holds units.json and units.lock (Dir(configHome, projectRoot) in the extension, a
	// temporary directory in tests).
	Dir string
	// Now is the clock for Written; time.Now when nil.
	Now func() time.Time
}

// Dir is the store directory of the project at projectRoot under PiG's config root:
// <config>/observational-memory/memories/code/<project key>, beside the curated store.
func Dir(configHome, projectRoot string) string {
	return filepath.Join(configHome, "observational-memory", "memories", "code", ProjectKey(projectRoot))
}

// ProjectKey is the first 16 hex digits of the SHA-256 of the cleaned absolute project root
// (PiG's project root, else the session's cwd).
func ProjectKey(projectRoot string) string {
	root := projectRoot
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(sum[:])[:16]
}

// Digest is the hex SHA-256 of a unit's source.
func Digest(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

func (s Store) path() string { return filepath.Join(s.Dir, unitsFile) }

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// read reads units.json as stored, in file order; a missing file is an empty store.
func (s Store) read() ([]Unit, error) {
	data, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var units []Unit
	if err := json.Unmarshal(data, &units); err != nil {
		return nil, fmt.Errorf("the kept units file is not a JSON array of units, so nothing loads and nothing is written until it is repaired or removed: %w", err)
	}
	return units, nil
}

// write replaces units.json with units, sorted by name, through a temporary file and a rename,
// so a reader never sees a half-written file.
func (s Store) write(units []Unit) error {
	slices.SortStableFunc(units, func(a, b Unit) int { return strings.Compare(a.Name, b.Name) })
	data, err := json.MarshalIndent(units, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, unitsFile+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(data, '\n'))
	if serr := tmp.Sync(); werr == nil {
		werr = serr
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(name, s.path())
	}
	if werr != nil {
		_ = os.Remove(name)
	}
	return werr
}

// locked runs fn holding the store's lock, shared by every process that writes it.
func (s Store) locked(fn func() error) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return withLock(filepath.Join(s.Dir, lockFile), fn)
}

// Refused is a unit that was not kept, or that is stored but does not load, with the reason.
type Refused struct {
	Name, Reason string
}

// KeepResult is what one call's keep did.
type KeepResult struct {
	// Kept are the units written (or already stored with the same source), in call order.
	Kept []Unit
	// Refused are the call's units that were not kept, in call order, each with its reason.
	Refused []Refused
	// Replaced are the kept names whose stored unit another process wrote since the live
	// namespace loaded it (its digest differs from the one the caller saw): last writer wins.
	Replaced []string
}

// Lines are the result lines the call reports: one line naming every unit kept, then one per
// unit refused with the reason, then one per name that replaced another process's unit.
func (r KeepResult) Lines() []string {
	var out []string
	if len(r.Kept) > 0 {
		names := make([]string, len(r.Kept))
		for i, u := range r.Kept {
			names[i] = u.Name
		}
		out = append(out, "kept: "+strings.Join(names, ", "))
	}
	for _, f := range r.Refused {
		out = append(out, "not kept: "+f.Name+": "+f.Reason)
	}
	for _, n := range r.Replaced {
		out = append(out, "replaced: the stored "+n+" was not the one this namespace loaded (another process kept it since this namespace started, or it did not load); this call's definition is the one stored now")
	}
	return out
}

// The reasons a unit is refused at keep time beside the policy's and provenance's own.
const (
	reasonVariable = "refers to variable %s, which is not kept: pass it as a parameter or make it a constant"
	reasonDepends  = "depends on %s"
	reasonFull     = "the store already holds %d units, the most kept() can list: forget one first"
)

// Keep stores the units one call compiled (codepolicy.Program.Units of the declaration chunks
// the child reported compiled), in call order, under the store's lock: the store is read,
// merged by name and written. A name defined twice in the call keeps its last definition.
//
// Each unit must be at most one recall page and pass codepolicy.CheckUnit; check, the injected
// provenance check (provenance.Code in the extension), must accept its source; and every free
// identifier must name another unit kept in this call or already stored, a seeded or owned
// name, or an allow-listed package (the variable check). A unit naming anything else (a
// namespace variable, or a name no call defined) is refused, and so is every unit of the call
// that depends on a refused one, transitively. A new name past MaxUnits is refused with
// "forget one first".
//
// seen are the digests of the units the live namespace loaded or kept since it started, by
// name: a kept unit replacing a stored one whose digest is not the seen one was written by
// another process meanwhile, and is reported in Replaced (nil seen reports none). Keep never returns an error for a
// refusal, only for a store it cannot read or write.
func (s Store) Keep(units []codepolicy.Unit, check func(src string) error, seen map[string]string) (KeepResult, error) {
	var res KeepResult
	// The last definition of each name wins, at the position of that last definition.
	var cands []codepolicy.Unit
	for i, u := range units {
		if !slices.ContainsFunc(units[i+1:], func(v codepolicy.Unit) bool { return v.Name == u.Name }) {
			cands = append(cands, u)
		}
	}
	if len(cands) == 0 {
		return res, nil
	}
	err := s.locked(func() error {
		stored, err := s.read()
		if err != nil {
			return err
		}
		storedNames := map[string]bool{}
		for _, u := range stored {
			storedNames[u.Name] = true
		}
		refused := make([]string, len(cands))
		for i, u := range cands {
			refused[i] = keepCheck(u, check)
		}
		// The variable check: a free name must be a unit of this call, a stored unit, or
		// seeded.
		inCall := map[string]int{}
		for i, u := range cands {
			inCall[u.Name] = i
		}
		free := make([][]string, len(cands))
		for i, u := range cands {
			free[i] = codepolicy.FreeNames(u.Src)
			if refused[i] != "" {
				continue
			}
			for _, n := range free[i] {
				if _, ok := inCall[n]; !ok && !storedNames[n] && !codepolicy.Seeded(n) {
					refused[i] = fmt.Sprintf(reasonVariable, n)
					break
				}
			}
		}
		// Dependents of refused units, and the store bound, to a fixpoint: refusing a unit
		// only frees room, so a unit the bound refused stays refused.
		for changed := true; changed; {
			changed = false
			for i := range cands {
				if refused[i] != "" {
					continue
				}
				for _, n := range dependsOn(cands[i], free[i]) {
					if j, ok := inCall[n]; ok && j != i && refused[j] != "" {
						refused[i], changed = fmt.Sprintf(reasonDepends, n), true
						break
					}
				}
			}
			count := len(storedNames)
			for i, u := range cands {
				if refused[i] == "" && !storedNames[u.Name] {
					count++
					if count > MaxUnits {
						refused[i], changed = fmt.Sprintf(reasonFull, MaxUnits), true
						count--
					}
				}
			}
		}
		now := s.now().UTC().Format(time.RFC3339)
		dirty := false
		for i, u := range cands {
			if refused[i] != "" {
				res.Refused = append(res.Refused, Refused{Name: u.Name, Reason: refused[i]})
				continue
			}
			unit := Unit{Name: u.Name, Src: u.Src, Digest: Digest(u.Src), Written: now}
			at := slices.IndexFunc(stored, func(v Unit) bool { return v.Name == u.Name })
			switch {
			case at < 0:
				stored, dirty = append(stored, unit), true
			case stored[at].Digest == unit.Digest && stored[at].Src == unit.Src:
				unit = stored[at]
			default:
				if d, ok := seen[u.Name]; seen != nil && (!ok || d != stored[at].Digest) {
					res.Replaced = append(res.Replaced, u.Name)
				}
				// Every entry under the name goes: a hand-edited store may hold it twice.
				stored = slices.DeleteFunc(stored, func(v Unit) bool { return v.Name == u.Name })
				stored, dirty = append(stored, unit), true
			}
			res.Kept = append(res.Kept, unit)
		}
		if !dirty {
			return nil
		}
		return s.write(stored)
	})
	if err != nil {
		return KeepResult{}, err
	}
	return res, nil
}

// keepCheck is one unit's own checks at keep time: the unit bound, the code policy as a lone
// unit, and provenance. It returns the refusal's reason, "" when the unit passes.
func keepCheck(u codepolicy.Unit, check func(src string) error) string {
	if n := utf8.RuneCountInString(u.Src); n > UnitChars {
		return fmt.Sprintf("the unit is %d characters, and a kept unit is at most %d (one recall page, so kept(%q) shows it whole)", n, UnitChars, u.Name)
	}
	if err := codepolicy.CheckUnit(u.Name, u.Src); err != nil {
		return err.Error()
	}
	if check != nil {
		if err := check(u.Src); err != nil {
			return err.Error()
		}
	}
	return ""
}

// dependsOn are the names a unit depends on: its free names, which hold a method's receiver
// type.
func dependsOn(u codepolicy.Unit, free []string) []string {
	if recv := codepolicy.ReceiverType(u.Name); recv != "" && !slices.Contains(free, recv) {
		return append(slices.Clone(free), recv)
	}
	return free
}

// ForgetResult is what forget did.
type ForgetResult struct {
	// Unit is the unit removed (the first entry under the name when a hand-edited store held it
	// twice; every entry under the name is removed).
	Unit Unit
	// Stub is the source that shadows a forgotten function in the live namespace until its next
	// restart (codepolicy.ForgottenStub), "" when the unit is not a plain function.
	Stub string
	// Restart is true when the live namespace must restart to match the store: a forgotten
	// method, type or constant cannot be shadowed.
	Restart bool
	// Dependents are the stored units that refer to it (for a type, its methods too), which
	// fail to load until forgotten or redefined.
	Dependents []string
}

// Text is forget's answer to the code: what was forgotten, what happens to the live namespace,
// and the units that depend on it.
func (r ForgetResult) Text() string {
	var b strings.Builder
	b.WriteString("forgot " + r.Unit.Name + ": it is removed from the store and no later namespace loads it.")
	if r.Restart {
		b.WriteString(" The namespace restarts from the kept definitions when this call returns, since a forgotten method, type or constant cannot be removed from a live namespace: unsaved variables are gone then.")
	} else {
		b.WriteString(" Until the namespace's next restart, calls to it fail with a \"was forgotten\" panic; variables are kept.")
	}
	if len(r.Dependents) > 0 {
		b.WriteString("\nThese kept units depend on it and fail to load until they are forgotten or " + r.Unit.Name + " is defined again: " + strings.Join(r.Dependents, ", "))
	}
	return b.String()
}

// Forget removes the unit stored under name, whatever its state (a unit that fails the
// load-time check stays in the store until it is forgotten), under the store's lock. A name
// not in the store is an error naming the stored names.
func (s Store) Forget(name string) (ForgetResult, error) {
	var res ForgetResult
	err := s.locked(func() error {
		stored, err := s.read()
		if err != nil {
			return err
		}
		at := slices.IndexFunc(stored, func(v Unit) bool { return v.Name == name })
		if at < 0 {
			return notKept(name, stored)
		}
		res.Unit = stored[at]
		stored = slices.DeleteFunc(stored, func(v Unit) bool { return v.Name == name })
		if err := s.write(stored); err != nil {
			return err
		}
		res.Stub, _ = codepolicy.ForgottenStub(name, res.Unit.Src)
		res.Restart = res.Stub == ""
		for _, u := range stored {
			if u.Name != name && refersTo(u, name) {
				res.Dependents = append(res.Dependents, u.Name)
			}
		}
		slices.Sort(res.Dependents)
		res.Dependents = slices.Compact(res.Dependents)
		return nil
	})
	if err != nil {
		return ForgetResult{}, err
	}
	return res, nil
}

// refersTo reports whether stored unit u depends on name: it names it, or it is a method of it.
func refersTo(u Unit, name string) bool {
	if codepolicy.ReceiverType(u.Name) == name {
		return true
	}
	return slices.Contains(codepolicy.FreeNames(u.Src), name)
}

// notKept is the error for a name the store does not hold.
func notKept(name string, stored []Unit) error {
	if len(stored) == 0 {
		return fmt.Errorf("no kept unit is named %s: nothing is kept", name)
	}
	names := make([]string, 0, len(stored))
	for _, u := range stored {
		names = append(names, u.Name)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) > PageRows {
		return fmt.Errorf("no kept unit is named %s: kept() lists the %d kept units", name, len(names))
	}
	return fmt.Errorf("no kept unit is named %s: kept are %s", name, strings.Join(names, ", "))
}
