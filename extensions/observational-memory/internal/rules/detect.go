package rules

import (
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/legacyquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
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
	// DetectDecodeFailure counts rows that did not decode in the model's codec.
	DetectDecodeFailure = "decode-failure"
)

// CountCodecDecodeFailure is the pass count, beside DetectDecodeFailure's, of the decode
// failures that were rows the codec could not decode. A memory query that did not parse is a
// syntax error in the query language, which no codec switch can fix (docs/memory-interface.md,
// section 6), so switch-codec is written and judged on this count alone, while long-spec
// answers both. It is not a detector of its own.
const CountCodecDecodeFailure = DetectDecodeFailure + ":codec"

// Detectors lists the detectors in the order the spec's table names them. It is what rule and
// detector definitions are validated against, so it keeps unresolved-relative-date and
// thin-chunk, which only memory v1 counted, for v1's rules and definitions to still parse;
// the detectors v2 runs are V2Detectors.
var Detectors = []string{DetectUncitedAssistantSpecifics, DetectCrossScopeSupersede, DetectUnresolvedRelativeDate, DetectThinChunk, DetectDroppedThenQueried, DetectDecodeFailure}

// detectorDescriptions say what each kernel check counts.
var detectorDescriptions = map[string]string{
	DetectUncitedAssistantSpecifics: "assistant entries no note cites that add specifics (numbers, names, lists) the user's messages, the turn's memory results and the notes in memory did not give",
	DetectCrossScopeSupersede:       "supersedes links between notes whose scopes differ",
	DetectUnresolvedRelativeDate:    "relative-date phrases in new notes the renderer had to resolve",
	DetectThinChunk:                 "chunks whose notes are far shorter than their source with specifics left uncited",
	DetectDroppedThenQueried:        "records the dropper retired that a later memory query asked for",
	DetectDecodeFailure:             "rows that did not decode in the summary's codec",
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

// StageObserver is the stage memory v1's observer passes were recorded for, which JudgedKind
// still reads to list a v1-written definition's probation.
const StageObserver = "observer"

// ObserverPass is what the detectors read: under memory v2, one level-0 extraction (DetectV2).
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
	// (omhost.Records.Aliases).
	Aliases []ledger.SubjectAlias
	// Defined are the retired defined tools, by name, with their stored queries: a call of one
	// on the branch is a memory query too (MemoryAsks).
	Defined map[string]string
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
	if name == omtool.ToolName {
		return true
	}
	_, ok := defined[name]
	return ok && name != ""
}

// isRole reports whether entry is a message of role.
func isRole(entry ledger.Entry, role string) bool {
	return entry.Type == "message" && entry.Message != nil && entry.Message["role"] == role
}

// DroppedThenQueried are the dropped records the memory tool calls in covered asked for: a
// recall of a record the dropper had retired before the call, or a search whose words all
// appear in such a record. Each record counts once per call, of the memory tool or of a tool
// the agent defined (defined, by name). Only calls in the covered entries are read, so a call
// is counted by the one pass that covered it. A code call is read from its result's details
// (the ids it recalled and the words it searched, omtool.Details); a call written before the
// code tool, and a defined tool's call, from its query (MemoryAsks).
func DroppedThenQueried(branch, covered []ledger.Entry, defined map[string]string) []ledger.Observation {
	inCovered := map[string]bool{}
	for _, e := range covered {
		inCovered[e.ID] = true
	}
	results := memoryResults(branch)
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
		for _, ask := range MemoryAsks(entry, defined, results) {
			asked := map[string]bool{}
			for _, id := range ask.Recalled {
				if _, dropped := droppedAt[id]; dropped {
					asked[id] = true
				}
			}
			for _, search := range ask.Searches {
				words := strings.Fields(strings.ToLower(search))
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

// memoryResults are the details of the memory tool's results on branch, by tool call id.
func memoryResults(branch []ledger.Entry) map[string]any {
	out := map[string]any{}
	for _, e := range branch {
		if !isRole(e, "toolResult") {
			continue
		}
		if name, _ := e.Message["toolName"].(string); name != omtool.ToolName {
			continue
		}
		if id, _ := e.Message["toolCallId"].(string); id != "" {
			out[id] = e.Message["details"]
		}
	}
	return out
}

// Ask is what one memory call asked for: the ids it recalled and the words it searched.
type Ask struct {
	Recalled []string
	Searches []string
}

// MemoryAsks are what the memory tool calls an assistant entry made asked for, and its calls
// of the tools the agent defined (defined maps a defined tool's name to its stored query). A
// code call is read from its result's details (results, by tool call id: omtool.Details'
// recalled and searches); a call written before the code tool parses its query with the frozen
// recognizer, and a defined tool's call its stored query with the call's input substituted, as
// the tool ran it. A write asks for nothing.
func MemoryAsks(entry ledger.Entry, defined map[string]string, results map[string]any) []Ask {
	if entry.Type != "message" || entry.Message == nil || entry.Message["role"] != "assistant" {
		return nil
	}
	blocks, _ := entry.Message["content"].([]any)
	out := []Ask{}
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok || block["type"] != "toolCall" {
			continue
		}
		name, _ := block["name"].(string)
		args, _ := block["arguments"].(map[string]any)
		query := ""
		if name == omtool.ToolName {
			if _, isCode, old := omtool.CallArgs(args); isCode {
				id, _ := block["id"].(string)
				if d := omtool.DetailsFrom(results[id]); d != nil && (len(d.Recalled) > 0 || len(d.Searches) > 0) {
					out = append(out, Ask{Recalled: d.Recalled, Searches: d.Searches})
				}
				continue
			} else {
				query = old
			}
		} else if stored, ok := defined[name]; ok {
			input, _ := args[legacyquery.InputParameter].(string)
			query = legacyquery.Substitute(stored, input)
		}
		if ask, ok := legacyAsk(query); ok {
			out = append(out, ask)
		}
	}
	return out
}

// legacyAsk is what a query of the removed language asked for, false for a write or a query
// that does not parse.
func legacyAsk(query string) (Ask, bool) {
	if strings.TrimSpace(query) == "" {
		return Ask{}, false
	}
	q, err := legacyquery.Parse(query)
	if err != nil || q.Write != "" {
		return Ask{}, false
	}
	var ask Ask
	if q.Source == legacyquery.SourceRecall {
		ask.Recalled = append(ask.Recalled, q.ID)
	}
	for _, stage := range q.Stages {
		if stage.Kind == legacyquery.StageSearch {
			ask.Searches = append(ask.Searches, stage.Words)
		}
	}
	return ask, true
}

// SourceTokens is the estimated tokens of entries: a pass's observed source tokens.
func SourceTokens(entries []ledger.Entry) int {
	total := 0
	for _, e := range entries {
		total += tokens.EstimateEntryTokens(e)
	}
	return total
}
