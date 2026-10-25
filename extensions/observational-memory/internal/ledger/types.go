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
}

// Reflection is a durable conclusion distilled from observations.
type Reflection struct {
	ID                       string   `json:"id"`
	Content                  string   `json:"content"`
	SupportingObservationIDs []string `json:"supportingObservationIds"`
	TokenCount               int      `json:"tokenCount"`
}

// ObservationsRecordedData is an om.observations.recorded payload.
type ObservationsRecordedData struct {
	Observations []Observation `json:"observations"`
	CoversUpToID string        `json:"coversUpToId"`
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
	if IsObservationKind(m["kind"]) {
		observation.Kind = ObservationKind(m["kind"].(string))
	}
	if subject, ok := m["subject"].(string); ok {
		observation.Subject = NormalizeSubject(subject)
	}
	if scope, ok := m["scope"].(string); ok {
		observation.Scope = NormalizeScope(scope)
	}
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
	return Reflection{
		ID:                       m["id"].(string),
		Content:                  content,
		SupportingObservationIDs: ids,
		TokenCount:               count,
	}, true
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
	return ObservationsRecordedData{Observations: observations, CoversUpToID: m["coversUpToId"].(string)}, true
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
	return ObservationsDroppedData{ObservationIDs: ids, CoversUpToID: m["coversUpToId"].(string)}, true
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
	return MemoryDetails{Type: OMFolded, Version: 1, FullFold: fullFold, Observations: observations, Reflections: reflections, FoldThroughEntryID: foldThroughID, SummaryCodec: summaryCodec, SummaryFormat: summaryFormat, Aliases: aliases}, true
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
