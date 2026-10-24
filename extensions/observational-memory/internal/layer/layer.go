// Package layer is the harness the model builds on top of the kernel (docs/memory-interface.md,
// section 8), after nodecode's layer files (organism/scribe.lisp) and held redefinitions
// (organism/provisional.lisp).
//
// A definition is declarative data (a query, a format declaration, a table, text), never code,
// of one of Kinds, named, for one model (or AllModels). Each lives in one file, its only home:
//
//	<store>/harness/<model>/<kind>/<name>.rows
//
// written atomically (a temp file beside it and one rename) with a header naming the kind,
// the name, the model, the time, the session and, for an override of a shipped seed, the
// seed's digest. Every change appends a generation to the model's manifest, so the model's
// harness can be rolled back to any earlier generation. A new definition can instead be held:
// it applies to the next pass only and is written once that pass is clean, or reverted to what
// stood before its first hold. An override whose seed changed since it was written is stale:
// it is skipped and reported, and the new seed stands.
//
// Every definition passes kernel.Check (through the store's floor) when it is made and again
// when it is loaded, so a hand-edited or crafted file that touches the kernel is refused at
// load with the same predicate that would have refused it at definition time.
package layer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The definition kinds.
const (
	// KindFormat is a table format declared as data.
	KindFormat = "format"
	// KindSchema is the fields a model's notes carry beyond the kernel's.
	KindSchema = "schema"
	// KindInstructions is a worker stage's instructions, appended after the kernel's rules.
	KindInstructions = "instructions"
	// KindSummary is the memory query that selects and groups what the summary shows.
	KindSummary = "summary"
	// KindTool is a named query registered as a tool (section 6).
	KindTool = "tool"
	// KindDetector is a query that counts a failure.
	KindDetector = "detector"
	// KindRemedy is a rule's remedy: an instruction line, a note template or a bounded setting.
	KindRemedy = "remedy"
)

// Kinds lists the definition kinds.
var Kinds = []string{KindFormat, KindSchema, KindInstructions, KindSummary, KindTool, KindDetector, KindRemedy}

// ValidKind reports whether kind is a definition kind.
func ValidKind(kind string) bool { return contains(Kinds, kind) }

// The states of a definition.
const (
	// StateHeld is a definition applied to the next pass only, not yet written.
	StateHeld = "held"
	// StateWritten is a definition in its layer file.
	StateWritten = "written"
	// StateSeed is a shipped seed standing where no definition overrides it.
	StateSeed = "seed"
)

// AllModels is the model a definition for every model is kept under. A model's own definition
// of a name replaces the one kept for all models.
const AllModels = "*"

// Definition is one layer definition.
type Definition struct {
	Kind, Name string
	// Model is the model id (provider/id) it applies to, or AllModels.
	Model string
	// Text is the declarative body: a table in the rows codec, a query, or text.
	Text string
	// Seed is the key (SeedKey) of the seed it overrides, "" when it overrides none.
	Seed string
	// SeedDigest is the Digest of that seed's text when the override was written.
	SeedDigest string
	// Session is the session that wrote it.
	Session string
	Time    time.Time
	State   string
}

// Def is the definition as kernel.Check reads it.
func (d Definition) Def() kernel.Def { return kernel.Def{Kind: d.Kind, Name: d.Name, Text: d.Text} }

// Key is the definition's kind and name, the identity its file is the one home of.
func (d Definition) Key() string { return SeedKey(d.Kind, d.Name) }

// SeedKey is the key a seed of kind named name is shipped under.
func SeedKey(kind, name string) string { return kind + "/" + name }

// Digest is the digest a seed's text or a layer file's content is recorded under: the first 16
// hex characters of its SHA-256.
func Digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}

var shippedMu sync.RWMutex
var shipped = map[string]string{}

