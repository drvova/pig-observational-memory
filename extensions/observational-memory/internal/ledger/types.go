// Package ledger folds the V3 memory ledger recorded on a session branch.
package ledger

import (
	"math"
	"regexp"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// The V3 custom entry types.
const (
	OMObservationsRecorded = "om.observations.recorded"
	OMReflectionsRecorded  = "om.reflections.recorded"
	OMObservationsDropped  = "om.observations.dropped"
	OMFolded               = "om.folded"
	// OMSubjectsAliased records subject aliases the reflector declared (SubjectsAliasedData). It
	// carries no coverage marker.
	OMSubjectsAliased = "om.subjects.aliased"
	// OMExtracted is memory v2's extraction marker (ExtractedData): level-0 extraction read
	// every source entry through its coversUpToId and found nothing to record after the last
	// om.observations.recorded entry it appended. It carries no records.
	OMExtracted = "om.extracted"
)

// Entry is a session entry.
type Entry = session.Entry

// Relevance rates an observation.
type Relevance string

// RelevanceValues are the valid relevance levels, in rank order.
var RelevanceValues = []Relevance{"low", "medium", "high", "critical"}

// MemoryIDPattern matches a memory record id.
var MemoryIDPattern = regexp.MustCompile(`^[a-f0-9]{12}$`)

// ObservationKind classifies an observation.
type ObservationKind string

// ObservationKindValues are the valid observation kinds. "assistant" marks
// something the assistant produced: a list, a recommendation, a number, a name.
var ObservationKindValues = []ObservationKind{"fact", "event", "preference", "plan", "correction", "assistant"}

// The kinds only memory v2's level-0 extraction writes (CodeKindValues).
const (
	// KindAttempt is one tool call and its outcome (Observation.Outcome).
	KindAttempt ObservationKind = "attempt"
	// KindFile is one path at its newest state (Observation.Target holds the path).
	KindFile ObservationKind = "file"
	// KindCommit is one git commit (Observation.Target holds the hash).
	KindCommit ObservationKind = "commit"
	// KindNote is a note the session model wrote with the memory tool.
	KindNote ObservationKind = "note"
)

// CodeKindValues are the kinds level-0 extraction writes beside ObservationKindValues. They
// are read back from the ledger (ObservationFrom) but are not kinds a worker or a note schema
// may write, so v1's prompts and validation are unchanged.
var CodeKindValues = []ObservationKind{KindAttempt, KindFile, KindCommit, KindNote}

// The origins of a record (Observation.Origin).
const (
	// OriginCode is a record level-0 extraction picked from the session by code.
	OriginCode = "code"
	// OriginModel is a record the session model wrote: a memory tool note, or a record memory
	// v2's level-1 consolidation request labelled or merged at a compaction.
	OriginModel = "model"
	// OriginHost is a compaction summary the ledger does not back (a native PiG summary, or one
	// from before install), stored once as one record citing that compaction's entry when memory
	// v2 compacts after it (docs/memory-v2.md, "Migration > Records and boundaries").
	OriginHost = "host"
)

// The outcomes of an attempt record (Observation.Outcome).
const (
	// OutcomeFailed is a call whose result was an error or a nonzero exit.
	OutcomeFailed = "failed"
	// OutcomeWorked is a call that succeeded, or a failed or open call whose identical command,
	// or the same tool on the same path, later succeeded.
	OutcomeWorked = "worked"
	// OutcomeOpen is a call with no result on the branch.
	OutcomeOpen = "open"
)

// Observation is a timestamped event distilled from the conversation. Kind,
// Subject, Scope and Supersedes are optional structure; records written before
// they existed leave them empty, and the id stays the hash of Content.
type Observation struct {
	ID             string    `json:"id"`
	Content        string    `json:"content"`
	Timestamp      string    `json:"timestamp"`
	Relevance      Relevance `json:"relevance"`
	SourceEntryIDs []string  `json:"sourceEntryIds"`
	TokenCount     int       `json:"tokenCount"`
	// Kind is one of ObservationKindValues, or "" when unclassified.
	Kind ObservationKind `json:"kind,omitempty"`
	// Subject is a short lowercase noun phrase grouping related records.
	Subject string `json:"subject,omitempty"`
	// Scope is the qualifier that tells two figures apart; records with
	// different scopes never replace each other.
	Scope string `json:"scope,omitempty"`
	// Supersedes holds the ids of earlier records this one replaces; empty
	// means the record adds rather than replaces.
	Supersedes []string `json:"supersedes,omitempty"`
	// Fields are the values of the fields the writing model's own note schema adds
	// (NoteSchema), by field name; none for a record written under the seed schema.
	Fields map[string]string `json:"fields,omitempty"`
	// Origin is who wrote the record under memory v2 (OriginCode, OriginModel, OriginHost); "" for a
	// record a v1 worker wrote.
	Origin string `json:"origin,omitempty"`
	// FromUser marks a level-1 merge whose sources all stood for the user's own corrections or
	// preferences (UserPriority): its text is the model's (Origin model), its basis the user's,
	// so selection keeps its user class. Code sets it from the sources, never from the model's
	// output.
	FromUser bool `json:"fromUser,omitempty"`
	// Outcome is an attempt record's outcome (OutcomeFailed, OutcomeWorked, OutcomeOpen).
	Outcome string `json:"outcome,omitempty"`
	// Tool is the tool an attempt record's call used.
	Tool string `json:"tool,omitempty"`
	// Target is what a code record is about: an attempt's command or path, a file record's
	// path, a commit's hash, a note's name.
	Target string `json:"target,omitempty"`
	// EventDate is the one absolute date (YYYY-MM-DD) the record's own relative date phrases
	// resolve to (EventDate), apart from Timestamp, when it was said; "" when the record names
	// none, or more than one. Memory v2's level 0 sets it.
	EventDate string `json:"eventDate,omitempty"`
	// Status is how the record was stated (StatusValues): planned, conditional, or a retraction
	// of the records it supersedes; "" means stated as fact. Memory v2's level 0 sets it from
	// fixed wording.
	Status string `json:"status,omitempty"`
	// PossiblyUpdates are the ids of earlier records on the same subject and scope whose value
	// of one specific type this record gives differently (UpdatesSpecific): code cannot tell an
	// update from a second value of another property, so the link never drops anything.
	PossiblyUpdates []string `json:"possiblyUpdates,omitempty"`
	// Conflicts are the ids of earlier records on the same subject sharing a specific that
	// exactly one side negates (NegationConflict). It never drops or supersedes anything.
	Conflicts []string `json:"conflicts,omitempty"`
	// Replaces are the ids of the records a level-1 record stands for (memory v2): a label's or a
	// settled possible update's one record, a merge's sources. Links to those records are read
	// as links to this one (Standing). Code sets it; none for every other record.
	Replaces []string `json:"replaces,omitempty"`
}

// The statuses of a record (Observation.Status), which memory v2's level 0 reads from fixed
// wording (docs/memory-v2.md, "Status by explicit construction").
const (
	// StatusProposed is a record stated as a plan: not done.
	StatusProposed = "proposed"
	// StatusHypothetical is a record stated under a condition: not known to hold.
	StatusHypothetical = "hypothetical"
	// StatusRetracted is an explicit retraction: the records it supersedes are withdrawn, and
	// read as retracted (RetractedIDs).
	StatusRetracted = "retracted"
)

// StatusValues are the valid record statuses.
var StatusValues = []string{StatusProposed, StatusHypothetical, StatusRetracted}

// UserPriority reports whether a record stands for the user's own correction or preference,
// which memory v2's selection takes before other records (docs/memory-v2.md, "Selection", class
// 2): a correction or preference no model wrote (level 0 picks them from user messages only),
// or a level-1 merge of only such records (FromUser). A kind a model chose never claims it.
//
// A retraction (StatusRetracted) never holds it: it withdraws other records rather than giving a
// value of its own.
func UserPriority(o Observation) bool {
	if o.Status == StatusRetracted {
		return false
	}
	if o.FromUser {
		return true
	}
	return (o.Kind == "correction" || o.Kind == "preference") && o.Origin != OriginModel
}

// Reflection is a durable conclusion distilled from observations.
type Reflection struct {
	ID                       string   `json:"id"`
	Content                  string   `json:"content"`
	SupportingObservationIDs []string `json:"supportingObservationIds"`
	TokenCount               int      `json:"tokenCount"`
	// Supersedes holds the ids of earlier reflections this one replaces (memory v2's level 1,
	// ActiveReflections); empty for a reflection that adds rather than replaces, and for every
	// reflection a v1 worker wrote.
	Supersedes []string `json:"supersedes,omitempty"`
}

// ObservationsRecordedData is an om.observations.recorded payload.
type ObservationsRecordedData struct {
	Observations []Observation `json:"observations"`
	CoversUpToID string        `json:"coversUpToId"`
	// Consolidated marks the records memory v2's level 1 wrote at a compaction: what they say
	// was judged then, not when their sources were said, so the point-in-time view holds them
	// only from where they were recorded (ConsolidatedObservationIDs).
	Consolidated bool `json:"consolidated,omitempty"`
}

// ReflectionsRecordedData is an om.reflections.recorded payload.
type ReflectionsRecordedData struct {
	Reflections  []Reflection `json:"reflections"`
	CoversUpToID string       `json:"coversUpToId"`
}

// ObservationsDroppedData is an om.observations.dropped payload.
type ObservationsDroppedData struct {
	ObservationIDs []string `json:"observationIds"`
	CoversUpToID   string   `json:"coversUpToId"`
	// Consolidated marks memory v2's level-1 drops of the records its batch at the same entry
	// replaced: only these confirm that batch's replacements (replace.go).
	Consolidated bool `json:"consolidated,omitempty"`
}

// MemoryDetails is the details of a compaction entry this extension wrote.
type MemoryDetails struct {
	Type         string        `json:"type"`
	Version      int           `json:"version"`
	FullFold     bool          `json:"fullFold"`
	Observations []Observation `json:"observations"`
	Reflections  []Reflection  `json:"reflections"`
	// FoldThroughEntryID is the entry through which this summary folded the
	// ledger. It differs from the compaction's firstKeptEntryId when the hook
	// moved the cut to retain unobserved source.
	FoldThroughEntryID string `json:"foldThroughEntryId,omitempty"`
	// SummaryCodec is the codec the summary's tables were encoded in; "" for a
	// lines summary or one written before the codec was recorded.
	SummaryCodec string `json:"summaryCodec,omitempty"`
	// SummaryFormat is that codec's declaration (tables.Format.Declaration)
	// when a model declared it, so the summary decodes in the codec that wrote
	// it whatever the layer holds later; "" for a native codec.
	SummaryFormat string `json:"summaryFormat,omitempty"`
	// Aliases are the subject aliases folded through the boundary (Projection.Aliases), so
	// the compacted memory keeps grouping as the ledger did; none for a memory without any.
	Aliases []SubjectAlias `json:"aliases,omitempty"`
	// Engine is the memory engine that wrote the compaction ("v2"); "" for v1. A v2
	// compaction keeps PiG's cut, so its fold boundary is its firstKeptEntryId.
	Engine string `json:"engine,omitempty"`
	// Consolidation is what memory v2's level-1 request did at this compaction, nil for v1.
	Consolidation *ConsolidationReport `json:"consolidation,omitempty"`
	// Shown are the ids of the records a memory v2 summary shows (selection.Result), nil for
	// v1 and for a v2 summary that rendered no record. A v2 summary shows records its lines
	// carry no record id for (the files and commits lines) and prints ids it does not show
	// (pointers such as (conflicts <id>)), so the harness reads what the summary showed from
	// here, not from the text (harness.VisibilityOf).
	Shown []string `json:"shown,omitempty"`
}

// ConsolidationReport is what memory v2's level-1 consolidation request did at one compaction
// (docs/memory-v2.md, "Level 1"), as /om:status shows it.
type ConsolidationReport struct {
	// Requested is set when the request was made (counted even when it failed or was aborted).
	Requested bool `json:"requested"`
	// Applied is set when its output was validated and recorded: the compaction is level 1.
	Applied bool `json:"applied"`
	// Reason is why no request was made, or why the request failed; "" when it was applied.
	Reason string `json:"reason,omitempty"`
	// Records, Merges, Reflections and Aliases count what was recorded; Dropped the records
	// the recorded ones replaced; Refused the proposals validation refused.
	Records     int `json:"records,omitempty"`
	Merges      int `json:"merges,omitempty"`
	Reflections int `json:"reflections,omitempty"`
	Aliases     int `json:"aliases,omitempty"`
	Dropped     int `json:"dropped,omitempty"`
	Refused     int `json:"refused,omitempty"`
	// Confirmed and Rejected count the possible updates level 1 settled: a confirmed one became a
	// supersede, a rejected one was removed.
	Confirmed int `json:"confirmed,omitempty"`
	Rejected  int `json:"rejected,omitempty"`
	// Cited counts the curated memories level 1 cited (evidence for a staged one), and Promoted
	// the staged memories the citations made live.
	Cited    int `json:"cited,omitempty"`
	Promoted int `json:"promoted,omitempty"`
	// HarnessOffered is set when the request was shown changed harness evidence and could define
	// (docs/memory-v2.md, "Self-built harness"), and Defined counts the definitions and rules it
	// held, each until a later compaction proves it.
	HarnessOffered bool `json:"harnessOffered,omitempty"`
	Defined        int  `json:"defined,omitempty"`
}

// Level is the compaction's level: 1 when the level-1 output was applied, else 0.
func (r *ConsolidationReport) Level() int {
	if r != nil && r.Applied {
		return 1
	}
	return 0
}

// IsRelevance reports whether v is a relevance level.
func IsRelevance(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, r := range RelevanceValues {
		if string(r) == s {
			return true
		}
	}
	return false
}

// isCodeKind reports whether v is one of CodeKindValues.
func isCodeKind(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, k := range CodeKindValues {
		if string(k) == s {
			return true
		}
	}
	return false
}

// IsObservationKind reports whether v is an observation kind.
func IsObservationKind(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, k := range ObservationKindValues {
		if string(k) == s {
			return true
		}
	}
	return false
}

// NormalizeSubject trims and lowercases a subject and folds runs of
// whitespace, so records about one subject group together. It is how a stored
// subject is read, so a subject recorded before subject paths reads back as it
// was written ("ci / cd" stays "ci / cd"); the observer's record normalizes a
// written path further (NormalizeSubjectPath, RecordableSubject).
func NormalizeSubject(subject string) string {
	return normalizeSubjectSegment(subject)
}

// NormalizeSubjectPath is NormalizeSubject for a subject written as a path
// (SubjectPathSeparator): each segment is normalized and empty segments are
// dropped ("Running / half  marathon/" is "running/half marathon"). A subject
// without a separator normalizes as NormalizeSubject does.
func NormalizeSubjectPath(subject string) string {
	if !strings.Contains(subject, SubjectPathSeparator) {
		return normalizeSubjectSegment(subject)
	}
	segments := []string{}
	for _, segment := range strings.Split(subject, SubjectPathSeparator) {
		if segment = normalizeSubjectSegment(segment); segment != "" {
			segments = append(segments, segment)
		}
	}
	return strings.Join(segments, SubjectPathSeparator)
}

func normalizeSubjectSegment(segment string) string {
	return strings.ToLower(strings.Join(strings.Fields(segment), " "))
}

// NormalizeScope trims a scope and folds runs of whitespace, so scopes compare
// equal however they were spaced when written.
func NormalizeScope(scope string) string {
	return strings.Join(strings.Fields(scope), " ")
}

// IsNonEmptyString reports a non-empty string value.
func IsNonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && s != ""
}

