package rules

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// The remedy catalogue (docs/memory-interface.md, section 7). It is closed: a rule names one
// of these, and nothing else is applied.
const (
	// RemedyPromptLine adds the rule's text as a line of the worker's system prompt for the
	// stage its detector judges.
	RemedyPromptLine = "prompt-line"
	// RemedyRequireKind re-asks the observer once for a note of the rule's kind on the entries
	// uncited-assistant-specifics flagged.
	RemedyRequireKind = "require-kind"
	// RemedyKeepHead keeps the opening of a flagged assistant entry as a low-relevance note,
	// only for entries the chunk's coverage boundary covers.
	RemedyKeepHead = "keep-head"
	// RemedyBlockLink drops the supersedes links cross-scope-supersede flags.
	RemedyBlockLink = "block-link"
	// RemedyProtectKind exempts a record kind from dropping while the pool's fullness is at
	// or below the rule's.
	RemedyProtectKind = "protect-kind"
	// RemedySplitChunk halves the observer chunk for the model.
	RemedySplitChunk = "split-chunk"
	// RemedySwitchCodec makes the profile switch codec, as section 6's decode evidence would.
	RemedySwitchCodec = "switch-codec"
	// RemedyLongSpec makes the profile carry the long spec, as section 6's syntax evidence would.
	RemedyLongSpec = "long-spec"
)

// Remedies lists the catalogue.
var Remedies = []string{RemedyPromptLine, RemedyRequireKind, RemedyKeepHead, RemedyBlockLink, RemedyProtectKind, RemedySplitChunk, RemedySwitchCodec, RemedyLongSpec}

// answers maps each remedy to the detectors it can answer: a remedy acts on what one detector
// flags, so it is refused for a detector whose findings it never touches. prompt-line answers
// any detector.
var answers = map[string][]string{
	RemedyPromptLine:  Detectors,
	RemedyRequireKind: {DetectUncitedAssistantSpecifics},
	RemedyKeepHead:    {DetectUncitedAssistantSpecifics},
	RemedyBlockLink:   {DetectCrossScopeSupersede},
	RemedyProtectKind: {DetectDroppedThenQueried},
	RemedySplitChunk:  {DetectThinChunk, DetectUncitedAssistantSpecifics},
	RemedySwitchCodec: {DetectDecodeFailure},
	RemedyLongSpec:    {DetectDecodeFailure},
}

// Catalogue is the remedies the harness tries for each detector, in order: when a rule for a
// detector is retired for a model and the count keeps rising, the next one is tried. A
// model can propose any remedy that answers the detector.
var Catalogue = map[string][]string{
	DetectUncitedAssistantSpecifics: {RemedyRequireKind, RemedyKeepHead},
	DetectCrossScopeSupersede:       {RemedyBlockLink, RemedyPromptLine},
	DetectUnresolvedRelativeDate:    {RemedyPromptLine},
	DetectThinChunk:                 {RemedySplitChunk, RemedyPromptLine},
	DetectDroppedThenQueried:        {RemedyProtectKind},
	DetectDecodeFailure:             {RemedySwitchCodec, RemedyLongSpec},
}

// catalogueText is the line a harness prompt-line rule carries for each detector. Rule text
// comes only from here or from a model's own propose call, never from a file, a web page or
// tool output.
var catalogueText = map[string]string{
	DetectUncitedAssistantSpecifics: "Record every concrete thing the assistant produced (names, numbers, lists, recommendations) as a kind=assistant note citing its entry.",
	DetectCrossScopeSupersede:       "Never set supersedes between notes whose scopes differ; a figure with another scope is an addition.",
	DetectUnresolvedRelativeDate:    "Write the absolute date a relative phrase refers to, computed from the message timestamp, instead of the phrase alone.",
	DetectThinChunk:                 "Keep the specifics of the chunk (numbers, names, dates) in the notes; a short chunk summary that drops them is not enough.",
	DetectDroppedThenQueried:        "Do not drop notes whose details the assistant still asks for.",
	DetectDecodeFailure:             "Write the rows table exactly in the format the user message describes, or pass the observations array instead.",
}

// stageOf is the worker stage a detector judges: where a prompt-line answering it is read.
func stageOf(detector string) string {
	if detector == DetectDroppedThenQueried {
		return StageDropper
	}
	return StageObserver
}

