package curated

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/legacyquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// Definition is a query the agent named with the memory tool's define write
// (docs/memory-interface.md, section 6). The store only keeps it; turning it
// into a tool of its own is the per-model harness's job. Definitions apply
// everywhere: they read the session's memory, not a project's files.
type Definition struct {
	Name string
	// Query is the read query the definition runs, as written.
	Query string
	// Description is the one sentence saying what the query returns.
	Description string
	// Sources are the sessions that defined it.
	Sources []string
	Created time.Time
}

// MaxDefinitionDescriptionChars bounds a definition's description, in
// characters after whitespace is collapsed. The description becomes part of a
// tool's description in every later session, so it stays one short sentence.
const MaxDefinitionDescriptionChars = 200

// MaxDefinitionQueryChars and MaxDefinitionLiteralChars bound a definition's query, in
// characters, and each quoted text in it (legacyquery.CheckDefinitionBounds).
const (
	MaxDefinitionQueryChars   = legacyquery.MaxDefinitionQueryChars
	MaxDefinitionLiteralChars = legacyquery.MaxDefinitionLiteralChars
)

// checkDefinitionQuery refuses a query over MaxDefinitionQueryChars characters or holding a
// quoted text over MaxDefinitionLiteralChars, through the frozen recognizer's load check
// (legacyquery.CheckDefinitionBounds). It is checked when a definition is stored and again when
// one is read, so a file written by hand or by an earlier version never reaches a tool
// description unbounded.
func checkDefinitionQuery(query string) error {
	return legacyquery.CheckDefinitionBounds(query)
}

// definitionTable is the table a definition's text holds, in the rows codec.
const definitionTable = "definition"

var definitionFields = []string{"name", "query", "description", "created", "sources"}

// A definition lives in the store's layer (docs/memory-interface.md, section 8) as a tool kept
// for every model: <store>/harness/<all models>/tool/<name>.rows. Definitions written before
// the layer, in <store>/definitions/<name>.rows, are moved there on first use
// (migrateDefinitions).

func (s Store) definitionPath(name string) string {
	return s.Layer().Path(layer.AllModels, layer.KindTool, name)
}

func (s Store) legacyDefinitionsDir() string { return filepath.Join(s.Dir, "definitions") }

func (s Store) legacyDefinitionPath(name string) string {
	return filepath.Join(s.legacyDefinitionsDir(), name+".rows")
}

// DefinitionUsageKey is the usage ledger name a definition's events are
// recorded under, apart from any memory's.
func DefinitionUsageKey(name string) string { return "query:" + name }

// Define stores a new definition. A name a memory or another definition holds
// is refused, and so is one the kernel owns (kernel.Owns): a defined tool never shadows the
// memory tool or the query engine's words. The defined tools are retired (docs/code-tool.md):
// the extension never calls Define (om.Define refuses), and it stays only for tests that write
// the store an earlier release left.
func (s Store) Define(d Definition, source string) (Definition, error) {
	if err := ValidateName(d.Name); err != nil {
		return Definition{}, err
	}
	if strings.TrimSpace(d.Query) == "" {
		return Definition{}, errors.New("a defined query needs a query")
	}
	if err := checkDefinitionQuery(strings.TrimSpace(d.Query)); err != nil {
		return Definition{}, err
	}
	if strings.TrimSpace(d.Description) == "" {
		return Definition{}, errors.New("a defined query needs a one-sentence description")
	}
	if n := utf8.RuneCountInString(oneLine(d.Description)); n > MaxDefinitionDescriptionChars {
		return Definition{}, fmt.Errorf("a defined query's description is one sentence of at most %d characters (this one has %d): say what the query returns", MaxDefinitionDescriptionChars, n)
	}
	d.Query = strings.TrimSpace(d.Query)
	d.Description = oneLine(d.Description)
	d.Created = s.Now().UTC()
	d.Sources = addSource(nil, source)
	def := layerDefinition(d, source)
	ls := s.Layer()
	if err := ls.Check(def); err != nil {
		return Definition{}, err
	}
	err := s.locked(func() error {
		if err := s.migrateDefinitionsLocked(); err != nil {
			return err
		}
		if _, err := os.Stat(s.definitionPath(d.Name)); err == nil {
			return fmt.Errorf("a query named %q is already defined; forget it first with forget name=%s", d.Name, d.Name)
		}
		if _, err := os.Stat(s.path(d.Name)); err == nil {
			return fmt.Errorf("the name %q is taken by a saved memory; choose another name", d.Name)
		}
		_, err := ls.PutLocked(def)
		return err
	})
	if err != nil {
		return Definition{}, err
	}
	// The save event starts the definition's probation over; without it the
	// name would inherit an earlier definition's standing, so the file is
	// removed again rather than kept unrecorded.
	if err := s.Record(DefinitionUsageKey(d.Name), EventSave); err != nil {
		_ = s.locked(func() error {
			_, _, err := ls.ForgetLocked(layer.AllModels, layer.KindTool, d.Name, source)
			return err
		})
		return Definition{}, fmt.Errorf("the definition could not be recorded in the usage ledger, so it was not kept: %w", err)
	}
	return d, nil
}

