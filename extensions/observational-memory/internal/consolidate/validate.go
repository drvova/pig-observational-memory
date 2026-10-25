package consolidate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// Input is what validation checks the level-1 output against: exactly what the prompt showed
// (Prompt), plus the ledger, which resolves supersede chains and ids already recorded.
type Input struct {
	// Candidates, Active and Reflections are the records and reflections the prompt showed.
	Candidates  []ledger.Observation
	Active      []ledger.Observation
	Reflections []ledger.Reflection
	// Ledger is the folded ledger (supersede links of records not shown, recorded ids and
	// aliases).
	Ledger ledger.FoldedLedger
	// Subjects are the subjects present in the memory, which aliases must name.
	Subjects []string
	// Links are the possible updates the prompt listed (Prompt.Links): the only pairs a verdict
	// may name.
	Links []Link
	// Curated are the curated memories the prompt listed (Prompt.Curated): the only ones a
	// citation may name.
	Curated []CuratedMemory
	// Branch is the current branch, which tells a record of the user's own words (a citation's
	// support) by its first source entry.
	Branch []ledger.Entry
	// Offer is what the prompt offered to define (Prompt.HarnessShown with the request's
	// DefinitionOffer); nil refuses every definition and rule.
	Offer *DefinitionOffer
}

// Link is a possible update (ledger.Observation.PossiblyUpdates) the level-1 input lists: Later
// may give a new value of what Earlier states. The input lists a link only when it shows both
// records.
type Link struct {
	Later, Earlier string
}

// Refusal is one proposal validation refused, with why.
type Refusal struct {
	// What is "record", "merge", "reflection", "alias", "link", "curated", "definition" or
	// "rule", Index its position in that list.
	What   string
	Index  int
	Reason string
}

// The Refusal.What of the harness offer's answers: a definition or a rule level 1 proposed.
const (
	RefusalDefinition = "definition"
	RefusalRule       = "rule"
)

// The refusal reasons.
const (
	ReasonForeignID      = "cites an id outside its input"
	ReasonNotCandidate   = "labels a record that is not a candidate"
	ReasonUsed           = "names a record already labelled or merged"
	ReasonTooFew         = "a merge needs two or more records"
	ReasonEmpty          = "has no text"
	ReasonDropsSpecifics = "drops specifics of what it replaces"
	ReasonCycle          = "would close a supersede cycle"
	ReasonCrossScope     = "supersedes a record with a different scope"
	ReasonMergeScopes    = "merges records with different scopes"
	ReasonSuperseded     = "names a record another proposal supersedes"
	ReasonSelf           = "supersedes itself"
	ReasonNoSupport      = "cites no record whose specifics it carries"
	ReasonMalformed      = "is not an object"
	ReasonCodeKind       = "merges an attempt, file, commit or note record, whose fields code keeps"
	ReasonMergeStatuses  = "merges records with different statuses (proposed, hypothetical, retracted or stated as fact)"
	ReasonNotLink        = "names a pair the input did not list as a possible update"
	ReasonVerdict        = "has no verdict: confirm or reject"
	ReasonLinkSettled    = "settles a possible update another verdict already settled"
	ReasonLinkMerged     = "settles a possible update between records its merge joins"
	ReasonStatusReplace  = "replaces a record of another status (a plan or condition never replaces a fact, a retraction only a plan or condition)"
)

// The failure reasons of an output nothing is recorded from.
const (
	FailureNoCall    = "the response did not call " + ToolName
	FailureNothing   = "the output accounts for nothing"
	FailureNoneValid = "the output has no valid record"
)

// Result is what of the level-1 output is recorded.
type Result struct {
	// Records are the new records: labelled candidates (keeping the candidate's origin, since
	// their text is the candidate's, unless the label changed the kind) and merges (origin
	// model, FromUser when every source stood for the user's correction or preference).
	Records []ledger.Observation
	// Merges is how many of Records are merges.
	Merges int
	// Replaced maps each record a new record replaces to that record's id; those records are
	// dropped (Union decides finally).
	Replaced map[string]string
	// Reflections are the new reflections.
	Reflections []ledger.Reflection
	// Aliases are the accepted subject aliases.
	Aliases []ledger.SubjectAlias
	// Unchanged counts labels that changed nothing: they account for their candidate.
	Unchanged int
	// Confirmed and Rejected count the possible updates settled: a confirmed one became a
	// supersede, a rejected one was removed. LinkRecords are the records of Records that only
	// settle links (the later record with its links settled, replacing it).
	Confirmed, Rejected, LinkRecords int
	// Cited are the accepted curated citations (Citation).
	Cited []Citation
	// Definitions and Rules are the structurally valid harness proposals; the hook holds each
	// through the layer's checks (DefinitionOffer).
	Definitions []Definition
	Rules       []RuleProposal
	Refusals    []Refusal
	// Failure is set when nothing is recorded: the output is a failure and level 0 stands.
	Failure string
}