// IsNonEmptyStringArray reports a non-empty array of non-empty strings.
func IsNonEmptyStringArray(v any) bool {
	_, ok := nonEmptyStrings(v)
	return ok
}

func nonEmptyStrings(v any) ([]string, bool) {
	switch list := v.(type) {
	case []string:
		if len(list) == 0 {
			return nil, false
		}
		for _, s := range list {
			if s == "" {
				return nil, false
			}
		}
		return append([]string(nil), list...), true
	case []any:
		if len(list) == 0 {
			return nil, false
		}
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// IsMemoryID reports a valid memory record id.
func IsMemoryID(v any) bool {
	s, ok := v.(string)
	return ok && MemoryIDPattern.MatchString(s)
}

func tokenCount(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return 0, false
		}
		return int(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// ObservationFrom validates a decoded observation.
func ObservationFrom(v any) (Observation, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return Observation{}, false
	}
	count, okCount := tokenCount(m["tokenCount"])
	ids, okIDs := nonEmptyStrings(m["sourceEntryIds"])
	if !IsMemoryID(m["id"]) || !IsNonEmptyString(m["content"]) || !IsNonEmptyString(m["timestamp"]) || !IsRelevance(m["relevance"]) || !okIDs || !okCount {
		return Observation{}, false
	}
	observation := Observation{
		ID:             m["id"].(string),
		Content:        m["content"].(string),
		Timestamp:      m["timestamp"].(string),
		Relevance:      Relevance(m["relevance"].(string)),
		SourceEntryIDs: ids,
		TokenCount:     count,
	}
	// The structured fields are optional and read leniently: a malformed one
	// is cleared rather than discarding the record it annotates.
	if IsObservationKind(m["kind"]) || isCodeKind(m["kind"]) {
		observation.Kind = ObservationKind(m["kind"].(string))
	}
	observation.Origin, _ = m["origin"].(string)
	observation.Outcome, _ = m["outcome"].(string)
	observation.FromUser, _ = m["fromUser"].(bool)
	observation.Tool, _ = m["tool"].(string)
	observation.Target, _ = m["target"].(string)
	if subject, ok := m["subject"].(string); ok {
		observation.Subject = NormalizeSubject(subject)
	}
	if scope, ok := m["scope"].(string); ok {
		observation.Scope = NormalizeScope(scope)
	}
	if date, ok := m["eventDate"].(string); ok && isISODate(date) {
		observation.EventDate = date
	}
	if status, ok := m["status"].(string); ok && slicesContains(StatusValues, status) {
		observation.Status = status
	}
	observation.PossiblyUpdates = observation.linkIDs(m["possiblyUpdates"])
	observation.Conflicts = observation.linkIDs(m["conflicts"])
	observation.Replaces = observation.linkIDs(m["replaces"])
	// Each supersedes element is checked on its own, so one bad element drops
	// only itself.
	switch list := m["supersedes"].(type) {
	case []string:
		for _, id := range list {
			observation.addSupersedes(id)
		}
	case []any:
		for _, id := range list {
			observation.addSupersedes(id)
		}
	}
	// A schema's own fields are kept when they are strings under field names a schema can
	// declare; the schema checked them when the note was recorded.
	if fields, ok := m["fields"].(map[string]any); ok {
		for name, raw := range fields {
			if value, ok := raw.(string); ok && value != "" && schemaFieldName.MatchString(name) {
				if observation.Fields == nil {
					observation.Fields = map[string]string{}
				}
				observation.Fields[name] = value
			}
		}
	}
	return observation, true
}

// addSupersedes appends id to Supersedes when it is a memory id other than
// the record's own and not already listed.
func (observation *Observation) addSupersedes(v any) {
	id, ok := v.(string)
	if !ok || !IsMemoryID(id) || id == observation.ID {
		return
	}
	for _, existing := range observation.Supersedes {
		if existing == id {
			return
		}
	}
	observation.Supersedes = append(observation.Supersedes, id)
}

// linkIDs reads a list of record ids leniently, as addSupersedes reads supersedes: an element
// that is not a memory id, the record's own id, or a repeat drops only itself; nil for none.
func (observation *Observation) linkIDs(v any) []string {
	var out []string
	add := func(raw any) {
		id, ok := raw.(string)
		if ok && IsMemoryID(id) && id != observation.ID && !slicesContains(out, id) {
			out = append(out, id)
		}
	}
	switch list := v.(type) {
	case []string:
		for _, id := range list {
			add(id)
		}
	case []any:
		for _, id := range list {
			add(id)
		}
	}
	return out
}

// isISODate reports a YYYY-MM-DD date.
func isISODate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i != 4 && i != 7 && (s[i] < '0' || s[i] > '9') {
			return false
		}
	}
	return true
}

