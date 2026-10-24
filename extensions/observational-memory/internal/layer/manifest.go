package layer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
)

// The generation operations.
const (
	// OpPut wrote a definition.
	OpPut = "put"
	// OpForget removed one.
	OpForget = "forget"
	// OpRevert put back what stood before a held definition's first hold (Digest), or removed
	// the name (no Digest).
	OpRevert = "revert"
	// OpRollback made the layer what it was at generation Target.
	OpRollback = "rollback"
	// OpBuild marks a build pass: the model was given the kernel's rules, the seeds and the
	// kinds and wrote its starting harness (its definitions are their own put generations).
	// It changes no file; it records that the model has a layer, with the evidence it saw.
	OpBuild = "build"
	// OpRevise marks a revision pass, with the evidence it was given; it changes no file.
	OpRevise = "revise"
	// OpEstablish records that probation established the definition its last put wrote; it
	// changes no file.
	OpEstablish = "establish"
	// OpMigrate records an instructions definition recorded before modes made an addition
	// (MigrateInstructions): a written one is rewritten with ModeExtend in its header (Digest,
	// the new content), which probation judges afresh (its evidence so far ran with the text in
	// the seed's place), a held one (Held) in its held file, which changes nothing written.
	OpMigrate = "migrate"
)

// Generation is one change to a model's layer, a line of its append-only manifest.
type Generation struct {
	// N numbers the generation from 1, in manifest order.
	N    int    `json:"gen"`
	Time string `json:"time"`
	Op   string `json:"op"`
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
	// Digest is the content written (the blob it is kept in); none for a removal.
	Digest  string `json:"digest,omitempty"`
	Session string `json:"session,omitempty"`
	// Target is a rollback's generation.
	Target int `json:"target,omitempty"`
	// Keep are the kind/name keys a rollback left as they were (RollbackKeeping): their
	// written content after it is what it was before it.
	Keep   []string `json:"keep,omitempty"`
	Reason string   `json:"reason,omitempty"`
	// Held marks a migration of a held definition: its held file changed, nothing written.
	Held bool `json:"held,omitempty"`
	// Evidence is the digest of the counted evidence a build or revision pass was given, and
	// Events the length of the model's usage ledger then: the next revision is due only once
	// passes were counted after it and the evidence changed.
	Evidence string `json:"evidence,omitempty"`
	Events   int    `json:"events,omitempty"`
}

// Describe is the generation as /om:status and notifications show it.
func (g Generation) Describe() string {
	switch g.Op {
	case OpRollback:
		return fmt.Sprintf("%d: rollback to %d", g.N, g.Target)
	case OpRevert:
		return fmt.Sprintf("%d: revert %s/%s (%s)", g.N, g.Kind, g.Name, g.Reason)
	case OpBuild:
		return fmt.Sprintf("%d: build pass", g.N)
	case OpRevise:
		return fmt.Sprintf("%d: revision pass", g.N)
	case OpEstablish:
		return fmt.Sprintf("%d: establish %s/%s (%s)", g.N, g.Kind, g.Name, g.Reason)
	case OpMigrate:
		what := ""
		if g.Held {
			what = " held"
		}
		return fmt.Sprintf("%d: migrate%s %s/%s (%s)", g.N, what, g.Kind, g.Name, g.Reason)
	}
	return fmt.Sprintf("%d: %s %s/%s", g.N, g.Op, g.Kind, g.Name)
}

func (s Store) manifestPath(model string) string {
	return filepath.Join(s.modelPath(model), "manifest.jsonl")
}