// Ship registers the shipped seed of kind named name: the definition that stands until a model
// overrides it, and whose digest an override records. Packages that own seeds call it from
// init, so every store sees the same seeds.
func Ship(kind, name, text string) {
	shippedMu.Lock()
	defer shippedMu.Unlock()
	shipped[SeedKey(kind, name)] = text
}

// Shipped are the registered seeds by key.
func Shipped() map[string]string {
	shippedMu.RLock()
	defer shippedMu.RUnlock()
	out := make(map[string]string, len(shipped))
	for k, v := range shipped {
		out[k] = v
	}
	return out
}

var validatorsMu sync.RWMutex
var validators = map[string][]func(Definition) error{}

// RegisterValidator registers a check every store runs on a definition of kind, when it is made
// and again when it is loaded, after the kernel's: the package that interprets the kind
// (internal/declare for format, schema and summary, internal/rules for detector and remedy)
// calls it from init, as seeds are shipped (Ship), so every store refuses a text its
// interpreter cannot read.
func RegisterValidator(kind string, check func(Definition) error) {
	validatorsMu.Lock()
	defer validatorsMu.Unlock()
	validators[kind] = append(validators[kind], check)
}

func registeredValidators(kind string) []func(Definition) error {
	validatorsMu.RLock()
	defer validatorsMu.RUnlock()
	return append([]func(Definition) error(nil), validators[kind]...)
}

// Locker runs fn holding a lock shared by every process that writes the store
// (curated.Store.Locked).
type Locker interface {
	Locked(fn func() error) error
}

// Store is the layer under one directory.
type Store struct {
	// Dir is the harness directory: <curated store>/harness.
	Dir string
	// Lock serialises writers across processes; nil runs unlocked.
	Lock Locker
	// Floor is the kernel floor Check runs under; nil is engaged.
	Floor *kernel.Floor
	// Seeds are the seeds by key; nil uses the shipped ones (Ship).
	Seeds map[string]string
	// Validate checks a definition's text for its kind, when made and when loaded; nil checks
	// nothing beyond the kernel and the store's own rules.
	Validate func(Definition) error
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// New is the store under dir, locked with lock.
func New(dir string, lock Locker) Store { return Store{Dir: dir, Lock: lock} }

// ErrNotFound is returned for a name the layer does not hold.
var ErrNotFound = errors.New("no layer definition by that name")

// ErrStale is wrapped by the error for an override whose seed changed since it was written.
var ErrStale = errors.New("stale override")

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Store) seeds() map[string]string {
	if s.Seeds != nil {
		return s.Seeds
	}
	return Shipped()
}

func (s Store) locked(fn func() error) error {
	if s.Lock == nil {
		return fn()
	}
	return s.Lock.Locked(fn)
}

// ModelDir is a model id as a directory name: lowercase letters, digits, '.' and '-' kept,
// every other byte written as '_' and two hex digits, so distinct ids never share a directory
// (AllModels is "_2a").
func ModelDir(model string) string {
	var b strings.Builder
	for i := 0; i < len(model); i++ {
		c := model[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "_%02x", c)
	}
	return b.String()
}

// modelOfDir inverts ModelDir; false for a name ModelDir never makes.
func modelOfDir(dir string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(dir); i++ {
		c := dir[i]
		if c != '_' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(dir) {
			return "", false
		}
		raw, err := hex.DecodeString(dir[i+1 : i+3])
		if err != nil {
			return "", false
		}
		b.WriteByte(raw[0])
		i += 2
	}
	model := b.String()
	return model, model != "" && ModelDir(model) == dir
}

func (s Store) modelPath(model string) string { return filepath.Join(s.Dir, ModelDir(model)) }

// Path is the one home of model's definition of kind named name.
func (s Store) Path(model, kind, name string) string {
	return filepath.Join(s.modelPath(model), kind, name+".rows")
}

func (s Store) heldPath(model, kind, name string) string {
	return filepath.Join(s.modelPath(model), "held", kind, name+".rows")
}