// IsObservation reports a valid decoded observation.
func IsObservation(v any) bool {
	_, ok := ObservationFrom(v)
	return ok
}

// ReflectionFrom validates a decoded reflection.
func ReflectionFrom(v any) (Reflection, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return Reflection{}, false
	}
	count, okCount := tokenCount(m["tokenCount"])
	ids, okIDs := nonEmptyStrings(m["supportingObservationIds"])
	content, _ := m["content"].(string)
	if !IsMemoryID(m["id"]) || content == "" || strings.ContainsAny(content, "\r\n") || !okIDs || !okCount {
		return Reflection{}, false
	}
	reflection := Reflection{
		ID:                       m["id"].(string),
		Content:                  content,
		SupportingObservationIDs: ids,
		TokenCount:               count,
	}
	// Read leniently, like an observation's supersedes: a bad element drops only itself.
	if list, ok := m["supersedes"].([]any); ok {
		for _, raw := range list {
			if id, ok := raw.(string); ok && IsMemoryID(id) && id != reflection.ID && !slicesContains(reflection.Supersedes, id) {
				reflection.Supersedes = append(reflection.Supersedes, id)
			}
		}
	}
	return reflection, true
}

// IsReflection reports a valid decoded reflection.
func IsReflection(v any) bool {
	_, ok := ReflectionFrom(v)
	return ok
}