// layerDefinition is d as the layer keeps it: a tool for every model whose text is the
// definition's row.
func layerDefinition(d Definition, session string) layer.Definition {
	table := tables.Table{Name: definitionTable, Fields: definitionFields, Rows: [][]string{{d.Name, d.Query, d.Description, d.Created.Format(time.RFC3339Nano), strings.Join(d.Sources, " ")}}}
	return layer.Definition{Kind: layer.KindTool, Name: d.Name, Model: layer.AllModels, Text: tables.Rows.Encode(table), Session: session}
}

// parseDefinition reads a definition's row from its text, held to the bounds a new definition
// is.
func parseDefinition(name, text string) (Definition, error) {
	decoded, err := tables.Rows.Decode(text)
	if err != nil {
		return Definition{}, fmt.Errorf("defined query %q: %w", name, err)
	}
	if len(decoded) != 1 || decoded[0].Name != definitionTable || len(decoded[0].Rows) != 1 {
		return Definition{}, fmt.Errorf("defined query %q is not one %s row", name, definitionTable)
	}
	t := decoded[0]
	d := Definition{Name: name}
	if stored, ok := t.Cell(0, "name"); ok && stored != name {
		return Definition{}, fmt.Errorf("defined query %q names itself %q", name, stored)
	}
	d.Query, _ = t.Cell(0, "query")
	d.Description, _ = t.Cell(0, "description")
	if created, ok := t.Cell(0, "created"); ok {
		d.Created, _ = time.Parse(time.RFC3339, created)
	}
	if sources, ok := t.Cell(0, "sources"); ok {
		d.Sources = strings.Fields(sources)
	}
	if strings.TrimSpace(d.Query) == "" {
		return Definition{}, fmt.Errorf("defined query %q has no query", name)
	}
	if err := checkDefinitionQuery(d.Query); err != nil {
		return Definition{}, fmt.Errorf("defined query %q: %w", name, err)
	}
	return d, nil
}

// GetDefinition reads one definition. The layer re-checks it against the kernel on every read.
func (s Store) GetDefinition(name string) (Definition, error) {
	if err := ValidateName(name); err != nil {
		return Definition{}, err
	}
	s.migrateDefinitions()
	def, err := s.Layer().Get(layer.AllModels, layer.KindTool, name)
	if errors.Is(err, layer.ErrNotFound) {
		return Definition{}, ErrNotFound
	}
	if err != nil {
		return Definition{}, fmt.Errorf("defined query %q: %w", name, err)
	}
	return parseDefinition(name, def.Text)
}