func (s Store) blobPath(model, digest string) string {
	return filepath.Join(s.modelPath(model), "blobs", digest+".rows")
}

// --- the file format -------------------------------------------------------------

// headerTable is the header every layer file opens with, one row in the rows codec; a blank
// line and the definition's text follow it verbatim.
const headerTable = "layer"

var headerFields = []string{"kind", "name", "model", "time", "session", "overrides", "fallback"}

// noFallback is a held file's fallback when nothing was written for its name before the first
// hold: a revert leaves the seed (or nothing) standing.
const noFallback = "none"

// encodeFile is d's file: the header, a blank line, the text. fallback is a held file's
// fallback, "" for a written file.
func encodeFile(d Definition, fallback string) string {
	overrides := ""
	if d.Seed != "" {
		overrides = d.Seed + "@" + d.SeedDigest
	}
	row := []string{d.Kind, d.Name, d.Model, d.Time.UTC().Format(time.RFC3339Nano), d.Session, overrides, fallback}
	absent := make([]bool, len(row))
	for i, v := range row {
		absent[i] = v == ""
	}
	header := tables.Rows.Encode(tables.Table{Name: headerTable, Fields: headerFields, Rows: [][]string{row}, Absent: [][]bool{absent}})
	return header + "\n\n" + strings.TrimRight(d.Text, "\n") + "\n"
}

// decodeFile reads a layer file: its definition and, for a held file, its fallback.
func decodeFile(content string) (Definition, string, error) {
	parts := strings.SplitN(content, "\n", 3)
	if len(parts) < 3 || !strings.HasPrefix(parts[2], "\n") {
		return Definition{}, "", errors.New("not a layer file: a header table of one row, a blank line and the text")
	}
	decoded, err := tables.Rows.Decode(parts[0] + "\n" + parts[1])
	if err != nil {
		return Definition{}, "", fmt.Errorf("header: %w", err)
	}
	if len(decoded) != 1 || decoded[0].Name != headerTable || len(decoded[0].Rows) != 1 {
		return Definition{}, "", fmt.Errorf("the header is not one %s row", headerTable)
	}
	t := decoded[0]
	cell := func(field string) string { v, _ := t.Cell(0, field); return v }
	d := Definition{Kind: cell("kind"), Name: cell("name"), Model: cell("model"), Session: cell("session"), Text: strings.TrimRight(strings.TrimPrefix(parts[2], "\n"), "\n")}
	d.Time, _ = time.Parse(time.RFC3339Nano, cell("time"))
	if overrides := cell("overrides"); overrides != "" {
		seed, digest, ok := strings.Cut(overrides, "@")
		if !ok || seed == "" || digest == "" {
			return Definition{}, "", fmt.Errorf("overrides %q is not <seed>@<digest>", overrides)
		}
		d.Seed, d.SeedDigest = seed, digest
	}
	return d, cell("fallback"), nil
}

// writeAtomic writes content to path through a temp file claimed beside it and one rename, so
// a crash mid-write leaves the old file or none, never a torn one.
func writeAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	renamed = true
	return nil
}

// --- checking ----------------------------------------------------------------------

// Check is what a definition must pass to be made: a known kind, a model, the kernel (through
// the store's floor), a valid name, a text, the store's Validate, and the validators the
// kind's interpreter registered (RegisterValidator). It is run again on every file at load.
func (s Store) Check(d Definition) error {
	if !ValidKind(d.Kind) {
		return fmt.Errorf("unknown definition kind %q; kinds are %s", d.Kind, strings.Join(Kinds, ", "))
	}
	if strings.TrimSpace(d.Model) == "" {
		return errors.New("a definition needs the model it applies to, or " + AllModels)
	}
	if err := s.Floor.Check(d.Def()); err != nil {
		return err
	}
	if !validName.MatchString(d.Name) {
		return fmt.Errorf("definition name %q must be lowercase letters, digits and hyphens, starting with a letter or digit, at most 64 characters", d.Name)
	}
	if strings.TrimSpace(d.Text) == "" {
		return fmt.Errorf("the %s definition %q has no text", d.Kind, d.Name)
	}
	if s.Validate != nil {
		if err := s.Validate(d); err != nil {
			return err
		}
	}
	for _, check := range registeredValidators(d.Kind) {
		if err := check(d); err != nil {
			return fmt.Errorf("the %s definition %q is refused: %w", d.Kind, d.Name, err)
		}
	}
	return nil
}

