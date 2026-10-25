// Package rules is the memory's behaviour rules as data (docs/memory-interface.md, section 7):
// what a note must capture, when one record may replace another, what the dropper protects and
// how chunks are sized are rules the harness writes, tests and retires per model, not code
// paths. A rule answers one detector (a deterministic check over a model's own notes and the
// branch, counted after every memory-worker pass in the model's usage ledger) with one remedy
// from a closed catalogue, and survives only while its detector's rate falls for that model.
//
// No rule can touch the invariants: compaction never discards unobserved entries, cut points
// stay valid, coverage markers stay positional claims, the base memory tool stays active and
// the room budget stays the ceiling. Every remedy is a function that can only narrow what a
// worker records (drop a link, keep a record from dropping, ask once more over entries the
// pass already covered) or change how it is asked (a prompt line, a smaller chunk, the
// profile's codec and spec), and one whose parameters would break an invariant is refused.
package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// MinEvidence is the minimum-evidence rule rules follow, the same as the per-model harness's
// switches (harness.MinEvidence): a decision needs at least this many observer passes, since
// one is chance and two can be one mistake and its retry.
const MinEvidence = 3

// DefaultAfterModels is how many models a rule must be established for before it becomes a
// default for all: the minimum-evidence rule applied to models instead of passes, so one model's
// quirk, or two models sharing a family's quirk, never sets every model's behaviour.
const DefaultAfterModels = MinEvidence

// The states of a rule.
const (
	// StateProbation is a new rule: applied to its model while its detector's rate is measured.
	StateProbation = "probation"
	// StateEstablished is a rule whose detector's rate fell for its model over its probation.
	StateEstablished = "established"
	// StateRetired is a rule whose detector's rate did not fall: stored, never applied again.
	StateRetired = "retired"
)

// Who proposed a rule.
const (
	ProposerHarness = "harness"
	ProposerModel   = "model"
)

// AllModels in a rule's Models makes it a default: it applies to every model but its Excluded.
const AllModels = "*"

// Unclassified is the protect-kind kind for records without a kind.
const Unclassified = "unclassified"

// MaxTextChars bounds a prompt-line rule's text, in characters after whitespace is collapsed:
// it is carried in a worker's system prompt on every pass, so it is one short sentence, as a
// defined tool's description is.
const MaxTextChars = curated.MaxDefinitionDescriptionChars

// Rule is one rule record.
type Rule struct {
	ID string
	// Detector is the detector the rule answers.
	Detector string
	// Models are the model ids (provider/id) the rule applies to, or AllModels.
	Models []string
	// Excluded are the models a default does not apply to: those for which the same rule was
	// retired.
	Excluded []string
	// Remedy is one of the Remedy constants; Kind, Fullness and Text are its parameters.
	Remedy string
	// Kind is require-kind's and protect-kind's record kind.
	Kind string
	// Fullness is protect-kind's pool fullness (active tokens over target) up to which the kind
	// is never dropped.
	Fullness float64
	// Text is prompt-line's line.
	Text  string
	State string
	// Reason says why the rule is in its state.
	Reason   string
	Proposer string
	Created  time.Time
	// Start is the length of the model's usage ledger when the rule was written: its probation
	// counts the passes after it.
	Start int
	// End is the length of the model's usage ledger when the rule left probation.
	End int
	// BaselineEvents and BaselineTokens are the detector's events and observed source tokens
	// over the passes before the rule: the rate it must bring down.
	//
	// They are counted only over passes that counted the rule's count by its current revision
	// (CountRevision, curated.ModelEvent.Revisions), as are its after-counts, so a count whose
	// meaning changed is never compared across the change. uncited-assistant-specifics is at
	// revision 2 (only the specifics a reply added; revision 1 counted echoes too): passes
	// before it are no evidence, so no harness rule for it is written until MinEvidence fresh
	// passes exist. A rule written before the change keeps its baseline as written (the usage
	// ledger keeps counts, not entries, so no earlier pass can be re-counted); its after-counts
	// read fresh passes only, and a rule already decided is not re-judged.
	BaselineEvents, BaselineTokens int
	// AfterEvents, AfterTokens and AfterPasses are the same over its probation.
	AfterEvents, AfterTokens, AfterPasses int
}

// Default reports whether the rule is a default for all models.
func (r Rule) Default() bool {
	return len(r.Models) == 1 && r.Models[0] == AllModels
}

// Model is a single-model rule's model, "" for a default.
func (r Rule) Model() string {
	if r.Default() || len(r.Models) == 0 {
		return ""
	}
	return r.Models[0]
}

// AppliesTo reports whether the rule applies to model, whatever its state.
func (r Rule) AppliesTo(model string) bool {
	if r.Default() {
		return !containsString(r.Excluded, model)
	}
	return containsString(r.Models, model)
}

// Active reports whether the rule is applied: on probation or established.
func (r Rule) Active() bool { return r.State == StateProbation || r.State == StateEstablished }