// ListDefinitions returns every readable definition in name order.
func (s Store) ListDefinitions() ([]Definition, error) {
	s.migrateDefinitions()
	defs, _, err := s.Layer().List(layer.AllModels, layer.KindTool)
	if err != nil {
		return nil, err
	}
	out := []Definition{}
	for _, def := range defs {
		if ValidateName(def.Name) != nil {
			continue
		}
		if d, err := parseDefinition(def.Name, def.Text); err == nil {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ForgetDefinition removes a definition, also one GetDefinition refuses to
// read. Its usage history stays in the ledger.
func (s Store) ForgetDefinition(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	s.migrateDefinitions()
	if _, _, err := s.Layer().Forget(layer.AllModels, layer.KindTool, name, ""); err != nil {
		if errors.Is(err, layer.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return s.Record(DefinitionUsageKey(name), EventForget)
}

// The suffixes a legacy definition file is renamed with once migration has handled it: moved
// into the layer, or refused by it (kept, never applied). Neither is read again.
const (
	migratedSuffix = ".migrated"
	refusedSuffix  = ".refused"
)

// migrateDefinitions moves definitions written before the layer into it, when there are any,
// and retries the ones the layer refused before (it may admit them now: a lowered floor, or a
// kernel that no longer owns the name for a tool). A failure leaves them where they are, to be
// tried again; the layer's own definitions still work.
func (s Store) migrateDefinitions() {
	names, _ := legacyRows(s.legacyDefinitionsDir(), ".rows")
	refused, _ := legacyRows(s.legacyDefinitionsDir(), ".rows"+refusedSuffix)
	if len(names) == 0 && len(refused) == 0 {
		return
	}
	_ = s.locked(s.migrateDefinitionsLocked)
}

// migrateDefinitionsLocked is migrateDefinitions for a caller holding the store's lock. Each
// legacy file becomes a layer file with the same row (a name the layer already holds keeps the
// layer's), and the legacy file is renamed with migratedSuffix, so nothing is lost; one the
// layer refuses is renamed with refusedSuffix and never applied, and RefusedDefinitions says
// why. A refused file is retried each time and migrated once the layer admits it.
func (s Store) migrateDefinitionsLocked() error {
	dir := s.legacyDefinitionsDir()
	names, err := legacyRows(dir, ".rows")
	if err != nil {
		return err
	}
	refused, err := legacyRows(dir, ".rows"+refusedSuffix)
	if err != nil {
		return err
	}
	ls := s.Layer()
	migrate := func(name, legacy string, wasRefused bool) error {
		def, err := legacyDefinition(name, legacy)
		if err != nil {
			return err
		}
		suffix := migratedSuffix
		if _, err := os.Stat(s.definitionPath(name)); errors.Is(err, os.ErrNotExist) {
			if _, err := ls.PutLocked(def); err != nil {
				suffix = refusedSuffix
			}
		}
		if wasRefused && suffix == refusedSuffix {
			return nil // still refused: it stays where it is
		}
		return os.Rename(legacy, s.legacyDefinitionPath(name)+suffix)
	}
	for _, name := range names {
		if err := migrate(name, s.legacyDefinitionPath(name), false); err != nil {
			return err
		}
	}
	for _, name := range refused {
		if err := migrate(name, s.legacyDefinitionPath(name)+refusedSuffix, true); err != nil {
			return err
		}
	}
	return nil
}

// legacyDefinition is the layer definition a legacy file at path becomes: a tool kept for
// every model, its text the file's row, its session the row's first source.
func legacyDefinition(name, path string) (layer.Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return layer.Definition{}, err
	}
	def := layer.Definition{Kind: layer.KindTool, Name: name, Model: layer.AllModels, Text: strings.TrimRight(string(data), "\n")}
	if decoded, err := tables.Rows.Decode(def.Text); err == nil && len(decoded) == 1 {
		if sources, ok := decoded[0].Cell(0, "sources"); ok {
			if fields := strings.Fields(sources); len(fields) > 0 {
				def.Session = fields[0]
			}
		}
	}
	return def, nil
}

// RefusedDefinition is a defined tool written before the layer that the layer refused to
// migrate: its name and the layer's refusal. Like every defined tool, it is retired: listed,
// never registered or run (harness.RetiredQueries).
type RefusedDefinition struct {
	Name, Reason string
}

// Message is the refusal as the user is told it.
func (r RefusedDefinition) Message() string {
	return fmt.Sprintf("Observational memory: defined tool %s was not migrated: %s", r.Name, r.Reason)
}

// RefusedDefinitions are the legacy defined tools the layer still refuses, each with the
// refusal, for om.Retired and /om:status (harness.RetiredQueries). The files stay, renamed, and
// are retried on every use.
func (s Store) RefusedDefinitions() ([]RefusedDefinition, error) {
	s.migrateDefinitions()
	names, err := legacyRows(s.legacyDefinitionsDir(), ".rows"+refusedSuffix)
	if err != nil {
		return nil, err
	}
	out := []RefusedDefinition{}
	for _, name := range names {
		def, err := legacyDefinition(name, s.legacyDefinitionPath(name)+refusedSuffix)
		if err != nil {
			out = append(out, RefusedDefinition{Name: name, Reason: err.Error()})
			continue
		}
		reason := "the layer refused it"
		if err := s.Layer().Check(def); err != nil {
			reason = err.Error()
		}
		out = append(out, RefusedDefinition{Name: name, Reason: reason})
	}
	return out, nil
}

// legacyRows are the valid names of the files in dir ending in suffix; none for a missing dir.
func legacyRows(dir, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := []string{}
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), suffix)
		if ok && !entry.IsDir() && ValidateName(name) == nil {
			out = append(out, name)
		}
	}
	return out, nil
}
