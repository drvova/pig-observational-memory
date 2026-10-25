// Package declare holds the shipped seeds of the declarative layer kinds and the interpreters
// that read them (docs/memory-interface.md, section 8): a format declaration becomes a strict
// codec (tables.Declared, accepted only once it round-trips the kernel's test tables), a
// schema declaration validates note fields (ledger.NoteSchema), and a summary declaration is
// parsed and validated only (ParseSummary): the compaction summary is selection.Render, so no
// summary definition, shipped or written (by v1 too), shapes it any more.
//
// Every definition is data: a declaration table, a query, or text. Nothing a model defines is
// run as code; the interpreters here are the same for every model.
//
// Active is a model's effective layer as the workers, the compaction hook, the memory tool and
// the harness profile read it: each name resolves to the model's own definition (held or
// written), else the one kept for every model, else its seed. A name that stands as its seed
// is interpreted exactly as the extension behaved before the layer existed: the rows and jsonl
// seeds are the kernel's native codecs, the summary seed is the kernel's native tables layout,
// the schema seed accepts exactly the structured fields the observer always accepted. The
// worker instructions are seeded by internal/agents, the detectors and the remedy catalogue by
// internal/rules.
package declare

import (
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The seed names of the kinds this package interprets.
const (
	// FormatRows and FormatJSONL are the shipped formats, named after the codecs they declare.
	FormatRows  = "rows"
	FormatJSONL = "jsonl"
	// SchemaNotes is the note schema the observer validates against.
	SchemaNotes = "observation"
	// SummaryDefault is the name of the shipped summary declaration, kept so stored summary
	// definitions still validate and stay listed; the compaction summary is selection.Render.
	SummaryDefault = "default"
)

// rowsFormat and jsonlFormat are the shipped codecs as declarations.
var (
	rowsFormat  = tables.Format{Name: FormatRows, Header: tables.HeaderCounted, Delimiter: ',', Quote: '"', Escape: '\\', Escapes: "nrt", Special: ":|", Indent: 2}
	jsonlFormat = tables.Format{Name: FormatJSONL, Header: tables.HeaderJSONLines, Delimiter: ',', Quote: '"', Escape: '\\'}
)

// SchemaSeed is the shipped note schema: the four structured fields of section 2, with the
// descriptions the observer's record_observations tool has always carried.
var SchemaSeed = tables.Rows.Encode(tables.Table{
	Name:   ledger.SchemaTable,
	Fields: ledger.SchemaColumns,
	Rows: [][]string{
		{ledger.NoteFieldKind, ledger.FieldEnum, kindValues(), "", ""},
		{ledger.NoteFieldSubject, ledger.FieldText, "", "", "Short lowercase noun phrase grouping related notes, root topic first: reuse a root from CURRENT SUBJECTS and put the specific part after '/', e.g. 'baking', 'running/half marathon'."},
		{ledger.NoteFieldScope, ledger.FieldText, "", "", "Qualifier that tells two figures apart, e.g. 'hard difficulty', 'on edX'."},
		{ledger.NoteFieldSupersedes, ledger.FieldIDs, "", "", "Ids of earlier observations this one replaces. Only when the user explicitly corrects or replaces them."},
	},
	Absent: [][]bool{
		{false, false, false, true, true},
		{false, false, true, true, false},
		{false, false, true, true, false},
		{false, false, true, true, false},
	},
})

func kindValues() string {
	out := make([]string, 0, len(ledger.ObservationKindValues))
	for _, k := range ledger.ObservationKindValues {
		out = append(out, string(k))
	}
	return strings.Join(out, " ")
}

// SummarySeed is the shipped summary as a declaration (section 3): the reflections, then the
// notes grouped by subject. It ships only so stored summary definitions (a v1 user's
// summary/default included) still validate and stay listed in the layer; nothing renders it or
// them, since the compaction summary is selection.Render (docs/memory-v2.md).
var SummarySeed = tables.Rows.Encode(tables.Table{
	Name:   SummaryTable,
	Fields: SummaryColumns,
	Rows: [][]string{
		{"reflections", "reflections", "id dates content"},
		{"notes", "notes | group by subject", "id when kind scope relevance content superseded_by"},
	},
})

// The seeds this package ships, and the validators of the kinds it interprets.
func init() {
	layer.Ship(layer.KindFormat, FormatRows, rowsFormat.Declaration())
	layer.Ship(layer.KindFormat, FormatJSONL, jsonlFormat.Declaration())
	layer.Ship(layer.KindSchema, SchemaNotes, SchemaSeed)
	layer.Ship(layer.KindSummary, SummaryDefault, SummarySeed)
	layer.RegisterValidator(layer.KindFormat, func(d layer.Definition) error { _, err := FormatCodec(d.Name, d.Text); return err })
	layer.RegisterValidator(layer.KindSchema, func(d layer.Definition) error { _, err := ledger.ParseNoteSchema(d.Text); return err })
	layer.RegisterValidator(layer.KindSummary, func(d layer.Definition) error { _, err := ParseSummary(d.Text); return err })
}

// FormatCodec interprets a format declaration as a codec, and accepts it only when the codec
// round-trips the kernel's test tables (kernel.AcceptCodec); otherwise it is refused.
func FormatCodec(name, text string) (tables.Codec, error) {
	f, err := tables.ParseFormat(name, text)
	if err != nil {
		return nil, err
	}
	c, err := tables.Declared(f)
	if err != nil {
		return nil, err
	}
	if err := kernel.AcceptCodec(c); err != nil {
		return nil, err
	}
	return c, nil
}

// Active is a model's effective layer, interpreted.
type Active struct {
	h layer.Harness
}

// Of is the harness h interpreted.
func Of(h layer.Harness) Active { return Active{h: h} }

// SeedsOnly is model's layer with nothing defined: every name stands as its seed, which is how
// the extension behaves without a store.
func SeedsOnly(model string) Active { return Active{h: layer.Seeded(model)} }

// Harness is the effective layer itself.
func (a Active) Harness() layer.Harness { return a.h }

// Model is the model the layer is for.
func (a Active) Model() string { return a.h.Model }

// Codec is the codec named name for this model: the kernel's native codec while the name
// stands as its seed, else the model's format declaration interpreted (FormatCodec). A
// declaration that no longer interprets falls back to the native codec; false when neither
// exists.
func (a Active) Codec(name string) (tables.Codec, bool) {
	native, hasNative := tables.Lookup(name)
	d, ok := a.h.Resolve(layer.KindFormat, name)
	if !ok || (d.State == layer.StateSeed && hasNative) {
		return native, hasNative
	}
	if c, err := FormatCodec(name, d.Text); err == nil {
		return c, true
	}
	return native, hasNative
}

// CodecNames are the codecs this model can be switched through, in order: the shipped codecs
// (tables.Codecs), then the formats the model defined under new names, in the order they were
// first written (layer.Harness.FirstDefined; one only held comes last), then by name. The
// profile replays the ledger against this order, so defining or forgetting a format never
// moves one the model was already switched to.
func (a Active) CodecNames() []string {
	out := []string{}
	for _, c := range tables.Codecs() {
		out = append(out, c.Name())
	}
	added := []string{}
	for _, d := range a.h.List(layer.KindFormat) {
		if !containsString(out, d.Name) {
			if _, ok := a.Codec(d.Name); ok {
				added = append(added, d.Name)
			}
		}
	}
	sort.SliceStable(added, func(i, j int) bool {
		ti, oki := a.h.FirstDefined(layer.KindFormat, added[i])
		tj, okj := a.h.FirstDefined(layer.KindFormat, added[j])
		switch {
		case oki != okj:
			return oki
		case oki && !ti.Equal(tj):
			return ti.Before(tj)
		}
		return added[i] < added[j]
	})
	return append(out, added...)
}

// Instructions are the model's own instructions for a worker stage, "" while the seed stands.
// The worker prompt puts them after the kernel's fixed rules (agents.ComposeStage), never in
// their place: after the seed instructions for an addition, in the seed's place for a
// replacement (ReplacesSeed).
func (a Active) Instructions(stage string) string {
	if d, ok := a.h.Get(layer.KindInstructions, stage); ok {
		return d.Text
	}
	return ""
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