// Accounted is how many proposals were accepted.
func (r Result) Accounted() int {
	return len(r.Records) + r.Unchanged + len(r.Reflections) + len(r.Aliases) + len(r.Cited) + len(r.Definitions) + len(r.Rules)
}

// validator holds one validation's state.
type validator struct {
	in          Input
	shown       map[string]ledger.Observation // candidates and active records, by id
	candidates  map[string]bool
	reflections map[string]ledger.Reflection
	used        map[string]bool
	replaced    map[string]string
	superseded  map[string]bool     // ids an accepted proposal supersedes
	edges       map[string][]string // supersede links, by record id, new records included
	verdicts    []verdict           // the links verdicts, in input order
	result      Result
}

// verdict is one valid links item, waiting for the record that stands for its later record.
type verdict struct {
	index   int
	link    Link
	confirm bool
	settled bool
}

// Validate checks the level-1 tool arguments against what the input showed (docs/memory-v2.md,
// level 1's "Validation"): a record or merge citing an id outside its input is refused and its
// records stand; a merge's sources are set by code to the union of its records' sources; a
// merge, a supersede or a replacing reflection whose text drops a specific of what it replaces
// (dropsSpecifics) is refused; a record is labelled or merged once, and never after another
// proposal superseded it, nor superseded after another proposal replaced it; supersede cycles,
// cross-scope supersedes and merges of records with different scopes are refused; a
// reflection cites only records whose specifics it carries, read from the record that will
// stand for each when it is rendered. Output that accounts for nothing, or has no valid
// proposal, is a failure. Every new record takes its sources' times.
func Validate(args map[string]any, in Input) Result {
	if args == nil {
		return Result{Failure: FailureNoCall}
	}
	v := &validator{
		in:          in,
		shown:       map[string]ledger.Observation{},
		candidates:  map[string]bool{},
		reflections: map[string]ledger.Reflection{},
		used:        map[string]bool{},
		replaced:    map[string]string{},
		superseded:  map[string]bool{},
		edges:       map[string][]string{},
	}
	for _, o := range in.Ledger.Observations {
		v.edges[o.ID] = o.Supersedes
	}
	for _, o := range in.Active {
		v.shown[o.ID] = o
		v.edges[o.ID] = o.Supersedes
	}
	for _, o := range in.Candidates {
		v.shown[o.ID] = o
		v.candidates[o.ID] = true
		v.edges[o.ID] = o.Supersedes
	}
	for _, r := range in.Reflections {
		v.reflections[r.ID] = r
	}
	merges, records, reflections, aliases := list(args["merges"]), list(args["records"]), list(args["reflections"]), list(args["aliases"])
	v.links(list(args["links"]))
	// Merges first, so a candidate a merge takes is not also labelled alone. A verdict on a
	// possible update is applied to the record that will stand for its later record: a merge or
	// a label taking it, else a record of its own.
	for i, item := range merges {
		v.merge(i, item)
	}
	for i, item := range records {
		v.label(i, item)
	}
	v.settleRemaining()
	for i, item := range reflections {
		v.reflection(i, item)
	}
	v.aliases(aliases)
	v.curated(list(args["curated"]))
	v.definitions(list(args["definitions"]))
	v.rules(list(args["rules"]))
	v.result.Replaced = v.replaced
	if v.result.Accounted() == 0 {
		v.result.Failure = FailureNothing
		if len(v.result.Refusals) > 0 {
			v.result.Failure = FailureNoneValid
		}
		v.result.Records, v.result.Reflections, v.result.Aliases, v.result.Replaced = nil, nil, nil, map[string]string{}
		v.result.Cited = nil
	}
	return v.result
}

func (v *validator) refuse(what string, index int, reason string) {
	v.result.Refusals = append(v.result.Refusals, Refusal{What: what, Index: index, Reason: reason})
}

