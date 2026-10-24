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

// ruleTable is the table a rule file holds. Rule files are in the rows codec, as the store's
// definitions are: the active codec is chosen per model for what a model reads and writes,
// while store files have one format every process can read.
const ruleTable = "rule"

var ruleFields = []string{"id", "detector", "models", "excluded", "remedy", "kind", "fullness", "text", "state", "reason", "proposer", "created", "start", "end", "baseline_events", "baseline_tokens", "after_events", "after_tokens", "after_passes"}

// Dir is the rules directory of store.
func Dir(store curated.Store) string { return filepath.Join(store.Dir, "rules") }

func path(store curated.Store, id string) string { return filepath.Join(Dir(store), id+".rows") }

// write stores r; the caller holds the store's lock.
func write(store curated.Store, r Rule) error {
	if err := os.MkdirAll(Dir(store), 0o700); err != nil {
		return err
	}
	row := []string{r.ID, r.Detector, strings.Join(r.Models, " "), strings.Join(r.Excluded, " "), r.Remedy, r.Kind, formatFullness(r.Fullness), r.Text, r.State, r.Reason, r.Proposer, r.Created.UTC().Format(time.RFC3339Nano),
		strconv.Itoa(r.Start), strconv.Itoa(r.End), strconv.Itoa(r.BaselineEvents), strconv.Itoa(r.BaselineTokens), strconv.Itoa(r.AfterEvents), strconv.Itoa(r.AfterTokens), strconv.Itoa(r.AfterPasses)}
	absent := make([]bool, len(row))
	for i, v := range row {
		absent[i] = v == ""
	}
	table := tables.Table{Name: ruleTable, Fields: ruleFields, Rows: [][]string{row}, Absent: [][]bool{absent}}
	tmp := path(store, r.ID) + ".tmp"
	if err := os.WriteFile(tmp, []byte(tables.Rows.Encode(table)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path(store, r.ID))
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

// read reads one rule file; one that fails Check is refused.
func read(file string) (Rule, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Rule{}, err
	}
	decoded, err := tables.Rows.Decode(string(data))
	if err != nil {
		return Rule{}, err
	}
	if len(decoded) != 1 || decoded[0].Name != ruleTable || len(decoded[0].Rows) != 1 {
		return Rule{}, fmt.Errorf("%s is not one %s row", file, ruleTable)
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
		return Rule{}, fmt.Errorf("%s is not a valid rule", file)
	}
	// A file edited by hand or written by another version is held to what Check allows of a
	// new rule: one whose parameters would break an invariant is never applied.
	if r, err = Check(r); err != nil {
		return Rule{}, fmt.Errorf("%s: %w", file, err)
	}
	switch r.State {
	case StateProbation, StateEstablished, StateRetired:
	default:
		return Rule{}, fmt.Errorf("%s has an unknown state %q", file, r.State)
	}
	return r, nil
}

// List reads every readable rule in the store, oldest first. A missing directory is empty;
// unreadable files and ones that fail Check are skipped, so one bad file never stops the rest.
func List(store curated.Store) ([]Rule, error) {
	entries, err := os.ReadDir(Dir(store))
	if errors.Is(err, os.ErrNotExist) {
		return []Rule{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Rule{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rows") {
			continue
		}
		if r, err := read(filepath.Join(Dir(store), entry.Name())); err == nil {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
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
