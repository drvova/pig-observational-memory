package rules

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The detector and remedy kinds of the layer as data (docs/memory-interface.md, section 8).
//
// A detector definition declares what it counts: one of the kernel's checks (the six detectors
// of section 7, whose counting is kernel code), or a memory query over the pass's own notes and
// the history it covered, whose selected rows (or count) are the events. The six seeds name
// their own checks. The kernel's checks are counted under their names on every observer pass
// whatever the layer holds, since rules are judged by them alone (grading stays with the
// kernel); every detector a model defines is counted beside them under DefinedCountKey, so it
// flags problems without ever judging a remedy.
//
// A remedy definition named after a catalogue remedy declares that remedy's catalogue entries:
// the detectors it answers (within the kernel's bounds, Answers), the order the harness tries it
// in for each (rank, 0 for never), and the parameters a harness rule carries (kind, fullness,
// the prompt line). The eight seeds are the catalogue section 7 ships (Catalogue). A model's
// override changes the catalogue for that model only, and every entry is held to Check, so no
// entry can break an invariant.

// DetectorTable is the table a detector declaration holds, one row.
const DetectorTable = "detector"

// DetectorColumns are a detector declaration's columns.
var DetectorColumns = []string{"check", "query", "description"}

// Detector bounds: a declaration is one short query and one sentence.
const (
	MaxDetectorQuery       = 200
	MaxDetectorDescription = 200
)

// detectorSources are what a detector query reads: the pass's notes, and the history (user,
// assistant and tool text) of the entries the pass covered.
var detectorSources = []string{memquery.SourceNotes, memquery.SourceHistory}

// DefinedCountKey is the usage-ledger count key of a detector a model defined: beside the
// kernel's checks, never one of them, so no rule is judged by it.
func DefinedCountKey(name string) string { return "defined:" + name }

// DetectorDef is one parsed detector declaration.
type DetectorDef struct {
	Name string
	// Check is a kernel check (one of Detectors); "" for a query.
	Check string
	// Query is a memory query whose selection is the count; "" for a check.
	Query       string
	Description string
}

func detectorSeed(name, description string) string {
	return tables.Rows.Encode(tables.Table{Name: DetectorTable, Fields: DetectorColumns, Rows: [][]string{{name, "", description}}, Absent: [][]bool{{false, true, false}}})
}

// ParseDetector reads the detector declaration text named name: exactly one of a kernel check
// and a query reading notes or history, of at most MaxDetectorQuery characters.
func ParseDetector(name, text string) (DetectorDef, error) {
	decoded, err := tables.Rows.Decode(strings.TrimSpace(text))
	if err != nil {
		return DetectorDef{}, fmt.Errorf("a detector declaration is one %s row in the rows codec: %w", DetectorTable, err)
	}
	if len(decoded) != 1 || decoded[0].Name != DetectorTable || len(decoded[0].Rows) != 1 {
		return DetectorDef{}, fmt.Errorf("a detector declaration is one %s row with the columns %s", DetectorTable, strings.Join(DetectorColumns, ","))
	}
	t := decoded[0]
	for _, c := range t.Fields {
		if !containsString(DetectorColumns, c) {
			return DetectorDef{}, fmt.Errorf("unknown detector column %q; the columns are %s", c, strings.Join(DetectorColumns, ","))
		}
	}
	cell := func(c string) string { v, _ := t.Cell(0, c); return strings.TrimSpace(v) }
	d := DetectorDef{Name: name, Check: cell("check"), Query: cell("query"), Description: strings.Join(strings.Fields(cell("description")), " ")}
	if n := utf8.RuneCountInString(d.Description); n > MaxDetectorDescription {
		return DetectorDef{}, fmt.Errorf("a detector's description is at most %d characters (this one %d)", MaxDetectorDescription, n)
	}
	switch {
	case (d.Check == "") == (d.Query == ""):
		return DetectorDef{}, fmt.Errorf("a detector declares exactly one of check (a kernel check: %s) and query", strings.Join(Detectors, ", "))
	case d.Check != "":
		if !containsString(Detectors, d.Check) {
			return DetectorDef{}, fmt.Errorf("check %q is not a kernel check; the checks are %s", d.Check, strings.Join(Detectors, ", "))
		}
	default:
		if n := utf8.RuneCountInString(d.Query); n > MaxDetectorQuery {
			return DetectorDef{}, fmt.Errorf("a detector query is at most %d characters (this one %d)", MaxDetectorQuery, n)
		}
		q, err := memquery.Parse(d.Query)
		if err != nil {
			return DetectorDef{}, fmt.Errorf("detector query %q: %w", d.Query, err)
		}
		if q.Write != "" || !containsString(detectorSources, q.Source) {
			return DetectorDef{}, fmt.Errorf("a detector query reads %s", strings.Join(detectorSources, " or "))
		}
	}
	return d, nil
}