func observationList(v any, requireNonEmpty bool) ([]Observation, bool) {
	list, ok := v.([]any)
	if !ok || (requireNonEmpty && len(list) == 0) {
		return nil, false
	}
	out := make([]Observation, 0, len(list))
	for _, item := range list {
		observation, ok := ObservationFrom(item)
		if !ok {
			return nil, false
		}
		out = append(out, observation)
	}
	return out, true
}

func reflectionList(v any, requireNonEmpty bool) ([]Reflection, bool) {
	list, ok := v.([]any)
	if !ok || (requireNonEmpty && len(list) == 0) {
		return nil, false
	}
	out := make([]Reflection, 0, len(list))
	for _, item := range list {
		reflection, ok := ReflectionFrom(item)
		if !ok {
			return nil, false
		}
		out = append(out, reflection)
	}
	return out, true
}

// ObservationsRecordedDataFrom validates a decoded om.observations.recorded payload.
func ObservationsRecordedDataFrom(v any) (ObservationsRecordedData, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return ObservationsRecordedData{}, false
	}
	observations, ok := observationList(m["observations"], true)
	if !ok || !IsNonEmptyString(m["coversUpToId"]) {
		return ObservationsRecordedData{}, false
	}
	consolidated, _ := m["consolidated"].(bool)
	return ObservationsRecordedData{Observations: observations, CoversUpToID: m["coversUpToId"].(string), Consolidated: consolidated}, true
}