// merge validates one merge.
func (v *validator) merge(index int, item any) {
	m, ok := item.(map[string]any)
	if !ok {
		v.refuse("merge", index, ReasonMalformed)
		return
	}
	sourceIDs := uniqueStrings(m["ids"])
	if len(sourceIDs) < 2 {
		v.refuse("merge", index, ReasonTooFew)
		return
	}
	sources := make([]ledger.Observation, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		o, ok := v.shown[id]
		if !ok {
			v.refuse("merge", index, ReasonForeignID)
			return
		}
		if v.used[id] {
			v.refuse("merge", index, ReasonUsed)
			return
		}
		if v.superseded[id] {
			v.refuse("merge", index, ReasonSuperseded)
			return
		}
		// A note is indexed by its name (Target), which a merged record does not keep, so a
		// later save of the note could no longer supersede it.
		if slices.Contains(ledger.CodeKindValues, o.Kind) {
			v.refuse("merge", index, ReasonCodeKind)
			return
		}
		sources = append(sources, o)
	}
	// Two records with different scopes never replace each other, and the merged record could
	// keep only one scope: which figure belongs to which qualifier would be lost.
	if commonScope(sources) == "" && scopeOf(sources) != "" {
		v.refuse("merge", index, ReasonMergeScopes)
		return
	}
	// A plan merged with a fact would be shown as done, and a retraction merged with anything
	// would withdraw it: records with different statuses never merge.
	for _, o := range sources[1:] {
		if o.Status != sources[0].Status {
			v.refuse("merge", index, ReasonMergeStatuses)
			return
		}
	}
	content := oneLine(m["content"])
	if content == "" {
		v.refuse("merge", index, ReasonEmpty)
		return
	}
	for _, o := range sources {
		if dropsSpecifics(o.Content, content) {
			v.refuse("merge", index, ReasonDropsSpecifics)
			return
		}
	}
	record := ledger.Observation{
		Content:   content,
		Timestamp: newestTimestamp(sources),
		Relevance: highestRelevance(sources),
		Kind:      commonKind(sources),
		Subject:   commonSubject(sources),
		Scope:     commonScope(sources),
		Origin:    ledger.OriginModel,
		Status:    sources[0].Status,
		EventDate: commonEventDate(sources),
	}
	// The sources' level-0 links stay on the merged record, except those between the sources.
	for _, o := range sources {
		for _, id := range o.PossiblyUpdates {
			if !slices.Contains(sourceIDs, id) && !slices.Contains(record.PossiblyUpdates, id) {
				record.PossiblyUpdates = append(record.PossiblyUpdates, id)
			}
		}
		for _, id := range o.Conflicts {
			if !slices.Contains(sourceIDs, id) && !slices.Contains(record.Conflicts, id) {
				record.Conflicts = append(record.Conflicts, id)
			}
		}
	}
	for _, o := range sources {
		for _, id := range o.SourceEntryIDs {
			if !slices.Contains(record.SourceEntryIDs, id) {
				record.SourceEntryIDs = append(record.SourceEntryIDs, id)
			}
		}
	}
	v.applyLabels(&record, m, false)
	// The text is the model's, so the record is; but when every source stood for the user's own
	// correction or preference, so does the merge, and it keeps the user's class in selection.
	// It is read from the sources, so a merge taking in any other record never claims it, and
	// only while its text adds no specific its sources do not give: a claim the model added is
	// not the user's.
	record.FromUser = len(ledger.AddedSpecifics(content, sourceContents(sources))) == 0
	for _, o := range sources {
		record.FromUser = record.FromUser && ledger.UserPriority(o)
	}
	targets, reason := v.supersedes(m["supersedes"], sourceIDs, record)
	if reason != "" {
		v.refuse("merge", index, reason)
		return
	}
	// What the sources replaced, the merged record replaces.
	for _, o := range sources {
		for _, id := range o.Supersedes {
			if !slices.Contains(sourceIDs, id) && !slices.Contains(targets, id) {
				targets = append(targets, id)
			}
		}
	}
	record.Supersedes = targets
	v.settle(&record, sourceIDs)
	v.accept(record, sourceIDs)
	v.result.Merges++
}