// StageOfRemedy is the worker stage a remedy definition acts in, which is the stage whose pass
// proves it once held (docs/memory-interface.md, section 8): a rule acts where its detector
// is judged (a protect-kind or prompt-line rule for dropped-then-queried acts in the dropper),
// and a catalogue whose every entry answers only dropper detectors acts in the dropper; every
// other remedy definition, and one that does not parse, in the observer.
func StageOfRemedy(d layer.Definition) string {
	if d.Kind != layer.KindRemedy {
		return StageObserver
	}
	if containsString(Remedies, d.Name) {
		entries, err := ParseCatalogue(d.Name, d.Text)
		if err != nil || len(entries) == 0 {
			return StageObserver
		}
		for _, e := range entries {
			if stageOf(e.Detector) != StageDropper {
				return StageObserver
			}
		}
		return StageDropper
	}
	if r, err := parse(d.Kind+"/"+d.Name, d.Text); err == nil {
		return stageOf(r.Detector)
	}
	return StageObserver
}

// ActsOnProfile reports whether remedy changes a model's profile (its codec or spec) rather
// than what its memory-worker passes record: the profile also shapes the memory tool's calls.
func ActsOnProfile(remedy string) bool {
	return remedy == RemedySwitchCodec || remedy == RemedyLongSpec
}

// Answers reports whether remedy can answer detector.
func Answers(remedy, detector string) bool { return containsString(answers[remedy], detector) }

// Check refuses a rule whose remedy is outside the catalogue, does not answer its detector, or
// has parameters that would break an invariant or leave it nothing to do:
//
//   - prompt-line needs one line of at most MaxTextChars characters;
//   - require-kind needs a known kind (default assistant);
//   - protect-kind needs a known kind or Unclassified and a fullness above 0 and at most
//     agents.DropFloorMaxFullness: above it memory pressure wins (the pool must stay bounded,
//     or the summary the room budget caps would be crowded by records no drop can remove),
//     the same bound the dropper's specifics floor respects.
//
// It returns the rule with its parameters normalized.
func Check(r Rule) (Rule, error) {
	if !containsString(Detectors, r.Detector) {
		return r, fmt.Errorf("unknown detector %q; detectors are %s", r.Detector, strings.Join(Detectors, ", "))
	}
	if !containsString(Remedies, r.Remedy) {
		return r, fmt.Errorf("unknown remedy %q; the catalogue is %s", r.Remedy, strings.Join(Remedies, ", "))
	}
	if !Answers(r.Remedy, r.Detector) {
		return r, fmt.Errorf("%s does not answer %s; it answers %s", r.Remedy, r.Detector, strings.Join(answers[r.Remedy], ", "))
	}
	text := strings.Join(strings.Fields(r.Text), " ")
	if r.Remedy == RemedyPromptLine {
		if text == "" {
			return r, fmt.Errorf("prompt-line needs text=\"the line to add\"")
		}
		if n := utf8.RuneCountInString(text); n > MaxTextChars {
			return r, fmt.Errorf("a rule's text is one line of at most %d characters (this one has %d)", MaxTextChars, n)
		}
		r.Text = text
	} else if text != "" {
		return r, fmt.Errorf("only prompt-line takes text")
	}
	switch r.Remedy {
	case RemedyRequireKind:
		if r.Kind == "" {
			r.Kind = string(kindAssistant)
		}
		if !ledger.IsObservationKind(r.Kind) {
			return r, fmt.Errorf("kind %q must be one of %s", r.Kind, kindList())
		}
	case RemedyProtectKind:
		if r.Kind != Unclassified && !ledger.IsObservationKind(r.Kind) {
			return r, fmt.Errorf("kind %q must be one of %s or %s", r.Kind, kindList(), Unclassified)
		}
		if r.Fullness == 0 {
			r.Fullness = agents.DropFloorMaxFullness
		}
		if r.Fullness < 0 || r.Fullness > agents.DropFloorMaxFullness {
			return r, fmt.Errorf("refused: protect-kind fullness %s would keep the pool from being pruned past %s times its target, where memory pressure must win so the memory stays bounded", formatFullness(r.Fullness), formatFullness(agents.DropFloorMaxFullness))
		}
	default:
		if r.Kind != "" || r.Fullness != 0 {
			return r, fmt.Errorf("%s takes no kind or fullness", r.Remedy)
		}
	}
	return r, nil
}