// IsObservationsRecordedData reports a valid decoded payload.
func IsObservationsRecordedData(v any) bool {
	_, ok := ObservationsRecordedDataFrom(v)
	return ok
}

// ReflectionsRecordedDataFrom validates a decoded om.reflections.recorded payload.
func ReflectionsRecordedDataFrom(v any) (ReflectionsRecordedData, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return ReflectionsRecordedData{}, false
	}
	reflections, ok := reflectionList(m["reflections"], true)
	if !ok || !IsNonEmptyString(m["coversUpToId"]) {
		return ReflectionsRecordedData{}, false
	}
	return ReflectionsRecordedData{Reflections: reflections, CoversUpToID: m["coversUpToId"].(string)}, true
}

// IsReflectionsRecordedData reports a valid decoded payload.
func IsReflectionsRecordedData(v any) bool {
	_, ok := ReflectionsRecordedDataFrom(v)
	return ok
}

// ObservationsDroppedDataFrom validates a decoded om.observations.dropped payload.
func ObservationsDroppedDataFrom(v any) (ObservationsDroppedData, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return ObservationsDroppedData{}, false
	}
	ids, ok := nonEmptyStrings(m["observationIds"])
	if !ok || !IsNonEmptyString(m["coversUpToId"]) {
		return ObservationsDroppedData{}, false
	}
	consolidated, _ := m["consolidated"].(bool)
	return ObservationsDroppedData{ObservationIDs: ids, CoversUpToID: m["coversUpToId"].(string), Consolidated: consolidated}, true
}