// label validates one candidate label.
func (v *validator) label(index int, item any) {
	m, ok := item.(map[string]any)
	if !ok {
		v.refuse("record", index, ReasonMalformed)
		return
	}
	id, _ := m["id"].(string)
	id = strings.TrimSpace(id)
	candidate, shown := v.shown[id]
	switch {
	case !shown:
		v.refuse("record", index, ReasonForeignID)
		return
	case !v.candidates[id]:
		v.refuse("record", index, ReasonNotCandidate)
		return
	case v.used[id]:
		v.refuse("record", index, ReasonUsed)
		return
	case v.superseded[id]:
		v.refuse("record", index, ReasonSuperseded)
		return
	}
	record := candidate
	record.ID, record.TokenCount = "", 0
	record.Supersedes = append([]string(nil), candidate.Supersedes...)
	v.applyLabels(&record, m, true)
	if raw, has := m["supersedes"]; has {
		targets, reason := v.supersedes(raw, []string{id}, record)
		if reason != "" {
			v.refuse("record", index, reason)
			return
		}
		for _, t := range targets {
			if !slices.Contains(record.Supersedes, t) {
				record.Supersedes = append(record.Supersedes, t)
			}
		}
	}
	v.settle(&record, []string{id})
	if sameLabels(record, candidate) {
		v.used[id] = true
		v.result.Unchanged++
		return
	}
	// The text is still the candidate's, so the record keeps its origin, and a user correction
	// or preference keeps its place in selection; a kind the model chose makes it the model's,
	// so relabelling a fact as a preference never claims a user's priority.
	if record.Kind != candidate.Kind {
		record.Origin = ledger.OriginModel
		record.FromUser = false
	}
	v.accept(record, []string{id})
}

// applyLabels sets the fields a label may set on record. A kind is set only from the kinds a
// model may write, and never on a record whose kind code set (attempt, file, commit, note) or
// on a user correction or preference (which selection ranks by that kind); an outcome only on
// an attempt; a subject as the kernel stores it (ledger.RecordableSubject).
func (v *validator) applyLabels(record *ledger.Observation, m map[string]any, label bool) {
	if kind, ok := m["kind"].(string); ok && ledger.IsObservationKind(kind) && !keepsKind(record.Kind) {
		record.Kind = ledger.ObservationKind(kind)
	}
	if relevance, ok := m["relevance"].(string); ok && ledger.IsRelevance(relevance) {
		record.Relevance = ledger.Relevance(relevance)
	}
	if subject, ok := m["subject"].(string); ok && strings.TrimSpace(subject) != "" {
		if stored, _ := ledger.RecordableSubject(subject); stored != "" {
			record.Subject = stored
		}
	}
	if scope, ok := m["scope"].(string); ok && strings.TrimSpace(scope) != "" {
		record.Scope = ledger.NormalizeScope(scope)
	}
	if outcome, ok := m["outcome"].(string); label && ok && record.Kind == ledger.KindAttempt {
		switch outcome {
		case ledger.OutcomeFailed, ledger.OutcomeWorked, ledger.OutcomeOpen:
			record.Outcome = outcome
		}
	}
}

// supersedes validates the ids a record replacing sources (with record's text and scope)
// supersedes: each must be shown, not one of the sources, not replaced by a proposal accepted
// before (its replacement would stand with the stale text), carry no specific the record
// drops, share its scope, and close no cycle. It returns them, or the reason the whole proposal is
// refused.
func (v *validator) supersedes(raw any, sources []string, record ledger.Observation) ([]string, string) {
	targets := uniqueStrings(raw)
	for _, t := range targets {
		target, ok := v.shown[t]
		switch {
		case !ok:
			return nil, ReasonForeignID
		case slices.Contains(sources, t):
			return nil, ReasonSelf
		case v.replaced[t] != "":
			return nil, ReasonUsed
		case record.Scope != "" && target.Scope != "" && record.Scope != target.Scope:
			return nil, ReasonCrossScope
		case !ledger.MaySupersede(record, target):
			return nil, ReasonStatusReplace
		case dropsSpecifics(target.Content, record.Content):
			return nil, ReasonDropsSpecifics
		case v.reaches(t, sources):
			return nil, ReasonCycle
		}
	}
	return targets, ""
}

// reaches reports whether a supersede chain from id, read through the ledger and the records
// accepted so far (a replaced record reads as its replacement), reaches any of targets.
func (v *validator) reaches(id string, targets []string) bool {
	seen := map[string]bool{}
	queue := []string{id}
	for len(queue) > 0 {
		next := v.resolve(queue[0])
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		for _, t := range targets {
			if next == t || next == v.resolve(t) {
				return true
			}
		}
		queue = append(queue, v.edges[next]...)
	}
	return false
}

// resolve is the record that now stands for id: its replacement, transitively.
func (v *validator) resolve(id string) string {
	for range len(v.replaced) + 1 {
		next, ok := v.replaced[id]
		if !ok {
			return id
		}
		id = next
	}
	return id
}

