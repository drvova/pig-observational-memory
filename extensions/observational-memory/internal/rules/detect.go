package rules

import (
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// The detectors (docs/memory-interface.md, section 7).
const (
	// DetectUncitedAssistantSpecifics counts covered assistant entries no note of the pass cites
	// whose text adds specifics (ledger.Specifics: numbers, names, relative dates) that neither
	// the user messages it answers, the memory results of its turn nor the notes in memory give
	// (UncitedAssistantSpecifics). What it counts changed at revision 2 (CountRevision).
	DetectUncitedAssistantSpecifics = "uncited-assistant-specifics"
	// DetectCrossScopeSupersede counts supersedes links between notes whose scopes differ.
	DetectCrossScopeSupersede = "cross-scope-supersede"
	// DetectUnresolvedRelativeDate counts relative-date phrases in new notes that the
	// renderer has to resolve (ledger.ResolveRelativeDates).
	DetectUnresolvedRelativeDate = "unresolved-relative-date"
	// DetectThinChunk counts chunks whose notes are far shorter than their source while
	// specifics of it are left uncited.
	DetectThinChunk = "thin-chunk"
	// DetectDroppedThenQueried counts records the dropper retired that a later memory query
	// asked for.
	DetectDroppedThenQueried = "dropped-then-queried"
	// DetectDecodeFailure counts rows and queries that did not decode in the model's codec.
	DetectDecodeFailure = "decode-failure"
)

// CountCodecDecodeFailure is the pass count, beside DetectDecodeFailure's, of the decode
// failures that were rows the codec could not decode. A memory query that did not parse is a
// syntax error in the query language, which no codec switch can fix (docs/memory-interface.md,
// section 6), so switch-codec is written and judged on this count alone, while long-spec
// answers both. It is not a detector of its own.
const CountCodecDecodeFailure = DetectDecodeFailure + ":codec"

// CountEmptyPass is the count an observer pass that ran over a chunk and recorded nothing
// carries (Pass.Empty), one per such pass. It is kernel code, read by probation
// (GradingKeys) and the revision pass's evidence, so a definition that silences the observer is
// counted like any other failure; no rule is written or judged on it. It is not a detector.
const CountEmptyPass = "observer-empty"

// Detectors lists the detectors in the order the spec's table names them.
var Detectors = []string{DetectUncitedAssistantSpecifics, DetectCrossScopeSupersede, DetectUnresolvedRelativeDate, DetectThinChunk, DetectDroppedThenQueried, DetectDecodeFailure}

// detectorDescriptions say what each kernel check counts.
var detectorDescriptions = map[string]string{
	DetectUncitedAssistantSpecifics: "assistant entries no note cites that add specifics (numbers, names, lists) the user's messages, the turn's memory results and the notes in memory did not give",
	DetectCrossScopeSupersede:       "supersedes links between notes whose scopes differ",
	DetectUnresolvedRelativeDate:    "relative-date phrases in new notes the renderer had to resolve",
	DetectThinChunk:                 "chunks whose notes are far shorter than their source with specifics left uncited",
	DetectDroppedThenQueried:        "records the dropper retired that a later memory query asked for",
	DetectDecodeFailure:             "rows or queries that did not decode in the model's codec",
}

// DetectorSeeds are the shipped detectors as declarations (DetectorTable), the seeds a model's
// own detector definition of the same name overrides (docs/memory-interface.md, section 8):
// each names its kernel check and says what it counts. The counting itself is kernel code; the
// declaration is what an override records the digest of, so an upgrade that changes what a
// seed counts makes the override stale.
var DetectorSeeds = func() map[string]string {
	out := map[string]string{}
	for name, description := range detectorDescriptions {
		out[name] = detectorSeed(name, description)
	}
	return out
}()

func init() {
	for name, text := range DetectorSeeds {
		layer.Ship(layer.KindDetector, name, text)
	}
}

// The worker stages a pass is recorded for.
const (
	StageObserver  = "observer"
	StageReflector = "reflector"
	StageDropper   = "dropper"
)

// ObserverPass is what the detectors read after an observer chunk was recorded.
type ObserverPass struct {
	// Branch is the branch the pass read.
	Branch []ledger.Entry
	// Covered are the source entries the pass covered: the chunk's entries up to its final
	// coverage boundary.
	Covered []ledger.Entry
	// Notes are the notes the pass recorded.
	Notes []ledger.Observation
	// Known are the notes in memory before the pass (a supersedes link may name one).
	Known []ledger.Observation
	// Aliases are the subject aliases recorded before the pass, in ledger order: a detector's
	// query reads the pass's notes with them, as the memory tool reads notes
	// (memquery.Records.Aliases).
	Aliases []ledger.SubjectAlias
	// Defined are the tools the agent defined, by name, with their stored queries: a call of
	// one is a memory query too (MemoryQueries).
	Defined map[string]string
	// Detectors are the detectors the worker model defined in its layer (ModelDetectors),
	// counted beside the kernel's checks.
	Detectors []DetectorDef
}

// Findings are what the detectors counted in one pass, and what they flagged for the remedies.
type Findings struct {
	// Counts are the events per detector.
	Counts map[string]int
	// Uncited are the covered assistant entry ids no note of the pass cites that add specifics
	// to their turn (UncitedAssistantSpecifics).
	Uncited []string
	// CrossScope are the "note id>superseded id" links between notes whose scopes differ.
	CrossScope []string
	// DroppedKinds are the kinds of the dropped records a memory query asked for.
	DroppedKinds map[string]int
	// Defined are the counts of the detectors the model defined, by name; recorded under
	// DefinedCountKey, never judging a rule.
	Defined map[string]int
}

// DetectObserverPass runs every note detector over one observer pass. It is pure: the same
// pass always gives the same findings. decode-failure is counted from the usage ledger instead
// (DecodeFailuresSince).
func DetectObserverPass(p ObserverPass) Findings {
	f := Findings{Counts: map[string]int{}, DroppedKinds: map[string]int{}}
	f.Uncited = UncitedAssistantSpecifics(p)
	f.CrossScope = CrossScopeSupersedes(p.Notes, p.Known)
	f.Counts[DetectUncitedAssistantSpecifics] = len(f.Uncited)
	f.Counts[DetectCrossScopeSupersede] = len(f.CrossScope)
	f.Counts[DetectUnresolvedRelativeDate] = UnresolvedRelativeDates(p.Notes)
	if ThinChunk(p.Covered, p.Notes) {
		f.Counts[DetectThinChunk] = 1
	}
	asked := DroppedThenQueried(p.Branch, p.Covered, p.Defined)
	f.Counts[DetectDroppedThenQueried] = len(asked)
	for _, o := range asked {
		f.DroppedKinds[kindName(o.Kind)]++
	}
	for _, d := range p.Detectors {
		if f.Defined == nil {
			f.Defined = map[string]int{}
		}
		f.Defined[d.Name] = d.Count(p, f.Counts)
	}
	return f
}

func kindName(kind ledger.ObservationKind) string {
	if kind == "" {
		return Unclassified
	}
	return string(kind)
}

// AssistantText is an assistant message's text blocks, what the assistant said (thinking and
// tool calls left out); ok is false for any other entry.
func AssistantText(entry ledger.Entry) (string, bool) {
	if entry.Type != "message" || entry.Message == nil || entry.Message["role"] != "assistant" {
		return "", false
	}
	return blockText(entry.Message["content"]), true
}

// conversationText is a user or assistant message's text; tool output, custom messages and
// summaries are not conversation.
func conversationText(entry ledger.Entry) (string, bool) {
	if text, ok := AssistantText(entry); ok {
		return text, true
	}
	if entry.Type == "message" && entry.Message != nil && entry.Message["role"] == "user" {
		return blockText(entry.Message["content"]), true
	}
	return "", false
}

func blockText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		parts := []string{}
		for _, raw := range c {
			if block, ok := raw.(map[string]any); ok && block["type"] == "text" {
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// UncitedAssistantSpecifics are the pass's covered assistant entries, in branch order, that no
// note of the pass cites and whose text adds specifics to its turn: the specifics
// (ledger.Specifics) left after the set difference (ledger.Added: whole items, numbers
// normalized as Specifics normalizes them, a carrier's lowercase words giving no name) with
//
//   - the user messages it answers: every user message on the branch since the previous
//     assistant entry that ended a turn (endsTurn), walking back over the turn's tool-call
//     entries (an assistant entry with stop reason toolUse, or with tool calls and no text),
//     tool results and other entries, so a reply after a run of tool calls or after a steering
//     message queued mid-turn still answers the message that opened the turn, and a reply with
//     no user message since the previous one answers none;
//   - the results of the memory tool calls in its turn (p.Defined names the agent's own memory
//     tools): what memory just returned, which the observer's chunk skips;
//   - every note in memory once the pass is recorded: those before it (p.Known) and the pass's
//     own (p.Notes). A specific a note already holds, from this exchange, an earlier turn or an
//     earlier compaction, is not lost by leaving the reply uncited, and a note repeating it
//     would only be an echo.
//
// Only the pass's own notes count as citing an entry, never p.Known: a covered entry is judged
// by what the pass recorded. An assistant reply that only repeats what the user said or what
// memory holds is no failure to record it, so it is never counted, and a remedy is never asked
// to write it again (require-kind and keep-head act on these entries). The turn is read on
// p.Branch; an entry missing from it is read in p.Covered.
func UncitedAssistantSpecifics(p ObserverPass) []string {
	cited := map[string]bool{}
	for _, o := range p.Notes {
		for _, id := range o.SourceEntryIDs {
			cited[id] = true
		}
	}
	var memory ledger.Given
	memoryRead := false
	branchIndex := ledger.EntryIndexByID(p.Branch)
	coveredIndex := ledger.EntryIndexByID(p.Covered)
	out := []string{}
	for _, entry := range p.Covered {
		text, ok := AssistantText(entry)
		if !ok || cited[entry.ID] || !ledger.HasSpecifics(text) {
			continue
		}
		history, at := p.Branch, -1
		if i, found := branchIndex[entry.ID]; found {
			at = i
		} else {
			history, at = p.Covered, coveredIndex[entry.ID]
		}
		if !memoryRead {
			memory, memoryRead = ledger.NewGiven(append(noteContents(p.Known), noteContents(p.Notes)...)), true
		}
		if len(ledger.Added(text, ledger.NewGiven(turnCarriers(history, at, p.Defined)), memory)) > 0 {
			out = append(out, entry.ID)
		}
	}
	return out
}

// noteContents are the notes' contents.
func noteContents(notes []ledger.Observation) []string {
	out := make([]string, 0, len(notes))
	for _, o := range notes {
		out = append(out, o.Content)
	}
	return out
}

// turnCarriers are the texts of its turn that already give the specifics of the assistant entry
// at history[at]: the user messages it answers and the memory tool results of its turn
// (UncitedAssistantSpecifics).
func turnCarriers(history []ledger.Entry, at int, defined map[string]string) []string {
	carriers := []string{}
	for i := at - 1; i >= 0 && !endsTurn(history[i]); i-- {
		if isRole(history[i], "user") || isMemoryResult(history[i], defined) {
			carriers = append(carriers, blockText(history[i].Message["content"]))
		}
	}
	return carriers
}

// endsTurn reports whether entry is an assistant message that ended a turn: not one that only
// called tools (stop reason toolUse, or tool calls with no text), after which the turn goes on.
func endsTurn(entry ledger.Entry) bool {
	if !isRole(entry, "assistant") {
		return false
	}
	if entry.Message["stopReason"] == "toolUse" {
		return false
	}
	if strings.TrimSpace(blockText(entry.Message["content"])) != "" {
		return true
	}
	blocks, _ := entry.Message["content"].([]any)
	for _, raw := range blocks {
		if block, ok := raw.(map[string]any); ok && block["type"] == "toolCall" {
			return false
		}
	}
	return true
}

// isMemoryResult reports whether entry is the result of a memory tool call: the memory tool's
// own, or a tool the agent defined (defined, by name), the results the observer's chunk skips.
func isMemoryResult(entry ledger.Entry, defined map[string]string) bool {
	if !isRole(entry, "toolResult") {
		return false
	}
	name, _ := entry.Message["toolName"].(string)
	if name == memquery.ToolName {
		return true
	}
	_, ok := defined[name]
	return ok && name != ""
}

// isRole reports whether entry is a message of role.
func isRole(entry ledger.Entry, role string) bool {
	return entry.Type == "message" && entry.Message != nil && entry.Message["role"] == role
}

// CrossScopeSupersedes are the supersedes links from notes to notes (in notes or known) whose
// scopes differ, as "note>superseded". An empty scope is no qualifier, so it differs from no
// scope: only two named scopes that differ tell two figures apart.
func CrossScopeSupersedes(notes, known []ledger.Observation) []string {
	scope := map[string]string{}
	for _, o := range known {
		scope[o.ID] = o.Scope
	}
	for _, o := range notes {
		scope[o.ID] = o.Scope
	}
	out := []string{}
	for _, o := range notes {
		for _, target := range o.Supersedes {
			if other, ok := scope[target]; ok && scopesDiffer(o.Scope, other) {
				out = append(out, o.ID+">"+target)
			}
		}
	}
	return out
}

func scopesDiffer(a, b string) bool {
	a, b = ledger.NormalizeScope(a), ledger.NormalizeScope(b)
	return a != "" && b != "" && a != b
}

// UnresolvedRelativeDates counts the relative-date phrases in notes that the renderer resolves
// against the note's own time: phrases the note left relative.
func UnresolvedRelativeDates(notes []ledger.Observation) int {
	count := 0
	for _, o := range notes {
		anchor, ok := serialize.ParseTimestamp(o.Timestamp)
		if !ok {
			anchor = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		count += strings.Count(ledger.ResolveRelativeDates(o.Content, anchor), " [=") - strings.Count(o.Content, " [=")
	}
	return count
}

// ThinChunk reports whether a chunk's notes are far shorter than their source: the user and
// assistant text it covered has specifics no note carries, and the notes together are shorter
// than a bare list of those left-out specifics. Notes that could not even have listed what
// they dropped did not compress the chunk; they skipped it. The bound is the chunk's own
// left-out detail, not a ratio picked for a score.
func ThinChunk(covered []ledger.Entry, notes []ledger.Observation) bool {
	contents := make([]string, 0, len(notes))
	noteTokens := 0
	for _, o := range notes {
		contents = append(contents, o.Content)
		noteTokens += tokens.EstimateStringTokens(o.Content)
	}
	missing := []string{}
	for _, entry := range covered {
		if text, ok := conversationText(entry); ok {
			missing = append(missing, ledger.MissingSpecifics(text, contents)...)
		}
	}
	if len(missing) == 0 {
		return false
	}
	return noteTokens < tokens.EstimateStringTokens(strings.Join(missing, ", "))
}

// DroppedThenQueried are the dropped records the memory tool calls in covered asked for: a
// recall of a record the dropper had retired before the call, or a search whose words all
// appear in such a record. Each record counts once per call, of the memory tool or of a tool
// the agent defined (defined, by name). Only calls in the covered entries are read, so a call
// is counted by the one pass that covered it.
func DroppedThenQueried(branch, covered []ledger.Entry, defined map[string]string) []ledger.Observation {
	inCovered := map[string]bool{}
	for _, e := range covered {
		inCovered[e.ID] = true
	}
	records := map[string]ledger.Observation{}
	droppedAt := map[string]int{}
	out := []ledger.Observation{}
	for i, entry := range branch {
		if data, ok := ledger.ObservationsRecordedEntry(entry); ok {
			for _, o := range data.Observations {
				records[o.ID] = o
			}
		}
		if entry.Type == "compaction" {
			if details, ok := ledger.MemoryDetailsFrom(entry.Details); ok {
				for _, o := range details.Observations {
					if _, seen := records[o.ID]; !seen {
						records[o.ID] = o
					}
				}
			}
		}
		if data, ok := ledger.ObservationsDroppedEntry(entry); ok {
			for _, id := range data.ObservationIDs {
				if _, seen := droppedAt[id]; !seen {
					droppedAt[id] = i
				}
			}
		}
		if !inCovered[entry.ID] {
			continue
		}
		for _, query := range MemoryQueries(entry, defined) {
			q, err := memquery.Parse(query)
			if err != nil || q.Write != "" {
				continue
			}
			asked := map[string]bool{}
			if q.Source == memquery.SourceRecall {
				if _, dropped := droppedAt[q.ID]; dropped {
					asked[q.ID] = true
				}
			}
			for _, stage := range q.Stages {
				if stage.Kind != memquery.StageSearch {
					continue
				}
				words := strings.Fields(strings.ToLower(stage.Words))
				for id := range droppedAt {
					if o, ok := records[id]; ok && len(words) > 0 && containsAll(strings.ToLower(o.Content), words) {
						asked[id] = true
					}
				}
			}
			ids := make([]string, 0, len(asked))
			for id := range asked {
				if o, ok := records[id]; ok {
					ids = append(ids, o.ID)
				}
			}
			sort.Strings(ids)
			for _, id := range ids {
				out = append(out, records[id])
			}
		}
	}
	return out
}

func containsAll(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// MemoryQueries are the queries of the memory tool calls an assistant entry made, and of its
// calls of the tools the agent defined (defined maps a defined tool's name to its stored
// query): the stored query with the call's input substituted, as the tool runs it.
func MemoryQueries(entry ledger.Entry, defined map[string]string) []string {
	if entry.Type != "message" || entry.Message == nil || entry.Message["role"] != "assistant" {
		return nil
	}
	blocks, _ := entry.Message["content"].([]any)
	out := []string{}
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok || block["type"] != "toolCall" {
			continue
		}
		name, _ := block["name"].(string)
		args, _ := block["arguments"].(map[string]any)
		query := ""
		if name == memquery.ToolName {
			query, _ = args["query"].(string)
		} else if stored, ok := defined[name]; ok {
			input, _ := args[memquery.InputParameter].(string)
			query = memquery.Substitute(stored, input)
		}
		if strings.TrimSpace(query) != "" {
			out = append(out, query)
		}
	}
	return out
}

// DecodeFailuresSince counts the decode failures in a model's usage ledger since its latest
// observer pass: codec is the rows tables the codec could not decode, syntax the memory
// queries that did not parse. Reflector and dropper passes are not boundaries: they carry no
// rate (isObserverPass), so a failure counted on one would never be judged.
func DecodeFailuresSince(events []curated.ModelEvent) (codec, syntax int) {
	start := 0
	for i, e := range events {
		if isObserverPass(e) {
			start = i + 1
		}
	}
	for _, e := range events[start:] {
		switch {
		case e.Kind == curated.ModelKindDecode && e.Outcome == curated.OutcomeError:
			codec++
		case e.Kind == curated.ModelKindMemory && e.Syntax:
			syntax++
		}
	}
	return codec, syntax
}

// SourceTokens is the estimated tokens of entries: a pass's observed source tokens.
func SourceTokens(entries []ledger.Entry) int {
	total := 0
	for _, e := range entries {
		total += tokens.EstimateEntryTokens(e)
	}
	return total
}