// IsObservationsDroppedData reports a valid decoded payload.
func IsObservationsDroppedData(v any) bool {
	_, ok := ObservationsDroppedDataFrom(v)
	return ok
}

// MemoryDetailsFrom validates decoded compaction details this extension wrote.
func MemoryDetailsFrom(v any) (MemoryDetails, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return MemoryDetails{}, false
	}
	version, _ := m["version"].(float64)
	fullFold, okFold := m["fullFold"].(bool)
	observations, okObs := observationList(m["observations"], false)
	reflections, okRef := reflectionList(m["reflections"], false)
	foldThrough, foldPresent := m["foldThroughEntryId"]
	foldThroughID, okFoldThrough := foldThrough.(string)
	if foldThrough == nil {
		okFoldThrough = true
	}
	if m["type"] != OMFolded || version != 1 || !okFold || !okObs || !okRef || (foldPresent && !okFoldThrough) {
		return MemoryDetails{}, false
	}
	summaryCodec, _ := m["summaryCodec"].(string)
	summaryFormat, _ := m["summaryFormat"].(string)
	// Aliases are read leniently, like the structured fields: a malformed one is skipped
	// rather than discarding the memory it groups.
	aliases := subjectAliasList(m["aliases"])
	details := MemoryDetails{Type: OMFolded, Version: 1, FullFold: fullFold, Observations: observations, Reflections: reflections, FoldThroughEntryID: foldThroughID, SummaryCodec: summaryCodec, SummaryFormat: summaryFormat, Aliases: aliases}
	details.Engine, _ = m["engine"].(string)
	details.Consolidation = consolidationReportFrom(m["consolidation"])
	if shown, ok := m["shown"].([]any); ok {
		details.Shown = make([]string, 0, len(shown))
		for _, raw := range shown {
			if id, ok := raw.(string); ok {
				details.Shown = append(details.Shown, id)
			}
		}
	}
	return details, true
}