// accept records a new record replacing sources.
func (v *validator) accept(record ledger.Observation, sources []string) {
	record.ID = recordID(record)
	record.TokenCount = tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: record.ID, Timestamp: record.Timestamp, Relevance: string(record.Relevance), Content: record.Content})
	for _, id := range sources {
		v.used[id] = true
	}
	if _, held := v.in.Ledger.ObservationsByID[record.ID]; held {
		// The same record is already in the ledger: an earlier compaction recorded it, and its
		// sources were dropped then or stand for a reason that still holds.
		v.result.Unchanged++
		return
	}
	for _, id := range sources {
		v.replaced[id] = record.ID
	}
	for _, id := range record.Supersedes {
		v.superseded[id] = true
	}
	edges := append([]string(nil), record.Supersedes...)
	v.edges[record.ID] = edges
	v.result.Records = append(v.result.Records, record)
}

// reflection validates one reflection.
func (v *validator) reflection(index int, item any) {
	m, ok := item.(map[string]any)
	if !ok {
		v.refuse("reflection", index, ReasonMalformed)
		return
	}
	content := oneLine(m["content"])
	if content == "" {
		v.refuse("reflection", index, ReasonEmpty)
		return
	}
	// A rendered reflection drops the records it cites, and it cites the record that now stands
	// for each id (a merge or label replacing it), so each support is checked against that
	// record's text: one whose specifics the reflection does not carry is no support, and it
	// stays (the union rule).
	supporting := []string{}
	support := func(id string, known bool) {
		resolved := v.resolve(id)
		cited, found := v.record(resolved)
		if (!found && known) || (found && dropsSpecifics(cited.Content, content)) || slices.Contains(supporting, resolved) {
			return
		}
		supporting = append(supporting, resolved)
	}
	for _, id := range uniqueStrings(m["supportingIds"]) {
		if _, ok := v.shown[id]; !ok {
			v.refuse("reflection", index, ReasonForeignID)
			return
		}
		support(id, true)
	}
	superseded := uniqueStrings(m["supersedes"])
	for _, id := range superseded {
		old, ok := v.reflections[id]
		if !ok {
			v.refuse("reflection", index, ReasonForeignID)
			return
		}
		if dropsSpecifics(old.Content, content) {
			v.refuse("reflection", index, ReasonDropsSpecifics)
			return
		}
		// The replaced reflection's supports pass the same check; one the ledger no longer
		// holds drops nothing, so it is kept as cited.
		for _, s := range old.SupportingObservationIDs {
			support(s, false)
		}
	}
	if len(supporting) == 0 {
		v.refuse("reflection", index, ReasonNoSupport)
		return
	}
	id := ids.HashID(content)
	if slices.Contains(superseded, id) {
		v.refuse("reflection", index, ReasonSelf)
		return
	}
	if _, held := v.in.Ledger.ReflectionsByID[id]; held {
		v.result.Unchanged++
		return
	}
	for _, r := range v.result.Reflections {
		if r.ID == id {
			v.result.Unchanged++
			return
		}
	}
	reflection := ledger.Reflection{ID: id, Content: content, SupportingObservationIDs: supporting, Supersedes: superseded}
	reflection.TokenCount = tokens.EstimateStringTokens(ledger.ReflectionToSummaryLine(ledger.Reflection{ID: id, Content: content}))
	v.result.Reflections = append(v.result.Reflections, reflection)
}

// links reads the verdicts on possible updates: each names a pair the input listed (Input.Links),
// once, with confirm or reject. They wait for the record that stands for their later record
// (settle).
func (v *validator) links(items []any) {
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			v.refuse("link", i, ReasonMalformed)
			continue
		}
		later, _ := m["id"].(string)
		earlier, _ := m["updates"].(string)
		link := Link{Later: strings.TrimSpace(later), Earlier: strings.TrimSpace(earlier)}
		word, _ := m["verdict"].(string)
		word = strings.TrimSpace(word)
		switch {
		case !slices.Contains(v.in.Links, link):
			v.refuse("link", i, ReasonNotLink)
			continue
		case word != VerdictConfirm && word != VerdictReject:
			v.refuse("link", i, ReasonVerdict)
			continue
		}
		if slices.ContainsFunc(v.verdicts, func(d verdict) bool { return d.link == link }) {
			v.refuse("link", i, ReasonLinkSettled)
			continue
		}
		v.verdicts = append(v.verdicts, verdict{index: i, link: link, confirm: word == VerdictConfirm})
	}
}