// Generations reads model's manifest in order. A missing manifest is empty; an unreadable line
// is skipped.
func (s Store) Generations(model string) ([]Generation, error) {
	data, err := os.ReadFile(s.manifestPath(model))
	if errors.Is(err, os.ErrNotExist) {
		return []Generation{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Generation{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var g Generation
		if json.Unmarshal(scanner.Bytes(), &g) != nil || g.Op == "" {
			continue
		}
		g.N = len(out) + 1
		out = append(out, g)
	}
	return out, scanner.Err()
}

// appendGeneration appends g to model's manifest, numbered after the last; the caller holds
// the store's lock.
func (s Store) appendGeneration(model string, g Generation) (Generation, error) {
	gens, err := s.Generations(model)
	if err != nil {
		return Generation{}, err
	}
	g.N = len(gens) + 1
	g.Time = s.now().UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(g)
	if err != nil {
		return Generation{}, err
	}
	if err := os.MkdirAll(s.modelPath(model), 0o700); err != nil {
		return Generation{}, err
	}
	f, err := os.OpenFile(s.manifestPath(model), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Generation{}, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return Generation{}, err
	}
	return g, nil
}

// Mark appends a generation that changes no file: a build or revision pass (OpBuild,
// OpRevise) with the evidence it was given. Any other op is refused.
func (s Store) Mark(model string, g Generation) (Generation, error) {
	if g.Op != OpBuild && g.Op != OpRevise {
		return Generation{}, fmt.Errorf("only a build or revision pass is marked, not %q", g.Op)
	}
	var out Generation
	err := s.locked(func() error {
		var err error
		out, err = s.appendGeneration(model, g)
		return err
	})
	return out, err
}

// LastPass is model's latest build or revision generation; false when it has none.
func (s Store) LastPass(model string) (Generation, bool, error) {
	gens, err := s.Generations(model)
	if err != nil {
		return Generation{}, false, err
	}
	for i := len(gens) - 1; i >= 0; i-- {
		if gens[i].Op == OpBuild || gens[i].Op == OpRevise {
			return gens[i], true, nil
		}
	}
	return Generation{}, false, nil
}

// Undecided is a written definition probation has not decided yet: the generation that
// wrote what stands for its name (its last put, a rollback that restored it, or the migration
// that made it an addition), with nothing after it that decided or replaced it (an establish,
// a revert, a forget, or a later put or rollback of that name).
type Undecided struct {
	Kind, Name string
	// Put is the generation that wrote it: an OpPut, the OpRollback that restored it (with
	// Kind, Name and the Digest restored filled in), or the OpMigrate that made it an addition.
	Put Generation
}

// writer is the generation that wrote a name's written content, with whether probation has
// decided it since and how. base is the generation whose prior state a retirement restores:
// gen's own, but for a migration, the base of the write it migrated (retiring a migrated
// definition restores what stood before it was first written, never its pre-migration text).
type writer struct {
	gen         Generation
	base        int
	decided     bool
	established bool
}

// writers replays gens: for each kind/name with written content, the generation that wrote it,
// and the names in the order they were first written. A rollback writes every name whose
// content it changed: a name it restored is a new write probation judges again (a rollback
// never brings back a definition without proof, not even one probation retired), and one it
// removed has no writer; the names it left as they were keep theirs. A revert writes the
// content it restored, already decided: what stood before is not judged again. A migration
// writes the migrated content as a new write probation judges, with events since the
// migration (what it was judged on before ran in the seed's place, a different prompt); a
// definition a revert put back stays decided, as it was never judged.
func writers(gens []Generation) (map[string]*writer, []string) {
	all := states(gens)
	out := map[string]*writer{}
	order := []string{}
	write := func(key string, w *writer) {
		if !containsKey(order, key) {
			order = append(order, key)
		}
		out[key] = w
	}
	for i, g := range gens {
		key := SeedKey(g.Kind, g.Name)
		switch g.Op {
		case OpPut:
			write(key, &writer{gen: g, base: g.N})
		case OpEstablish:
			if w, ok := out[key]; ok {
				w.decided, w.established = true, true
			}
		case OpForget:
			delete(out, key)
		case OpMigrate:
			w, ok := out[key]
			if !ok || g.Held {
				break
			}
			if w.decided && !w.established {
				// Put back by a revert, never judged: it stays so, now for the migrated content.
				migrated := *w
				migrated.gen.Digest = g.Digest
				out[key] = &migrated
				break
			}
			write(key, &writer{gen: g, base: w.base})
		case OpRevert:
			if g.Digest == "" {
				delete(out, key)
			} else {
				write(key, &writer{gen: g, base: g.N, decided: true})
			}
		case OpRollback:
			before, after := all[i], all[i+1]
			keys := []string{}
			for k := range before {
				keys = append(keys, k)
			}
			for k := range after {
				if _, ok := before[k]; !ok {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				if before[k] == after[k] {
					continue
				}
				if after[k] == "" {
					delete(out, k)
					continue
				}
				kind, name := splitKey(k)
				restored := g
				restored.Kind, restored.Name, restored.Digest = kind, name, after[k]
				write(k, &writer{gen: restored, base: g.N})
			}
		}
	}
	return out, order
}

func containsKey(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// Undecided lists model's written definitions probation has not decided (Undecided), in the
// order their names were first written.
func (s Store) Undecided(model string) ([]Undecided, error) {
	gens, err := s.Generations(model)
	if err != nil {
		return nil, err
	}
	ws, order := writers(gens)
	out := []Undecided{}
	for _, key := range order {
		if w, ok := ws[key]; ok && !w.decided {
			out = append(out, Undecided{Kind: w.gen.Kind, Name: w.gen.Name, Put: w.gen})
		}
	}
	return out, nil
}

// Written is what wrote one of a model's written definitions, as /om:status lists it.
type Written struct {
	// Generation is the generation that wrote it (a put, a rollback that restored it, or a
	// revert that put it back).
	Generation int
	// Decided marks a definition probation has decided; Established one it established.
	Decided, Established bool
}

// WrittenBy maps each kind/name with written content in model's manifest to what wrote it.
func (s Store) WrittenBy(model string) (map[string]Written, error) {
	gens, err := s.Generations(model)
	if err != nil {
		return nil, err
	}
	ws, _ := writers(gens)
	out := make(map[string]Written, len(ws))
	for key, w := range ws {
		out[key] = Written{Generation: w.gen.N, Decided: w.decided, Established: w.established}
	}
	return out, nil
}

// Settle records probation's decision about model's written definition of kind named name
// (Undecided): established, a generation saying so; retired, what stood before the generation
// that wrote it is restored (the earlier definition, else the seed; for a migrated one, what
// stood before the write it migrated) as a revert with reason. A
// name probation already decided, or whose file no longer holds what that generation wrote, is
// left as it is (ErrNotFound).
func (s Store) Settle(model, kind, name string, established bool, reason string) (Generation, error) {
	var out Generation
	err := s.locked(func() error {
		gens, err := s.Generations(model)
		if err != nil {
			return err
		}
		all := states(gens)
		key := SeedKey(kind, name)
		ws, _ := writers(gens)
		w, ok := ws[key]
		if !ok || w.decided || w.base < 1 || all[len(all)-1][key] != w.gen.Digest {
			return ErrNotFound
		}
		if established {
			out, err = s.appendGeneration(model, Generation{Op: OpEstablish, Kind: kind, Name: name, Reason: reason})
			return err
		}
		before := all[w.base-1][key]
		change, err := s.restore(model, kind, name, before)
		if err != nil {
			return err
		}
		digest := before
		if change != nil && change.Removed {
			digest = ""
		}
		out, err = s.appendGeneration(model, Generation{Op: OpRevert, Kind: kind, Name: name, Digest: digest, Reason: reason})
		return err
	})
	return out, err
}

// states replays gens: states[n] maps each kind/name to the digest written for it after
// generation n (states[0] is empty: only seeds stand).
func states(gens []Generation) []map[string]string {
	out := make([]map[string]string, 0, len(gens)+1)
	out = append(out, map[string]string{})
	for _, g := range gens {
		next := map[string]string{}
		if g.Op == OpRollback && g.Target >= 0 && g.Target < len(out) {
			for k, v := range out[g.Target] {
				next[k] = v
			}
			prev := out[len(out)-1]
			for _, k := range g.Keep {
				if v, ok := prev[k]; ok {
					next[k] = v
				} else {
					delete(next, k)
				}
			}
		} else {
			for k, v := range out[len(out)-1] {
				next[k] = v
			}
			key := SeedKey(g.Kind, g.Name)
			switch {
			case g.Op == OpForget, g.Op == OpRevert && g.Digest == "":
				delete(next, key)
			case g.Op == OpPut, g.Op == OpRevert, g.Op == OpMigrate && !g.Held:
				next[key] = g.Digest
			}
		}
		out = append(out, next)
	}
	return out
}

// Change is one file a rollback or a revert changed.
type Change struct {
	Kind, Name string
	// Removed marks a name the change removed; otherwise it restored Digest.
	Removed bool
	Digest  string
}

// Rollback makes model's layer what it was at generation n (0 is no definitions: every seed
// stands), appends a generation saying so, and drops the model's held definitions, which were
// held over the layer being rolled back. Only names some generation wrote are touched: a file
// no generation recorded is left as it is. A rollback that would restore content a revert took
// away is refused (also the content that content was migrated from, which the next layer read
// would migrate into it again), and what it restores is judged by probation again
// (Undecided). It returns the generation and what changed.
func (s Store) Rollback(model string, n int, session string) (Generation, []Change, error) {
	return s.RollbackKeeping(model, n, session, nil)
}

// RollbackKeeping is Rollback leaving every name keep reports (nil keeps none) as it is: neither
// removed nor restored, its held definition kept, and its written content after the rollback
// what it was before (Generation.Keep). The memory tool's rollback keeps the rules, which the
// harness writes and decides, not the model.
func (s Store) RollbackKeeping(model string, n int, session string, keep func(kind, name string) bool) (Generation, []Change, error) {
	kept := func(key string) bool {
		if keep == nil {
			return false
		}
		kind, name := splitKey(key)
		return keep(kind, name)
	}
	var gen Generation
	var changes []Change
	err := s.locked(func() error {
		gens, err := s.Generations(model)
		if err != nil {
			return err
		}
		if n < 0 || n > len(gens) {
			return fmt.Errorf("generation %d does not exist; %s has generations 0 to %d", n, model, len(gens))
		}
		all := states(gens)
		target := all[n]
		current := all[len(all)-1]
		// migratedFrom maps each kind/name's migrated content (an OpMigrate's digest) to the
		// content it was migrated from: the same definition, which a layer read migrates again.
		migratedFrom := map[string]map[string]string{}
		for i, g := range gens {
			if g.Op != OpMigrate || g.Held || g.Digest == "" {
				continue
			}
			key := SeedKey(g.Kind, g.Name)
			if migratedFrom[key] == nil {
				migratedFrom[key] = map[string]string{}
			}
			migratedFrom[key][g.Digest] = all[i][key]
		}
		// covers reports whether digest is reverted or content reverted was migrated from.
		covers := func(key, reverted, digest string) bool {
			for x, steps := reverted, 0; x != "" && steps <= len(gens); x, steps = migratedFrom[key][x], steps+1 {
				if x == digest {
					return true
				}
			}
			return false
		}
		// A rollback never brings back what a revert took away (probation retired it, or a
		// held definition's failed pass put back what stood before), nor what it was migrated
		// from: it would stand again with no proof. Defining it again holds it, and probation
		// judges it afresh.
		for i, g := range gens {
			if g.Op != OpRevert {
				continue
			}
			key := SeedKey(g.Kind, g.Name)
			reverted := all[i][key]
			if kept(key) {
				continue
			}
			if reverted != "" && target[key] != "" && covers(key, reverted, target[key]) && !covers(key, reverted, current[key]) {
				return fmt.Errorf("refused: generation %d restores %s, which generation %d reverted (%s); roll back to a generation that does not restore it, or define it again so a clean pass and probation judge it", n, key, g.N, g.Reason)
			}
		}
		keys := map[string]bool{}
		for _, state := range all {
			for k := range state {
				keys[k] = true
			}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		keepKeys := []string{}
		for _, key := range sorted {
			kind, name := splitKey(key)
			if kept(key) {
				if target[key] != current[key] {
					keepKeys = append(keepKeys, key)
				}
				continue
			}
			change, err := s.restore(model, kind, name, target[key])
			if err != nil {
				return err
			}
			if change != nil {
				changes = append(changes, *change)
			}
		}
		if err := s.dropHeld(model, kept); err != nil {
			return err
		}
		gen, err = s.appendGeneration(model, Generation{Op: OpRollback, Target: n, Session: session, Keep: keepKeys})
		return err
	})
	return gen, changes, err
}

// restore makes model's file for kind/name hold the blob digest names ("" removes it); nil
// when it already does. The caller holds the store's lock.
func (s Store) restore(model, kind, name, digest string) (*Change, error) {
	path := s.Path(model, kind, name)
	have := ""
	if data, err := os.ReadFile(path); err == nil {
		have = Digest(string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if have == digest {
		return nil, nil
	}
	if digest == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return &Change{Kind: kind, Name: name, Removed: true}, nil
	}
	data, err := os.ReadFile(s.blobPath(model, digest))
	if err != nil {
		return nil, fmt.Errorf("the content of %s/%s at that generation is missing: %w", kind, name, err)
	}
	if err := writeAtomic(path, string(data)); err != nil {
		return nil, err
	}
	return &Change{Kind: kind, Name: name, Digest: digest}, nil
}

func splitKey(key string) (string, string) {
	kind, name, _ := strings.Cut(key, "/")
	return kind, name
}

// --- the floor's record ------------------------------------------------------------

func (s Store) floorPath() string { return filepath.Join(s.Dir, "floor.jsonl") }

// RecordFloor appends a floor lowering or engagement to the layer's floor record, under the
// store's lock. It is a kernel.Floor's Record.
func (s Store) RecordFloor(l kernel.Lowering) error {
	line, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return s.locked(func() error {
		if err := os.MkdirAll(s.Dir, 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(s.floorPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
		return f.Sync()
	})
}

// FloorRecord reads the floor record in order; a missing one is empty.
func (s Store) FloorRecord() ([]kernel.Lowering, error) {
	data, err := os.ReadFile(s.floorPath())
	if errors.Is(err, os.ErrNotExist) {
		return []kernel.Lowering{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []kernel.Lowering{}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var l kernel.Lowering
		if len(line) > 0 && json.Unmarshal(line, &l) == nil {
			out = append(out, l)
		}
	}
	return out, nil
}