// stamp prepares d to be written: checked, timed, and marked with the seed it overrides.
func (s Store) stamp(d Definition) (Definition, error) {
	d.Text = strings.TrimRight(d.Text, "\n")
	if err := s.Check(d); err != nil {
		return Definition{}, err
	}
	d.Time = s.now().UTC()
	d.Seed, d.SeedDigest = "", ""
	if text, ok := s.seeds()[d.Key()]; ok {
		d.Seed, d.SeedDigest = d.Key(), Digest(text)
	}
	return d, nil
}

// stale reports why d is a stale override, "" when it is not: the seed it overrides changed or
// is gone since it was written, or a seed by its name ships since it was written as the
// model's own.
func (s Store) stale(d Definition) string {
	text, shippedNow := s.seeds()[d.Key()]
	switch {
	case d.Seed == "" && shippedNow:
		return fmt.Sprintf("a seed %s ships since it was written; the seed stands until it is defined again", d.Key())
	case d.Seed == "":
		return ""
	case d.Seed != d.Key():
		return fmt.Sprintf("it overrides %s, not its own name %s", d.Seed, d.Key())
	case !shippedNow:
		return fmt.Sprintf("the seed it overrides (%s@%s) no longer ships", d.Seed, d.SeedDigest)
	case Digest(text) != d.SeedDigest:
		return fmt.Sprintf("the seed it overrides changed (%s@%s, now @%s); the new seed stands until it is defined again", d.Seed, d.SeedDigest, Digest(text))
	}
	return ""
}

// Report is a layer file Load skipped: refused by the kernel or the store's checks, unreadable,
// or a stale override.
type Report struct {
	Model, Kind, Name, Path string
	// Stale marks a stale override; the seed stands.
	Stale bool
	// Untracked marks a file no generation or hold recorded: written or edited outside the
	// store (the agent's own file tools can reach it), so it never passed holding, the
	// clean-pass proof or probation. It is never loaded.
	Untracked bool
	Reason    string
}

// Message is the report as the user is told it.
func (r Report) Message() string {
	what := "skipped"
	if r.Stale {
		what = "skipped as a stale override"
	}
	return fmt.Sprintf("Observational memory: layer definition %s/%s for %s %s: %s", r.Kind, r.Name, r.Model, what, r.Reason)
}

// load reads the written or held file at path, where model's definition of kind named name
// lives, and holds it to everything a new definition must pass. recorded maps each kind/name to
// the digest of the content the store recorded for it (the manifest's latest state for a
// written file, the held index for a held one): a file whose content is not the one recorded
// was written or edited outside the store and is refused as untracked, so it never skips
// holding, the clean-pass proof and probation. It returns the definition, a held file's
// fallback, and the report for one that is refused.
func (s Store) load(path, model, kind, name string, recorded map[string]string) (Definition, string, *Report) {
	report := func(stale bool, format string, args ...any) *Report {
		return &Report{Model: model, Kind: kind, Name: name, Path: path, Stale: stale, Reason: fmt.Sprintf(format, args...)}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, "", report(false, "%v", err)
	}
	d, fallback, err := decodeFile(string(data))
	if err != nil {
		return Definition{}, "", report(false, "%v", err)
	}
	// One home per name: a header that names another kind, name or model than its path is a
	// file moved or crafted to shadow another definition.
	if d.Kind != kind || d.Name != name || d.Model != model {
		return Definition{}, "", report(false, "its header names %s/%s for %s, not the definition its path holds", d.Kind, d.Name, d.Model)
	}
	if err := s.Check(d); err != nil {
		return Definition{}, "", report(false, "%v", err)
	}
	// Checked after the kernel, so a crafted file that touches it is refused naming its owner.
	if want := recorded[SeedKey(kind, name)]; want == "" || want != Digest(string(data)) {
		r := report(false, "untracked: the store recorded no such content for it (a file written or edited outside the memory tool); forget it, or define it again so a clean pass and probation judge it")
		r.Untracked = true
		return Definition{}, "", r
	}
	if reason := s.stale(d); reason != "" {
		return Definition{}, "", report(true, "%s", reason)
	}
	d.State = StateWritten
	return d, fallback, nil
}