// settle applies the verdicts on the possible updates of sources (the records record replaces)
// to record: a confirmed one becomes a supersede of the earlier record, when it may be one
// (confirmable), and a rejected one is removed; either way the link leaves record's
// PossiblyUpdates. A verdict that cannot apply is refused and its link stays unsettled.
func (v *validator) settle(record *ledger.Observation, sources []string) {
	for i := range v.verdicts {
		d := &v.verdicts[i]
		if d.settled || !slices.Contains(sources, d.link.Later) {
			continue
		}
		d.settled = true
		if slices.Contains(sources, d.link.Earlier) {
			v.refuse("link", d.index, ReasonLinkMerged)
			continue
		}
		if !slices.Contains(record.PossiblyUpdates, d.link.Earlier) {
			v.refuse("link", d.index, ReasonNotLink)
			continue
		}
		if d.confirm {
			if reason := v.confirmable(*record, sources, d.link.Earlier); reason != "" {
				v.refuse("link", d.index, reason)
				continue
			}
			if !slices.Contains(record.Supersedes, d.link.Earlier) {
				record.Supersedes = append(record.Supersedes, d.link.Earlier)
			}
			v.result.Confirmed++
		} else {
			v.result.Rejected++
		}
		record.PossiblyUpdates = slices.DeleteFunc(slices.Clone(record.PossiblyUpdates), func(id string) bool { return id == d.link.Earlier })
		if len(record.PossiblyUpdates) == 0 {
			record.PossiblyUpdates = nil
		}
	}
}

// confirmable is why record (replacing sources) may not supersede earlier on a confirmed
// possible update, "" when it may: earlier not replaced by a proposal accepted before, the same
// scope, a status that may replace earlier's (ledger.MaySupersede: a planned or conditional
// value never replaces a stated one), no supersede cycle, and every specific of earlier carried except the values the
// update replaces (dropsSpecificsBesidesUpdate). Code already matched the subject, the scope and
// the specific's type when it linked them; the old value stays recallable.
func (v *validator) confirmable(record ledger.Observation, sources []string, earlier string) string {
	target := v.shown[earlier]
	switch {
	case v.replaced[earlier] != "":
		return ReasonUsed
	case record.Scope != "" && target.Scope != "" && record.Scope != target.Scope:
		return ReasonCrossScope
	case !ledger.MaySupersede(record, target):
		return ReasonStatusReplace
	case dropsSpecificsBesidesUpdate(target.Content, record.Content):
		return ReasonDropsSpecifics
	case v.reaches(earlier, sources):
		return ReasonCycle
	}
	return ""
}

// settleRemaining gives each later record whose verdicts no merge or label took a record of its
// own: the later record as it stands, with its links settled, replacing it. Its text, kind and
// origin are the later record's. A later record another proposal supersedes takes no verdict.
func (v *validator) settleRemaining() {
	for i := range v.verdicts {
		d := v.verdicts[i]
		if d.settled {
			continue
		}
		later := d.link.Later
		if v.used[later] || v.superseded[later] {
			for j := range v.verdicts {
				if !v.verdicts[j].settled && v.verdicts[j].link.Later == later {
					v.verdicts[j].settled = true
					v.refuse("link", v.verdicts[j].index, ReasonSuperseded)
				}
			}
			continue
		}
		record := v.shown[later]
		record.ID, record.TokenCount = "", 0
		record.Supersedes = slices.Clone(record.Supersedes)
		record.PossiblyUpdates = slices.Clone(record.PossiblyUpdates)
		before := v.result.Confirmed + v.result.Rejected
		v.settle(&record, []string{later})
		if v.result.Confirmed+v.result.Rejected == before {
			continue
		}
		records := len(v.result.Records)
		v.accept(record, []string{later})
		if len(v.result.Records) > records {
			v.result.LinkRecords++
		}
	}
}