// Signature is what makes two rules the same rule: the detector, the remedy and its
// parameters (a text compared case-insensitively). A default is promoted from single-model
// rules with one signature.
func (r Rule) Signature() string {
	return strings.Join([]string{r.Detector, r.Remedy, r.Kind, formatFullness(r.Fullness), strings.ToLower(r.Text)}, "\x1f")
}

// Describe is the rule as notifications and /om:status show it.
func (r Rule) Describe() string {
	remedy := r.Remedy
	switch r.Remedy {
	case RemedyRequireKind:
		remedy += " kind=" + r.Kind
	case RemedyProtectKind:
		remedy += fmt.Sprintf(" kind=%s fullness=%s", r.Kind, formatFullness(r.Fullness))
	case RemedyPromptLine:
		remedy += " " + strconv.Quote(r.Text)
	}
	return fmt.Sprintf("%s (%s → %s, by %s)", r.ID, r.Detector, remedy, r.Proposer)
}

// Counts is the counts that decided the rule's state, as /om:status lists them.
func (r Rule) Counts() string {
	out := fmt.Sprintf("before %d events / %d tokens", r.BaselineEvents, r.BaselineTokens)
	if r.AfterPasses > 0 || r.State != StateProbation {
		out += fmt.Sprintf(", since %d events / %d tokens over %d passes", r.AfterEvents, r.AfterTokens, r.AfterPasses)
	}
	return out
}