// --- reading -------------------------------------------------------------------------

// Get reads model's written definition of kind named name. ErrNotFound when there is none; a
// refused file's error names why, and a stale override's wraps ErrStale.
func (s Store) Get(model, kind, name string) (Definition, error) {
	path := s.Path(model, kind, name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return Definition{}, ErrNotFound
	}
	recorded, err := s.current(model)
	if err != nil {
		return Definition{}, err
	}
	d, _, report := s.load(path, model, kind, name, recorded)
	if report != nil {
		if report.Stale {
			return Definition{}, fmt.Errorf("%w: %s", ErrStale, report.Reason)
		}
		return Definition{}, errors.New(report.Reason)
	}
	return d, nil
}

// List reads model's written definitions of kind ("" for every kind), in kind and name order,
// with a report for every file it skipped. A missing directory is empty.
func (s Store) List(model, kind string) ([]Definition, []Report, error) {
	kinds := Kinds
	if kind != "" {
		kinds = []string{kind}
	}
	recorded, err := s.current(model)
	if err != nil {
		return nil, nil, err
	}
	out, reports := []Definition{}, []Report{}
	for _, k := range kinds {
		names, err := rowsNames(filepath.Join(s.modelPath(model), k))
		if err != nil {
			return nil, nil, err
		}
		for _, name := range names {
			d, _, report := s.load(s.Path(model, k, name), model, k, name, recorded)
			if report != nil {
				reports = append(reports, *report)
				continue
			}
			out = append(out, d)
		}
	}
	return out, reports, nil
}

// current is model's written content as its manifest records it: each kind/name's digest after
// the latest generation.
func (s Store) current(model string) (map[string]string, error) {
	gens, err := s.Generations(model)
	if err != nil {
		return nil, err
	}
	all := states(gens)
	return all[len(all)-1], nil
}

// ListAll reads every model's written definitions of kind, by model and name.
func (s Store) ListAll(kind string) ([]Definition, []Report, error) {
	models, err := s.Models()
	if err != nil {
		return nil, nil, err
	}
	out, reports := []Definition{}, []Report{}
	for _, model := range models {
		defs, rs, err := s.List(model, kind)
		if err != nil {
			return nil, nil, err
		}
		out, reports = append(out, defs...), append(reports, rs...)
	}
	return out, reports, nil
}

// Models are the models the layer holds a directory for, in order.
func (s Store) Models() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if model, ok := modelOfDir(e.Name()); ok {
			out = append(out, model)
		}
	}
	sort.Strings(out)
	return out, nil
}