// aliases validates the proposed aliases with the kernel's alias check
// (ledger.AcceptNewSubjectAliases) over the subjects present, the new records' included.
func (v *validator) aliases(items []any) {
	proposed := []ledger.SubjectAlias{}
	positions := []int{}
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			v.refuse("alias", i, ReasonMalformed)
			continue
		}
		from, _ := m["from"].(string)
		to, _ := m["to"].(string)
		reason, _ := m["reason"].(string)
		proposed = append(proposed, ledger.SubjectAlias{From: strings.TrimSpace(from), To: strings.TrimSpace(to), Reason: oneLine(reason)})
		positions = append(positions, i)
	}
	if len(proposed) == 0 {
		return
	}
	subjects := append(append([]string{}, v.in.Subjects...), ledger.SubjectsOf(v.result.Records)...)
	accepted, refused := ledger.AcceptNewSubjectAliases(subjects, v.in.Ledger.Aliases, proposed)
	for _, r := range refused {
		index := -1
		for i, a := range proposed {
			if a == r.Alias {
				index = positions[i]
				break
			}
		}
		v.refuse("alias", index, r.Reason)
	}
	if len(accepted) > ledger.MaxSubjectAliasesPerEntry {
		accepted = accepted[:ledger.MaxSubjectAliasesPerEntry]
	}
	v.result.Aliases = accepted
}

// Union applies the union rule (docs/memory-v2.md, level 1's "Union"): a record is dropped
// only when the record replacing it carries every specific it carries, and a new reflection
// cites a record (which its rendering drops) only when it carries every specific of that
// record, so any record carrying specifics level 1 does not account for is kept. records are
// the active records (candidates included). It returns the ids to drop, in the order of
// records, the reflections to record (each without the supports it does not carry, and none
// left with no support), and how many replacements and supports it kept back. Validation
// already refuses or leaves out each of these; Union checks them again on what is about to be
// recorded, so no path past validation loses a fact.
func Union(result Result, records []ledger.Observation) (drop []string, reflections []ledger.Reflection, keptBack int) {
	byID := make(map[string]ledger.Observation, len(result.Records)+len(records))
	for _, o := range records {
		byID[o.ID] = o
	}
	for _, r := range result.Records {
		byID[r.ID] = r
	}
	for _, o := range records {
		replacement, ok := result.Replaced[o.ID]
		if !ok {
			continue
		}
		r, ok := byID[replacement]
		if !ok || replacement == o.ID || dropsSpecifics(o.Content, r.Content) {
			keptBack++
			continue
		}
		if !slices.Contains(drop, o.ID) {
			drop = append(drop, o.ID)
		}
	}
	for _, r := range result.Reflections {
		supporting := make([]string, 0, len(r.SupportingObservationIDs))
		for _, id := range r.SupportingObservationIDs {
			// A support that is not an active record drops nothing when the reflection renders.
			if o, active := byID[id]; active && dropsSpecifics(o.Content, r.Content) {
				keptBack++
				continue
			}
			supporting = append(supporting, id)
		}
		if len(supporting) == 0 {
			continue
		}
		r.SupportingObservationIDs = supporting
		reflections = append(reflections, r)
	}
	return drop, reflections, keptBack
}

// dropsSpecificsBesidesUpdate is dropsSpecifics for a confirmed possible update: later may drop
// the values of earlier it updates (ledger.UpdatedSpecifics), and no other specific.
func dropsSpecificsBesidesUpdate(earlier, later string) bool {
	updated := ledger.UpdatedSpecifics(later, earlier)
	for _, s := range ledger.AddedSpecifics(earlier, []string{later}) {
		if !updated[s] {
			return true
		}
	}
	return false
}

// dropsSpecifics reports whether replacement drops a specific of source (ledger.Specifics):
// one replacement does not give as a whole item (ledger.AddedSpecifics), never a substring, so
// "30" is not carried by "300" nor "Go" by "Google". It is level 1's one specifics check, for
// merges, supersedes, reflections and the union rule.
func dropsSpecifics(source, replacement string) bool {
	return len(ledger.AddedSpecifics(source, []string{replacement})) > 0
}

// record is the record id names: a new record of this output, else a shown record, else one
// the ledger holds.
func (v *validator) record(id string) (ledger.Observation, bool) {
	for _, r := range v.result.Records {
		if r.ID == id {
			return r, true
		}
	}
	if o, ok := v.shown[id]; ok {
		return o, true
	}
	o, ok := v.in.Ledger.ObservationsByID[id]
	return o, ok
}

// keepsKind reports whether a label leaves kind as it is: a kind code set (CodeKindValues), or
// a user correction or preference, which selection ranks by its kind.
func keepsKind(kind ledger.ObservationKind) bool {
	return slices.Contains(ledger.CodeKindValues, kind) || kind == "correction" || kind == "preference"
}