const kindAssistant ledger.ObservationKind = "assistant"

func kindList() string {
	out := make([]string, 0, len(ledger.ObservationKindValues))
	for _, k := range ledger.ObservationKindValues {
		out = append(out, string(k))
	}
	return strings.Join(out, ", ")
}

// harnessRule is the catalogue rule the harness wrote for detector with remedy before the
// catalogue was data; kinds are the kinds the evidence flagged (protect-kind protects the most
// frequent one). The seed catalogue (CatalogueSeeds) declares exactly these parameters, and
// Catalog.Rule, which the harness now uses, reproduces them from it.
func harnessRule(detector, remedy string, kinds map[string]int) Rule {
	r := Rule{Detector: detector, Remedy: remedy, Proposer: ProposerHarness}
	switch remedy {
	case RemedyPromptLine:
		r.Text = catalogueText[detector]
	case RemedyRequireKind:
		r.Kind = string(kindAssistant)
	case RemedyProtectKind:
		r.Kind, r.Fullness = mostFrequent(kinds), agents.DropFloorMaxFullness
	}
	return r
}

func mostFrequent(counts map[string]int) string {
	best, bestN := Unclassified, 0
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}

// --- applying the rules ------------------------------------------------------------

// Active is the rules that apply to one model: those on probation for it, established for it,
// and the defaults it is not excluded from.
type Active struct {
	Rules []Rule
}

// ActiveFor is the rules of all that apply to model.
func ActiveFor(all []Rule, model string) Active {
	a := Active{}
	if model == "" {
		return a
	}
	for _, r := range all {
		if r.Active() && r.AppliesTo(model) {
			a.Rules = append(a.Rules, r)
		}
	}
	return a
}

// Has reports whether a rule with remedy applies.
func (a Active) Has(remedy string) bool {
	for _, r := range a.Rules {
		if r.Remedy == remedy {
			return true
		}
	}
	return false
}

// PromptLines are the prompt-line texts for stage's worker, in rule order, each once.
func (a Active) PromptLines(stage string) []string {
	out := []string{}
	for _, r := range a.Rules {
		if r.Remedy == RemedyPromptLine && stageOf(r.Detector) == stage && !containsString(out, r.Text) {
			out = append(out, r.Text)
		}
	}
	return out
}

// RequiredKind is the kind a require-kind rule asks for, and whether one applies.
func (a Active) RequiredKind() (string, bool) {
	for _, r := range a.Rules {
		if r.Remedy == RemedyRequireKind {
			return r.Kind, true
		}
	}
	return "", false
}

// SplitChunk is the observer chunk cap for the model: halved when a split-chunk rule applies
// (once, however many do), never below 1 token, so the serializer still takes the first entry
// (as an excerpt when it is larger) and coverage keeps moving.
func (a Active) SplitChunk(maxTokens int) int {
	if !a.Has(RemedySplitChunk) || maxTokens <= 0 {
		return maxTokens
	}
	return max(1, maxTokens/2)
}

// RequireInstruction is the catalogue text of a require-kind re-ask over the flagged entries.
func RequireInstruction(kind string, flagged []string) string {
	return "A memory rule asks for one more look at these source entries, which no observation cites although they carry specifics: " + strings.Join(flagged, ", ") +
		". Record a kind=" + kind + " observation for each one that has something worth remembering, with its distinguishing details in the content and citing only these ids. Record nothing for an entry with nothing worth keeping."
}

// KeepHeadChars bounds a keep-head note's content: the window the memory tool's history source
// shows of an entry (memquery.HistoryClipChars), so a kept head reads like a history row and
// recall <id> still reads the entry whole.
const KeepHeadChars = memquery.HistoryClipChars