// rowsNames are the names of the .rows files in dir, sorted; none for a missing dir.
func rowsNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".rows")
		if ok && !e.IsDir() && validName.MatchString(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Harness is a model's effective layer for its next pass: the definitions kept for all models
// (held over written), replaced by the model's own (held over written).
type Harness struct {
	Model string
	defs  map[string]Definition
	seeds map[string]string
	// first is when each kind/name was first put, by the manifests of every model and of the
	// model itself.
	first map[string]time.Time
	// changed is when each kind/name's written content changed (a put, revert, forget or
	// rollback that touched it), in order, by the same manifests.
	changed map[string][]time.Time
}

// Seeded is model's harness with no definitions: every name resolves to its shipped seed. It
// is what stands for a host without a store.
func Seeded(model string) Harness {
	return Harness{Model: model, defs: map[string]Definition{}, seeds: Shipped()}
}

// Get is the effective definition of kind named name, held or written; false when only the
// seed (or nothing) stands.
func (h Harness) Get(kind, name string) (Definition, bool) {
	d, ok := h.defs[SeedKey(kind, name)]
	return d, ok
}

// Resolve is what stands for kind named name: the effective definition, else the seed as a
// definition in StateSeed; false when neither exists.
func (h Harness) Resolve(kind, name string) (Definition, bool) {
	if d, ok := h.Get(kind, name); ok {
		return d, true
	}
	if text, ok := h.seeds[SeedKey(kind, name)]; ok {
		return Definition{Kind: kind, Name: name, Model: AllModels, Text: text, State: StateSeed}, true
	}
	return Definition{}, false
}

// FirstDefined is when kind named name was first written (the earliest put generation in the
// manifests of every model and of the model itself, whatever has happened to it since); false
// for a name no generation wrote, such as one only held.
func (h Harness) FirstDefined(kind, name string) (time.Time, bool) {
	t, ok := h.first[SeedKey(kind, name)]
	return t, ok
}

// Changes are the times kind named name's written content changed, in order: every put,
// revert, forget or rollback generation that touched it, in the manifests of every model and of
// the model itself. A held definition changes nothing written until it is claimed.
func (h Harness) Changes(kind, name string) []time.Time {
	return append([]time.Time(nil), h.changed[SeedKey(kind, name)]...)
}

// SeedNames are the names of kind's seeds, in order.
func (h Harness) SeedNames(kind string) []string {
	out := []string{}
	for key := range h.seeds {
		if k, name, ok := strings.Cut(key, "/"); ok && k == kind {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// List is the effective definitions of kind ("" for every kind), by kind and name.
func (h Harness) List(kind string) []Definition {
	out := []Definition{}
	for _, d := range h.defs {
		if kind == "" || d.Kind == kind {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// Load is model's effective layer (Harness) with a report for every file it skipped.
func (s Store) Load(model string) (Harness, []Report, error) {
	h := Harness{Model: model, defs: map[string]Definition{}, seeds: s.seeds(), first: map[string]time.Time{}, changed: map[string][]time.Time{}}
	reports := []Report{}
	models := []string{AllModels}
	if model != AllModels && model != "" {
		models = append(models, model)
	}
	for _, m := range models {
		gens, err := s.Generations(m)
		if err != nil {
			return Harness{}, nil, err
		}
		all := states(gens)
		for i, g := range gens {
			at, err := time.Parse(time.RFC3339Nano, g.Time)
			if err != nil {
				continue
			}
			before, after := all[i], all[i+1]
			for key := range unionKeys(before, after) {
				if before[key] != after[key] {
					h.changed[key] = append(h.changed[key], at)
				}
			}
			if g.Op != OpPut {
				continue
			}
			key := SeedKey(g.Kind, g.Name)
			if t, ok := h.first[key]; !ok || at.Before(t) {
				h.first[key] = at
			}
		}
	}
	for key := range h.changed {
		sort.Slice(h.changed[key], func(i, j int) bool { return h.changed[key][i].Before(h.changed[key][j]) })
	}
	// Per model in precedence order: written then held for all models, then written then held
	// for the model itself, so the model's own definition replaces one held for all models.
	for _, m := range models {
		defs, rs, err := s.List(m, "")
		if err != nil {
			return Harness{}, nil, err
		}
		reports = append(reports, rs...)
		for _, d := range defs {
			h.defs[d.Key()] = d
		}
		held, rs, err := s.held(m)
		if err != nil {
			return Harness{}, nil, err
		}
		reports = append(reports, rs...)
		for _, hd := range held {
			h.defs[hd.def.Key()] = hd.def
		}
	}
	return h, reports, nil
}

// unionKeys are the keys of a and b.
func unionKeys(a, b map[string]string) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// --- writing -------------------------------------------------------------------------

// Put writes d as model's definition of its kind and name, replacing the one there in place,
// and appends a generation. It is checked first (Check); an override of a seed records the
// seed's digest. Defining the same text again changes nothing.
func (s Store) Put(d Definition) (Definition, error) {
	var out Definition
	err := s.locked(func() error {
		var err error
		out, err = s.PutLocked(d)
		return err
	})
	return out, err
}

// PutLocked is Put for a caller that already holds the store's lock.
func (s Store) PutLocked(d Definition) (Definition, error) {
	d, err := s.stamp(d)
	if err != nil {
		return Definition{}, err
	}
	path := s.Path(d.Model, d.Kind, d.Name)
	recorded, err := s.current(d.Model)
	if err != nil {
		return Definition{}, err
	}
	// Only a file the manifest recorded is the same definition: an untracked one with the same
	// text is written again, with its generation.
	if data, err := os.ReadFile(path); err == nil && recorded[d.Key()] == Digest(string(data)) {
		if old, _, err := decodeFile(string(data)); err == nil && old.Kind == d.Kind && old.Name == d.Name && old.Model == d.Model &&
			old.Text == d.Text && old.Seed == d.Seed && old.SeedDigest == d.SeedDigest {
			old.State = StateWritten
			return old, nil
		}
	}
	content := encodeFile(d, "")
	if err := s.write(d.Model, path, content); err != nil {
		return Definition{}, err
	}
	if _, err := s.appendGeneration(d.Model, Generation{Op: OpPut, Kind: d.Kind, Name: d.Name, Digest: Digest(content), Session: d.Session}); err != nil {
		return Definition{}, err
	}
	d.State = StateWritten
	return d, nil
}

// write keeps content in model's blobs (so a rollback or a revert can restore it) and then at
// path.
func (s Store) write(model, path, content string) error {
	blob := s.blobPath(model, Digest(content))
	if _, err := os.Stat(blob); err != nil {
		if err := writeAtomic(blob, content); err != nil {
			return err
		}
	}
	return writeAtomic(path, content)
}

// Forget removes model's definition of kind named name (also one Load refuses), and any held
// one, and appends a generation. What stands then is the seed, returned with true when there
// is one. ErrNotFound when there was nothing to forget.
func (s Store) Forget(model, kind, name, session string) (Definition, bool, error) {
	var seed Definition
	var seeded bool
	err := s.locked(func() error {
		var err error
		seed, seeded, err = s.ForgetLocked(model, kind, name, session)
		return err
	})
	return seed, seeded, err
}

// ForgetLocked is Forget for a caller that already holds the store's lock.
func (s Store) ForgetLocked(model, kind, name, session string) (Definition, bool, error) {
	if !ValidKind(kind) || !validName.MatchString(name) {
		return Definition{}, false, ErrNotFound
	}
	heldErr := os.Remove(s.heldPath(model, kind, name))
	if heldErr == nil {
		if err := s.recordHeld(model, SeedKey(kind, name), ""); err != nil {
			return Definition{}, false, err
		}
	}
	err := os.Remove(s.Path(model, kind, name))
	switch {
	case errors.Is(err, os.ErrNotExist) && errors.Is(heldErr, os.ErrNotExist):
		return Definition{}, false, ErrNotFound
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return Definition{}, false, err
	case err == nil:
		if _, err := s.appendGeneration(model, Generation{Op: OpForget, Kind: kind, Name: name, Session: session}); err != nil {
			return Definition{}, false, err
		}
	}
	if text, ok := s.seeds()[SeedKey(kind, name)]; ok {
		return Definition{Kind: kind, Name: name, Model: AllModels, Text: text, State: StateSeed}, true, nil
	}
	return Definition{}, false, nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