// RefusalSummary is the refusals as one line, for the debug event and the status line.
func RefusalSummary(refusals []Refusal) string {
	parts := make([]string, 0, len(refusals))
	for _, r := range refusals {
		parts = append(parts, fmt.Sprintf("%s %d: %s", r.What, r.Index, r.Reason))
	}
	return strings.Join(parts, "; ")
}

// recordID is a level-1 record's id: the hash of everything it states, so a label that changes
// a field is a new record, and recording the same label again gives the same id. The unsettled
// possible updates count only when there are any, so a record without them keeps the id it had
// before links existed.
func recordID(o ledger.Observation) string {
	parts := []string{"level1", o.Content, strings.Join(o.SourceEntryIDs, ","), string(o.Kind), string(o.Relevance), o.Subject, o.Scope, strings.Join(o.Supersedes, ","), o.Outcome}
	if len(o.PossiblyUpdates) > 0 {
		parts = append(parts, "possiblyUpdates", strings.Join(o.PossiblyUpdates, ","))
	}
	return ids.HashID(strings.Join(parts, "\x00"))
}

// sameLabels reports whether a label changed none of the fields a label sets.
func sameLabels(a, b ledger.Observation) bool {
	return a.Kind == b.Kind && a.Relevance == b.Relevance && a.Subject == b.Subject && a.Scope == b.Scope && a.Outcome == b.Outcome && slices.Equal(a.Supersedes, b.Supersedes) && slices.Equal(a.PossiblyUpdates, b.PossiblyUpdates)
}

// list is v as a JSON array, nil otherwise.
func list(v any) []any {
	out, _ := v.([]any)
	return out
}

// uniqueStrings are the distinct non-empty trimmed strings of a JSON array, in order.
func uniqueStrings(v any) []string {
	out := []string{}
	for _, item := range list(v) {
		s, ok := item.(string)
		if s = strings.TrimSpace(s); ok && s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// oneLine is a string value with its whitespace collapsed to single spaces: records and
// reflections are one line.
func oneLine(v any) string {
	s, _ := v.(string)
	return strings.Join(strings.Fields(s), " ")
}

// newestTimestamp is the latest timestamp of records ("YYYY-MM-DD HH:MM" sorts as text).
func newestTimestamp(records []ledger.Observation) string {
	newest := ""
	for _, o := range records {
		if o.Timestamp > newest {
			newest = o.Timestamp
		}
	}
	return newest
}

// highestRelevance is the highest relevance among records, "low" when none is set.
func highestRelevance(records []ledger.Observation) ledger.Relevance {
	best := 0
	for _, o := range records {
		if i := slices.Index(ledger.RelevanceValues, o.Relevance); i > best {
			best = i
		}
	}
	return ledger.RelevanceValues[best]
}

// commonKind is the kind every record shares, else "".
func commonKind(records []ledger.Observation) ledger.ObservationKind {
	kind := records[0].Kind
	for _, o := range records[1:] {
		if o.Kind != kind {
			return ""
		}
	}
	return kind
}

// sourceContents are the records' texts.
func sourceContents(records []ledger.Observation) []string {
	out := make([]string, 0, len(records))
	for _, o := range records {
		out = append(out, o.Content)
	}
	return out
}

// commonEventDate is the one event date the records name between them, else "": a record naming
// none adds no date, and two different dates, or a record whose text names dates but no one date
// (ledger.NamesNoOneDate: several dates, or a week, a month or a year), leave none.
func commonEventDate(records []ledger.Observation) string {
	date := ""
	for _, o := range records {
		if ledger.NamesNoOneDate(o) {
			return ""
		}
		if o.EventDate == "" || o.EventDate == date {
			continue
		}
		if date != "" {
			return ""
		}
		date = o.EventDate
	}
	return date
}

// commonSubject is the subject every record shares, else "".
func commonSubject(records []ledger.Observation) string {
	subject := records[0].Subject
	for _, o := range records[1:] {
		if o.Subject != subject {
			return ""
		}
	}
	return subject
}

// commonScope is the one non-empty scope the scoped records share (an unscoped record shares
// any scope), else "".
func commonScope(records []ledger.Observation) string {
	scope := scopeOf(records)
	for _, o := range records {
		if o.Scope != "" && o.Scope != scope {
			return ""
		}
	}
	return scope
}

// scopeOf is the first non-empty scope among records, else "".
func scopeOf(records []ledger.Observation) string {
	for _, o := range records {
		if o.Scope != "" {
			return o.Scope
		}
	}
	return ""
}