func formatFullness(f float64) string {
	if f == 0 {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// newID is a rule's id: the hash of its signature, models and creation time.
func newID(r Rule) string {
	return ids.HashID(r.Signature() + "\x1f" + strings.Join(r.Models, " ") + "\x1f" + r.Created.Format(time.RFC3339Nano))
}

// --- store ---------------------------------------------------------------------

// ruleTable is the table a rule's text holds. Rule texts are in the rows codec, as the store's
// definitions are: the active codec is chosen per model for what a model reads and writes,
// while store files have one format every process can read.
const ruleTable = "rule"

var ruleFields = []string{"id", "detector", "models", "excluded", "remedy", "kind", "fullness", "text", "state", "reason", "proposer", "created", "start", "end", "baseline_events", "baseline_tokens", "after_events", "after_tokens", "after_passes"}

// A rule lives in the store's layer (docs/memory-interface.md, section 8) as a remedy
// definition named by its id, under its model, or under every model for a default:
// <store>/harness/<model>/remedy/<id>.rows. Rules written before the layer, in
// <store>/rules/<id>.rows, are moved there on first use (migrate).

// legacyDir is where rules were kept before the layer.
func legacyDir(store curated.Store) string { return filepath.Join(store.Dir, "rules") }

// layerModel is the layer model a rule is kept under: its model, or every model for a default.
func layerModel(r Rule) (string, error) {
	switch {
	case r.Default():
		return layer.AllModels, nil
	case r.Model() != "":
		return r.Model(), nil
	}
	return "", fmt.Errorf("rule %s applies to no model", r.ID)
}

// encode is r's table in the rows codec.
func encode(r Rule) string {
	row := []string{r.ID, r.Detector, strings.Join(r.Models, " "), strings.Join(r.Excluded, " "), r.Remedy, r.Kind, formatFullness(r.Fullness), r.Text, r.State, r.Reason, r.Proposer, r.Created.UTC().Format(time.RFC3339Nano),
		strconv.Itoa(r.Start), strconv.Itoa(r.End), strconv.Itoa(r.BaselineEvents), strconv.Itoa(r.BaselineTokens), strconv.Itoa(r.AfterEvents), strconv.Itoa(r.AfterTokens), strconv.Itoa(r.AfterPasses)}
	absent := make([]bool, len(row))
	for i, v := range row {
		absent[i] = v == ""
	}
	return tables.Rows.Encode(tables.Table{Name: ruleTable, Fields: ruleFields, Rows: [][]string{row}, Absent: [][]bool{absent}})
}

// write stores r in the layer; the caller holds the store's lock.
func write(store curated.Store, r Rule) error {
	model, err := layerModel(r)
	if err != nil {
		return err
	}
	_, err = store.Layer().PutLocked(layer.Definition{Kind: layer.KindRemedy, Name: r.ID, Model: model, Text: encode(r)})
	return err
}

// Add checks r (Check), stamps it (id, creation time, and Start from its model's ledger when
// it applies to one model) and stores it. A rule without a state starts on probation, and one
// without a proposer is the harness's. It is how rules are written outside the lifecycle: by
// hand, and in tests.
func Add(store curated.Store, r Rule) (Rule, error) {
	r, err := Check(r)
	if err != nil {
		return Rule{}, err
	}
	if len(r.Models) == 0 {
		return Rule{}, errors.New("a rule needs the model ids it applies to, or " + AllModels)
	}
	if r.State == "" {
		r.State = StateProbation
	}
	if r.Proposer == "" {
		r.Proposer = ProposerHarness
	}
	migrate(store)
	err = store.Locked(func() error {
		r.Created = store.Now().UTC()
		if model := r.Model(); model != "" {
			events, err := store.ModelEvents(model)
			if err != nil {
				return err
			}
			r.Start = len(events)
		}
		r.ID = newID(r)
		return write(store, r)
	})
	if err != nil {
		return Rule{}, err
	}
	return r, nil
}

// parse reads one rule from its text (where names the file); one that fails Check is refused.
func parse(where, text string) (Rule, error) {
	decoded, err := tables.Rows.Decode(text)
	if err != nil {
		return Rule{}, err
	}
	if len(decoded) != 1 || decoded[0].Name != ruleTable || len(decoded[0].Rows) != 1 {
		return Rule{}, fmt.Errorf("%s is not one %s row", where, ruleTable)
	}
	t := decoded[0]
	cell := func(field string) string { v, _ := t.Cell(0, field); return v }
	num := func(field string) int { n, _ := strconv.Atoi(cell(field)); return n }
	r := Rule{
		ID: cell("id"), Detector: cell("detector"), Models: fields(cell("models")), Excluded: fields(cell("excluded")),
		Remedy: cell("remedy"), Kind: cell("kind"), Text: cell("text"), State: cell("state"), Reason: cell("reason"), Proposer: cell("proposer"),
		Start: num("start"), End: num("end"), BaselineEvents: num("baseline_events"), BaselineTokens: num("baseline_tokens"),
		AfterEvents: num("after_events"), AfterTokens: num("after_tokens"), AfterPasses: num("after_passes"),
	}
	r.Fullness, _ = strconv.ParseFloat(cell("fullness"), 64)
	r.Created, _ = time.Parse(time.RFC3339Nano, cell("created"))
	if r.ID == "" || len(r.Models) == 0 {
		return Rule{}, fmt.Errorf("%s is not a valid rule", where)
	}
	// A file edited by hand or written by another version is held to what Check allows of a
	// new rule: one whose parameters would break an invariant is never applied.
	if r, err = Check(r); err != nil {
		return Rule{}, fmt.Errorf("%s: %w", where, err)
	}
	switch r.State {
	case StateProbation, StateEstablished, StateRetired:
	default:
		return Rule{}, fmt.Errorf("%s has an unknown state %q", where, r.State)
	}
	return r, nil
}

// List reads every readable rule in the store, oldest first, after moving any rules written
// before the layer into it. Files the layer refuses (the kernel, a stale header), unreadable
// rules and ones that fail Check are skipped, so one bad file never stops the rest. A caller
// already holding the store's lock uses list.
func List(store curated.Store) ([]Rule, error) {
	migrate(store)
	return list(store)
}

// list is List without the migration, for a caller holding the store's lock.
func list(store curated.Store) ([]Rule, error) {
	defs, _, err := store.Layer().ListAll(layer.KindRemedy)
	if err != nil {
		return nil, err
	}
	return rulesOf(defs), nil
}

// rulesOf are the rules among remedy definitions, oldest first: a catalogue table, a file that
// is not a valid rule, and a rule kept under another model than its own are skipped.
func rulesOf(defs []layer.Definition) []Rule {
	out := []Rule{}
	for _, d := range defs {
		r, err := parse(d.Kind+"/"+d.Name, d.Text)
		if err != nil || r.ID != d.Name {
			continue
		}
		// A rule kept under another model than its own is a file moved to apply it elsewhere.
		if model, err := layerModel(r); err != nil || model != d.Model {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// The suffixes a legacy rule file is renamed with once migration has handled it: moved into
// the layer, or refused (kept, never applied). Neither is read again.
const (
	migratedSuffix = ".migrated"
	refusedSuffix  = ".refused"
)

// migrate moves rules written before the layer into it, when there are any, under the store's
// lock. Each legacy file a rule reads from becomes a layer file of the same rule (a rule the
// layer already holds keeps the layer's), and the legacy file is renamed with migratedSuffix,
// so nothing is lost; one that is not a valid rule is renamed with refusedSuffix. A failure
// leaves the rest to be tried again, and the layer's rules still work.
func migrate(store curated.Store) {
	if names := legacyNames(store); len(names) == 0 {
		return
	}
	_ = store.Locked(func() error {
		ls := store.Layer()
		for _, name := range legacyNames(store) {
			file := filepath.Join(legacyDir(store), name)
			suffix := migratedSuffix
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			r, err := parse(file, string(data))
			if err == nil {
				var model string
				if model, err = layerModel(r); err == nil {
					if _, statErr := os.Stat(ls.Path(model, layer.KindRemedy, r.ID)); errors.Is(statErr, os.ErrNotExist) {
						err = write(store, r)
					}
				}
			}
			if err != nil {
				suffix = refusedSuffix
			}
			if err := os.Rename(file, file+suffix); err != nil {
				return err
			}
		}
		return nil
	})
}

// legacyNames are the .rows files in the legacy rules directory.
func legacyNames(store curated.Store) []string {
	entries, err := os.ReadDir(legacyDir(store))
	if err != nil {
		return nil
	}
	out := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".rows") {
			out = append(out, entry.Name())
		}
	}
	return out
}

// fields splits a space-separated list; nil when it is empty.
func fields(s string) []string {
	if f := strings.Fields(s); len(f) > 0 {
		return f
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