// Count is what d counts in one observer pass: its kernel check's count (kernel, from
// DetectObserverPass), or its query's selection over the pass's notes and covered history.
// The notes' subjects are canonical over the memory before the pass and the pass's notes,
// under the pass's aliases (ObserverPass.Known, Aliases), as the memory tool reads them. A
// query that fails counts nothing.
func (d DetectorDef) Count(p ObserverPass, kernel map[string]int) int {
	if d.Check != "" {
		return kernel[d.Check]
	}
	env := memquery.Env{Entries: p.Covered, Records: &memquery.Records{Observations: p.Notes, Reflections: []ledger.Reflection{}, Aliases: p.Aliases, SubjectContext: ledger.SubjectsOf(p.Known)}}
	sel, err := memquery.Select(env, d.Query, detectorSources...)
	if err != nil {
		return 0
	}
	return sel.Count
}

// ModelDetectors are the detectors a model defined in its effective layer (held or written),
// by name: those its passes count beside the kernel's checks. A declaration that no longer
// parses is skipped.
func ModelDetectors(h layer.Harness) []DetectorDef {
	out := []DetectorDef{}
	for _, def := range h.List(layer.KindDetector) {
		if d, err := ParseDetector(def.Name, def.Text); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// --- the catalogue as remedy definitions --------------------------------------------------

// CatalogueTable is the table a catalogue remedy definition holds, one row per detector the
// remedy answers.
const CatalogueTable = "catalogue"

// CatalogueColumns are a catalogue definition's columns.
var CatalogueColumns = []string{"detector", "rank", "kind", "fullness", "text"}

// CatalogueEntry is one remedy's entry for one detector.
type CatalogueEntry struct {
	Remedy, Detector string
	// Rank orders the remedies the harness tries for the detector, from 1; 0 is never tried by
	// the harness (a model may still propose it).
	Rank int
	// Kind is require-kind's kind, or protect-kind's ("" protects the kind the evidence
	// flagged most).
	Kind string
	// Fullness is protect-kind's fullness (0 is agents.DropFloorMaxFullness).
	Fullness float64
	// Text is prompt-line's line.
	Text string
	// Authored is true when Text is the model's own: it came from a definition and is not the
	// seed's line for the detector. A harness rule carrying it is recorded as the model's
	// (ProposerModel), never as the harness's.
	Authored bool
}

// catalogueSeed is remedy's seed: its entries in Catalogue, with the parameters the harness
// rules carried before the layer (harnessRule).
func catalogueSeed(remedy string) string {
	t := tables.Table{Name: CatalogueTable, Fields: CatalogueColumns, Rows: [][]string{}}
	for _, detector := range Detectors {
		if !Answers(remedy, detector) {
			continue
		}
		rank := 0
		for i, r := range Catalogue[detector] {
			if r == remedy {
				rank = i + 1
			}
		}
		row := []string{detector, strconv.Itoa(rank), "", "", ""}
		switch remedy {
		case RemedyPromptLine:
			row[4] = catalogueText[detector]
		case RemedyRequireKind:
			row[2] = string(kindAssistant)
		}
		if rank == 0 && remedy != RemedyPromptLine {
			continue
		}
		absent := make([]bool, len(row))
		for i, v := range row {
			absent[i] = i > 0 && v == ""
		}
		t.Rows, t.Absent = append(t.Rows, row), append(t.Absent, absent)
	}
	return tables.Rows.Encode(t)
}

// CatalogueSeeds are the shipped catalogue definitions by remedy.
var CatalogueSeeds = func() map[string]string {
	out := map[string]string{}
	for _, remedy := range Remedies {
		out[remedy] = catalogueSeed(remedy)
	}
	return out
}()

// ParseCatalogue reads remedy's catalogue definition text. Every entry must answer a detector
// the remedy can answer (Answers: the kernel's bound on what each remedy acts on), name each
// detector once, and carry parameters Check accepts; prompt-line entries need their line.
func ParseCatalogue(remedy, text string) ([]CatalogueEntry, error) {
	if !containsString(Remedies, remedy) {
		return nil, fmt.Errorf("a catalogue definition is named after a remedy of the catalogue (%s), not %q", strings.Join(Remedies, ", "), remedy)
	}
	decoded, err := tables.Rows.Decode(strings.TrimSpace(text))
	if err != nil {
		return nil, fmt.Errorf("a catalogue definition is one %s table in the rows codec: %w", CatalogueTable, err)
	}
	if len(decoded) != 1 || decoded[0].Name != CatalogueTable {
		return nil, fmt.Errorf("a catalogue definition is one %s table with the columns %s", CatalogueTable, strings.Join(CatalogueColumns, ","))
	}
	t := decoded[0]
	for _, c := range t.Fields {
		if !containsString(CatalogueColumns, c) {
			return nil, fmt.Errorf("unknown catalogue column %q; the columns are %s", c, strings.Join(CatalogueColumns, ","))
		}
	}
	out := []CatalogueEntry{}
	seen := map[string]bool{}
	for r := range t.Rows {
		cell := func(c string) string { v, _ := t.Cell(r, c); return strings.TrimSpace(v) }
		e := CatalogueEntry{Remedy: remedy, Detector: cell("detector"), Kind: cell("kind"), Text: cell("text")}
		if seen[e.Detector] {
			return nil, fmt.Errorf("catalogue %s names %s twice", remedy, e.Detector)
		}
		seen[e.Detector] = true
		if v := cell("rank"); v != "" {
			if e.Rank, err = strconv.Atoi(v); err != nil || e.Rank < 0 {
				return nil, fmt.Errorf("catalogue %s: rank %q must be a whole number, 0 for never", remedy, v)
			}
		}
		if e.Fullness, err = ParseFullness(cell("fullness")); err != nil {
			return nil, fmt.Errorf("catalogue %s: %w", remedy, err)
		}
		probe := Rule{Detector: e.Detector, Remedy: remedy, Kind: e.Kind, Fullness: e.Fullness, Text: e.Text}
		if remedy == RemedyProtectKind && probe.Kind == "" {
			probe.Kind = Unclassified
		}
		if _, err := Check(probe); err != nil {
			return nil, fmt.Errorf("catalogue %s for %s: %w", remedy, e.Detector, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// Catalog is a model's remedy catalogue: for each detector the entries of the remedies that
// answer it.
type Catalog struct {
	entries map[string][]CatalogueEntry
}

// SeedCatalog is the shipped catalogue.
func SeedCatalog() Catalog { return CatalogOf(layer.Seeded("")) }

// CatalogOf is the catalogue a model's effective layer declares: each remedy's own definition,
// else its seed. A definition that no longer parses leaves the seed standing.
func CatalogOf(h layer.Harness) Catalog {
	c := Catalog{entries: map[string][]CatalogueEntry{}}
	for _, remedy := range Remedies {
		var entries []CatalogueEntry
		var err error
		seeds, _ := ParseCatalogue(remedy, CatalogueSeeds[remedy])
		if d, ok := h.Get(layer.KindRemedy, remedy); ok {
			entries, err = ParseCatalogue(remedy, d.Text)
			for i := range entries {
				entries[i].Authored = entries[i].Text != "" && entries[i].Text != seedText(seeds, entries[i].Detector)
			}
		}
		if entries == nil || err != nil {
			entries = seeds
		}
		for _, e := range entries {
			c.entries[e.Detector] = append(c.entries[e.Detector], e)
		}
	}
	for d := range c.entries {
		sort.SliceStable(c.entries[d], func(i, j int) bool {
			a, b := c.entries[d][i], c.entries[d][j]
			if a.Rank != b.Rank {
				return a.Rank < b.Rank
			}
			return indexOf(Remedies, a.Remedy) < indexOf(Remedies, b.Remedy)
		})
	}
	return c
}

// Tries are the remedies the harness tries for detector, in order: the entries ranked 1 and up.
func (c Catalog) Tries(detector string) []string {
	out := []string{}
	for _, e := range c.entries[detector] {
		if e.Rank > 0 {
			out = append(out, e.Remedy)
		}
	}
	return out
}

// Entry is remedy's entry for detector.
func (c Catalog) Entry(detector, remedy string) (CatalogueEntry, bool) {
	for _, e := range c.entries[detector] {
		if e.Remedy == remedy {
			return e, true
		}
	}
	return CatalogueEntry{}, false
}

// Rule is the catalogue rule the harness writes for detector with remedy: the entry's
// parameters, and for a protect-kind without a kind the kind the evidence flagged most. A rule
// carrying a line the model wrote (CatalogueEntry.Authored) is recorded as proposed by the
// model.
func (c Catalog) Rule(detector, remedy string, kinds map[string]int) Rule {
	r := Rule{Detector: detector, Remedy: remedy, Proposer: ProposerHarness}
	e, _ := c.Entry(detector, remedy)
	switch remedy {
	case RemedyPromptLine:
		r.Text = e.Text
		if e.Authored {
			r.Proposer = ProposerModel
		}
	case RemedyRequireKind:
		r.Kind = e.Kind
		if r.Kind == "" {
			r.Kind = string(kindAssistant)
		}
	case RemedyProtectKind:
		r.Kind, r.Fullness = e.Kind, e.Fullness
		if r.Kind == "" {
			r.Kind = mostFrequent(kinds)
		}
		if r.Fullness == 0 {
			r.Fullness = agents.DropFloorMaxFullness
		}
	}
	return r
}

// seedText is the seed catalogue's line for detector among seeds, "" when it has none.
func seedText(seeds []CatalogueEntry, detector string) string {
	for _, e := range seeds {
		if e.Detector == detector {
			return e.Text
		}
	}
	return ""
}

// CheckFromModel is the check every path that accepts a definition from the model (the memory
// tool's harness and propose writes, and the build and revision passes) runs beside
// layer.Store.Check, which reads no branch: text from files, web pages or tool output never
// becomes a definition. A definition of any kind is refused when its text, or any run of its
// words long enough to be a quotation (MinCopiedWords), appears in a tool result, a custom
// message or a summary on branch (output the model read, not words it wrote), as Propose refuses
// rule text; a seed's own text is never refused for it. It also refuses a catalogue with lines
// of the model's own kept for every model (layer.AllModels): a model's lines apply to that model
// only, and reach others only as a default its rules earn.
func CheckFromModel(branch []ledger.Entry, d layer.Definition) error {
	if d.Kind == layer.KindRemedy && containsString(Remedies, d.Name) {
		entries, err := ParseCatalogue(d.Name, d.Text)
		if err != nil {
			return err
		}
		seeds, _ := ParseCatalogue(d.Name, CatalogueSeeds[d.Name])
		for _, e := range entries {
			if e.Text == "" || e.Text == seedText(seeds, e.Detector) {
				continue
			}
			if d.Model == layer.AllModels {
				return fmt.Errorf("refused: catalogue %s carries your own line for %s; define it for your model, not for every model", d.Name, e.Detector)
			}
			if copiedFromOutput(branch, e.Text) {
				return fmt.Errorf("refused: catalogue %s's line for %s appears in tool output, a file or another message on this branch; a definition's text must be your own words", d.Name, e.Detector)
			}
		}
	}
	seed := layer.Shipped()[d.Key()]
	if quoted, ok := copiedSegment(branch, d.Text, seed); ok {
		what := "the " + d.Kind + " definition " + d.Name
		if d.Kind == layer.KindRemedy && !containsString(Remedies, d.Name) {
			what = "that rule text"
		}
		return fmt.Errorf("refused: %s quotes text found in tool output, a file or another message on this branch (%q); a definition's text must be your own words", what, clip(quoted, 80))
	}
	return nil
}

// MinCopiedWords is how many consecutive words of a definition make a quotation: a run of
// that many found in the branch's output is refused wherever it sits in the text (inside a
// line, between the model's own words). Shorter runs (a query, a delimiter, a field name) are
// common words, and are checked only as the whole text.
const MinCopiedWords = 6

// copiedSegment reports the first part of text found in output on branch: the whole text, or
// any run of MinCopiedWords consecutive words (copiedRun), so a quotation is found inside a line
// or a table value as well as on its own. A part that is also in seed (the shipped text of the
// name) is the kernel's own, not a quotation.
func copiedSegment(branch []ledger.Entry, text, seed string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	outputs := outputTexts(branch)
	if len(outputs) == 0 {
		return "", false
	}
	normalizedSeed := normalizeText(seed)
	if whole := normalizeText(text); !(normalizedSeed != "" && strings.Contains(normalizedSeed, whole)) && containedIn(outputs, whole) {
		return text, true
	}
	return copiedRun(outputs, text, seed)
}

// QuotedFromOutput reports the first run of MinCopiedWords consecutive words of text found in
// output on branch (copiedRun), the whole text aside: the check for a text short enough to be
// common words as a whole, such as a defined tool's query, which may well match a query the
// branch's output already shows.
func QuotedFromOutput(branch []ledger.Entry, text string) (string, bool) {
	outputs := outputTexts(branch)
	if strings.TrimSpace(text) == "" || len(outputs) == 0 {
		return "", false
	}
	return copiedRun(outputs, text, "")
}

// provenanceWords are text's words as the quotation check compares them: lowercased, split at
// every character that is not a letter or a digit, so a word glued to a table's delimiter or
// quote ("thin-chunk,ignore", "\"always") is still its own word.
func provenanceWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// copiedRun reports the first run of MinCopiedWords consecutive words of text (provenanceWords)
// found in outputs, a run also in seed excluded.
func copiedRun(outputs []string, text, seed string) (string, bool) {
	seedWords := " " + strings.Join(provenanceWords(seed), " ") + " "
	words := provenanceWords(text)
	runs := map[string]bool{}
	firsts := map[string]bool{}
	for i := 0; i+MinCopiedWords <= len(words); i++ {
		run := strings.Join(words[i:i+MinCopiedWords], " ")
		if !strings.Contains(seedWords, " "+run+" ") {
			runs[run] = true
			firsts[words[i]] = true
		}
	}
	if len(runs) == 0 {
		return "", false
	}
	// Every run of the text is looked up while each output's words are scanned once.
	for _, output := range outputs {
		outWords := provenanceWords(output)
		for i := 0; i+MinCopiedWords <= len(outWords); i++ {
			if !firsts[outWords[i]] {
				continue
			}
			if run := strings.Join(outWords[i:i+MinCopiedWords], " "); runs[run] {
				return run, true
			}
		}
	}
	return "", false
}

// clip shortens s to at most n runes for an error message.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func indexOf(values []string, value string) int {
	for i, v := range values {
		if v == value {
			return i
		}
	}
	return len(values)
}

// validateRemedy checks a remedy definition that is a catalogue table; a rule's table is
// checked where rules are read (parse).
func validateRemedy(d layer.Definition) error {
	decoded, err := tables.Rows.Decode(strings.TrimSpace(d.Text))
	if err != nil || len(decoded) != 1 || decoded[0].Name != CatalogueTable {
		if containsString(Remedies, d.Name) {
			return fmt.Errorf("a definition named after the catalogue remedy %s is its %s table", d.Name, CatalogueTable)
		}
		return nil
	}
	_, err = ParseCatalogue(d.Name, d.Text)
	return err
}

func init() {
	for remedy, text := range CatalogueSeeds {
		layer.Ship(layer.KindRemedy, remedy, text)
	}
	layer.RegisterValidator(layer.KindDetector, func(d layer.Definition) error { _, err := ParseDetector(d.Name, d.Text); return err })
	layer.RegisterValidator(layer.KindRemedy, validateRemedy)
}
