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

// MaxDefinitionQueryChars and MaxDefinitionLiteralChars bound a definition's
// query, in characters, and each quoted text in it. The query is shown in the
// tool's description in every later session too, so its text is bounded like
// the description's: a whole query of one short line, whose quoted texts are a
// few search words or a field value, not sentences.
const (
	MaxDefinitionQueryChars   = 200
	MaxDefinitionLiteralChars = 40
)

// checkDefinitionQuery refuses a query over MaxDefinitionQueryChars characters
// or holding a quoted text over MaxDefinitionLiteralChars. It is checked when
// a definition is stored and again when one is read, so a file written by
// hand or by an earlier version never reaches a tool description unbounded.
func checkDefinitionQuery(query string) error {
	if n := utf8.RuneCountInString(query); n > MaxDefinitionQueryChars {
		return fmt.Errorf("a defined query is at most %d characters (this one has %d): keep it to a source and a few stages", MaxDefinitionQueryChars, n)
	}
	for _, literal := range quotedTexts(query) {
		if n := utf8.RuneCountInString(literal); n > MaxDefinitionLiteralChars {
			return fmt.Errorf("a quoted text in a defined query is at most %d characters (%q has %d): search for a few words, or use $input for the rest", MaxDefinitionLiteralChars, literal, n)
		}
	}
	return nil
}

// quotedTexts are the "..." texts of a query, escapes kept as written; an
// unterminated one runs to the end.
func quotedTexts(query string) []string {
	out := []string{}
	start := -1
	for i := 0; i < len(query); i++ {
		switch {
		case start >= 0 && query[i] == '\\':
			i++
		case query[i] == '"' && start < 0:
			start = i + 1
		case query[i] == '"':
			out = append(out, query[start:i])
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, query[start:])
	}
	return out
}

// definitionTable is the table a definition file holds, in the rows codec.
const definitionTable = "definition"

var definitionFields = []string{"name", "query", "description", "created", "sources"}

func (s Store) definitionsDir() string { return filepath.Join(s.Dir, "definitions") }

func (s Store) definitionPath(name string) string {
	return filepath.Join(s.definitionsDir(), name+".rows")
}

// DefinitionUsageKey is the usage ledger name a definition's events are
// recorded under, apart from any memory's.
func DefinitionUsageKey(name string) string { return "query:" + name }

// Define stores a new definition. A name a memory or another definition holds
// is refused.
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
	var saved Definition
	err := s.locked(func() error {
		if _, err := os.Stat(s.definitionPath(d.Name)); err == nil {
			return fmt.Errorf("a query named %q is already defined; forget it first with forget name=%s", d.Name, d.Name)
		}
		if _, err := os.Stat(s.path(d.Name)); err == nil {
			return fmt.Errorf("the name %q is taken by a saved memory; choose another name", d.Name)
		}
		d.Query = strings.TrimSpace(d.Query)
		d.Description = oneLine(d.Description)
		d.Created = s.Now().UTC()
		d.Sources = addSource(nil, source)
		saved = d
		if err := os.MkdirAll(s.definitionsDir(), 0o700); err != nil {
			return err
		}
		table := tables.Table{Name: definitionTable, Fields: definitionFields, Rows: [][]string{{d.Name, d.Query, d.Description, d.Created.Format(time.RFC3339Nano), strings.Join(d.Sources, " ")}}}
		tmp := s.definitionPath(d.Name) + ".tmp"
		if err := os.WriteFile(tmp, []byte(tables.Rows.Encode(table)+"\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, s.definitionPath(d.Name))
	})
	if err != nil {
		return Definition{}, err
	}
	// The save event starts the definition's probation over; without it the
	// name would inherit an earlier definition's standing, so the file is
	// removed again rather than kept unrecorded.
	if err := s.Record(DefinitionUsageKey(d.Name), EventSave); err != nil {
		_ = s.locked(func() error { return os.Remove(s.definitionPath(d.Name)) })
		return Definition{}, fmt.Errorf("the definition could not be recorded in the usage ledger, so it was not kept: %w", err)
	}
	return saved, nil
}

// GetDefinition reads one definition.
func (s Store) GetDefinition(name string) (Definition, error) {
	if err := ValidateName(name); err != nil {
		return Definition{}, err
	}
	data, err := os.ReadFile(s.definitionPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return Definition{}, ErrNotFound
	}
	if err != nil {
		return Definition{}, err
	}
	decoded, err := tables.Rows.Decode(string(data))
	if err != nil {
		return Definition{}, fmt.Errorf("defined query %q: %w", name, err)
	}
	if len(decoded) != 1 || decoded[0].Name != definitionTable || len(decoded[0].Rows) != 1 {
		return Definition{}, fmt.Errorf("defined query %q is not one %s row", name, definitionTable)
	}
	t := decoded[0]
	d := Definition{Name: name}
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

// ListDefinitions returns every readable definition in name order.
func (s Store) ListDefinitions() ([]Definition, error) {
	entries, err := os.ReadDir(s.definitionsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []Definition{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Definition{}
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".rows")
		if !ok || entry.IsDir() || ValidateName(name) != nil {
			continue
		}
		if d, err := s.GetDefinition(name); err == nil {
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
	err := s.locked(func() error {
		err := os.Remove(s.definitionPath(name))
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return err
	}
	return s.Record(DefinitionUsageKey(name), EventForget)
}

// DescribeDefinition renders a definition in full for recall <name>.
func DescribeDefinition(d Definition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [query]: %s\n\n%s\n\ndefined %s", d.Name, d.Description, d.Query, d.Created.Format("2006-01-02"))
	if len(d.Sources) > 0 {
		fmt.Fprintf(&b, ", sessions: %s", strings.Join(d.Sources, ", "))
	}
	return b.String()
}