// KeepHead is the keep-head remedy: a low-relevance kind=assistant note holding the opening of
// each flagged assistant entry, citing it. Only entries at or before coversUpToID in the chunk
// order get one: a note citing an entry past the coverage boundary would be recorded by a
// marker that does not cover its source, so those are refused and returned. An entry that
// already has a note of that content gets none. The caller applies it only to a chunk whose
// observer recorded something: a deliberately empty verdict stays empty.
func KeepHead(branch []ledger.Entry, flagged, chunkOrder []string, coversUpToID string, existing []ledger.Observation) (kept []ledger.Observation, refused []string) {
	boundary := -1
	position := map[string]int{}
	for i, id := range chunkOrder {
		position[id] = i
		if id == coversUpToID {
			boundary = i
		}
	}
	byID := ledger.EntryIndexByID(branch)
	seen := map[string]bool{}
	for _, o := range existing {
		seen[o.ID] = true
	}
	for _, id := range flagged {
		pos, inChunk := position[id]
		if !inChunk || pos > boundary {
			refused = append(refused, id)
			continue
		}
		index, ok := byID[id]
		if !ok {
			continue
		}
		text, ok := AssistantText(branch[index])
		if !ok {
			continue
		}
		head := opening(text)
		if head == "" {
			continue
		}
		content := serialize.TruncateRecordContent("Assistant said: " + head)
		obsID := ids.HashID(content)
		if seen[obsID] {
			continue
		}
		seen[obsID] = true
		timestamp := serialize.FormatTimestamp(branch[index].Timestamp)
		if strings.HasPrefix(timestamp, "?") {
			timestamp = serialize.NowTimestamp()
		}
		kept = append(kept, ledger.Observation{
			ID: obsID, Content: content, Timestamp: timestamp, Relevance: "low", Kind: kindAssistant, SourceEntryIDs: []string{id},
			TokenCount: tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: obsID, Timestamp: timestamp, Relevance: "low", Content: content}),
		})
	}
	return kept, refused
}

// opening is the start of text up to its first paragraph that carries specifics, whitespace
// collapsed, clipped to KeepHeadChars characters.
func opening(text string) string {
	paragraphs := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	taken := []string{}
	for _, p := range paragraphs {
		p = strings.Join(strings.Fields(p), " ")
		if p == "" {
			continue
		}
		taken = append(taken, p)
		if ledger.HasSpecifics(p) {
			break
		}
	}
	head := strings.Join(taken, " ")
	if r := []rune(head); len(r) > KeepHeadChars {
		head = strings.TrimSpace(string(r[:KeepHeadChars])) + "…"
	}
	return head
}

// BlockLinks is the block-link remedy: it removes from notes every supersedes link
// CrossScopeSupersedes flags against known, and returns how many it removed. It only removes
// links, so a blocked link leaves both records active.
func BlockLinks(notes, known []ledger.Observation) ([]ledger.Observation, int) {
	flagged := map[string]bool{}
	for _, link := range CrossScopeSupersedes(notes, known) {
		flagged[link] = true
	}
	if len(flagged) == 0 {
		return notes, 0
	}
	out := make([]ledger.Observation, len(notes))
	blocked := 0
	for i, o := range notes {
		var keep []string
		for _, target := range o.Supersedes {
			if flagged[o.ID+">"+target] {
				blocked++
				continue
			}
			keep = append(keep, target)
		}
		o.Supersedes = keep
		out[i] = o
	}
	return out, blocked
}

// Protect is the protect-kind remedy: it removes from dropped the records of every protected
// kind while the pool's fullness is at or below that rule's fullness, and returns the drops
// that remain and the ids it kept. Above the fullness memory pressure wins.
func (a Active) Protect(dropped []string, pool []ledger.Observation, fullness float64) ([]string, []string) {
	protected := map[string]bool{}
	for _, r := range a.Rules {
		if r.Remedy == RemedyProtectKind && fullness <= r.Fullness {
			protected[r.Kind] = true
		}
	}
	if len(protected) == 0 {
		return dropped, nil
	}
	kinds := map[string]string{}
	for _, o := range pool {
		kinds[o.ID] = kindName(o.Kind)
	}
	remaining, kept := []string{}, []string{}
	for _, id := range dropped {
		if kind, ok := kinds[id]; ok && protected[kind] {
			kept = append(kept, id)
			continue
		}
		remaining = append(remaining, id)
	}
	return remaining, kept
}

// ParseFullness reads a protect-kind fullness parameter.
func ParseFullness(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("fullness %q must be a positive number, the pool's active tokens over its target (e.g. 1.5)", s)
	}
	return f, nil
}