// IsMemoryDetails reports valid decoded memory details.
func IsMemoryDetails(v any) bool {
	_, ok := MemoryDetailsFrom(v)
	return ok
}

// ObservationsRecordedEntry reads a valid om.observations.recorded entry.
func ObservationsRecordedEntry(entry Entry) (ObservationsRecordedData, bool) {
	if entry.Type != "custom" || entry.CustomType != OMObservationsRecorded {
		return ObservationsRecordedData{}, false
	}
	return ObservationsRecordedDataFrom(entry.Data)
}

// ReflectionsRecordedEntry reads a valid om.reflections.recorded entry.
func ReflectionsRecordedEntry(entry Entry) (ReflectionsRecordedData, bool) {
	if entry.Type != "custom" || entry.CustomType != OMReflectionsRecorded {
		return ReflectionsRecordedData{}, false
	}
	return ReflectionsRecordedDataFrom(entry.Data)
}

// ObservationsDroppedEntry reads a valid om.observations.dropped entry.
func ObservationsDroppedEntry(entry Entry) (ObservationsDroppedData, bool) {
	if entry.Type != "custom" || entry.CustomType != OMObservationsDropped {
		return ObservationsDroppedData{}, false
	}
	return ObservationsDroppedDataFrom(entry.Data)
}

// IsObservationsRecordedEntry reports a valid om.observations.recorded entry.
func IsObservationsRecordedEntry(entry Entry) bool {
	_, ok := ObservationsRecordedEntry(entry)
	return ok
}

// IsReflectionsRecordedEntry reports a valid om.reflections.recorded entry.
func IsReflectionsRecordedEntry(entry Entry) bool {
	_, ok := ReflectionsRecordedEntry(entry)
	return ok
}

// IsObservationsDroppedEntry reports a valid om.observations.dropped entry.
func IsObservationsDroppedEntry(entry Entry) bool {
	_, ok := ObservationsDroppedEntry(entry)
	return ok
}

// ExtractedData is an om.extracted payload: level-0 extraction read every source entry
// through CoversUpToID.
type ExtractedData struct {
	CoversUpToID string `json:"coversUpToId"`
}

// ExtractedEntry reads a valid om.extracted entry.
func ExtractedEntry(entry Entry) (ExtractedData, bool) {
	if entry.Type != "custom" || entry.CustomType != OMExtracted {
		return ExtractedData{}, false
	}
	m, ok := entry.Data.(map[string]any)
	if !ok || !IsNonEmptyString(m["coversUpToId"]) {
		return ExtractedData{}, false
	}
	return ExtractedData{CoversUpToID: m["coversUpToId"].(string)}, true
}

// BuildObservationsRecordedData builds a payload, or nil when it would be empty.
func BuildObservationsRecordedData(observations []Observation, coversUpToID string) *ObservationsRecordedData {
	if len(observations) == 0 || coversUpToID == "" {
		return nil
	}
	return &ObservationsRecordedData{Observations: observations, CoversUpToID: coversUpToID}
}

// BuildReflectionsRecordedData builds a payload, or nil when it would be empty.
func BuildReflectionsRecordedData(reflections []Reflection, coversUpToID string) *ReflectionsRecordedData {
	if len(reflections) == 0 || coversUpToID == "" {
		return nil
	}
	return &ReflectionsRecordedData{Reflections: reflections, CoversUpToID: coversUpToID}
}

// BuildObservationsDroppedData builds a payload, or nil when it would be empty.
func BuildObservationsDroppedData(observationIDs []string, coversUpToID string) *ObservationsDroppedData {
	if len(observationIDs) == 0 || coversUpToID == "" {
		return nil
	}
	return &ObservationsDroppedData{ObservationIDs: observationIDs, CoversUpToID: coversUpToID}
}

// consolidationReportFrom reads a decoded ConsolidationReport leniently; nil when v is not one.
func consolidationReportFrom(v any) *ConsolidationReport {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	report := &ConsolidationReport{}
	report.Requested, _ = m["requested"].(bool)
	report.Applied, _ = m["applied"].(bool)
	report.Reason, _ = m["reason"].(string)
	count := func(key string) int {
		n, _ := tokenCount(m[key])
		return n
	}
	report.Records, report.Merges, report.Reflections = count("records"), count("merges"), count("reflections")
	report.Aliases, report.Dropped, report.Refused = count("aliases"), count("dropped"), count("refused")
	report.Confirmed, report.Rejected = count("confirmed"), count("rejected")
	report.Cited, report.Promoted = count("cited"), count("promoted")
	report.HarnessOffered, _ = m["harnessOffered"].(bool)
	report.Defined = count("defined")
	return report
}

// slicesContains reports whether list holds s.
func slicesContains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// ActiveReflections is reflections without the ones another reflection in the list supersedes
// (Reflection.Supersedes), in order: what memory v2 renders after its level 1 replaced a
// reflection. A supersede cycle leaves every reflection on it out only when something outside
// the cycle supersedes it; reflections that only supersede each other all stay.
func ActiveReflections(reflections []Reflection) []Reflection {
	superseded := map[string]bool{}
	byID := make(map[string]Reflection, len(reflections))
	for _, r := range reflections {
		byID[r.ID] = r
	}
	for _, r := range reflections {
		for _, id := range r.Supersedes {
			if id == r.ID {
				continue
			}
			// A reflection the superseded one supersedes back (directly) keeps both: neither
			// claim is the later word.
			if other, ok := byID[id]; ok && slicesContains(other.Supersedes, r.ID) {
				continue
			}
			superseded[id] = true
		}
	}
	out := make([]Reflection, 0, len(reflections))
	for _, r := range reflections {
		if !superseded[r.ID] {
			out = append(out, r)
		}
	}
	return out
}
