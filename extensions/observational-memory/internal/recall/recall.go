// Package recall implements the recall tool: exact evidence behind a memory id.
package recall

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// ToolName is the tool's name.
const ToolName = "recall"

// Label is the tool's human-readable name.
const Label = "Recall memory evidence"

// Description is the tool description sent to the model.
const Description = "Recover exact evidence and source context behind a compacted observational-memory observation or reflection id on the current branch. " +
	"Use when compressed memory is important and original source context is needed before acting."

// PromptSnippet is the tool's line in the system prompt's tool list.
const PromptSnippet = "Use recall(<id>) to recover exact source context behind compacted memory observations/reflections when precision matters."

// PromptGuidelines are the tool's system prompt guidelines.
var PromptGuidelines = []string{
	"Use recall before making an important decision that depends on a compacted observation or reflection whose details are unclear.",
	"Use recall when you need exact wording, rationale, file paths, commands, errors, commits, user constraints, or provenance behind a remembered claim.",
	"Use recall when a broad reflection is relevant but you need its supporting observations or raw sources to continue safely.",
	"Use recall when the user asks why you believe something, what supports a memory, or what was decided earlier.",
	"Do not use recall as semantic search or transcript browsing; you must already have a specific 12-character memory id.",
	"Do not recall every id preemptively. Recall only when exact source context will materially improve the next action.",
}

// Parameters is the tool's JSON schema.
var Parameters = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id": map[string]any{
			"type":        "string",
			"pattern":     "^[a-f0-9]{12}$",
			"description": "12-character lowercase hex observation or reflection id shown in compacted memory, /om:view, or a previous recall result. Must be a specific id; this tool does not search by topic.",
		},
	},
	"required": []any{"id"},
}

var memoryIDPattern = regexp.MustCompile(`^[a-f0-9]{12}$`)

// Status is the tool's aggregate status.
type Status string

// The statuses.
const (
	StatusOK                Status = "ok"
	StatusPartial           Status = "partial"
	StatusInvalidID         Status = "invalid_id"
	StatusNotFound          Status = "not_found"
	StatusNoSource          Status = "no_source"
	StatusSourceUnavailable Status = "source_unavailable"
)

// ObservationDetails describes a recalled observation.
type ObservationDetails struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Relevance string `json:"relevance"`
	Status    string `json:"status,omitempty"`
}

// ReflectionDetails describes a recalled reflection.
type ReflectionDetails struct {
	ID                       string   `json:"id"`
	Content                  string   `json:"content"`
	SupportingObservationIDs []string `json:"supportingObservationIds"`
	ReflectionIndex          int      `json:"reflectionIndex"`
}

// SourceEntryDetails describes one source entry.
type SourceEntryDetails struct {
	ID         string   `json:"id"`
	Origin     string   `json:"origin"`
	Timestamp  string   `json:"timestamp"`
	Tokens     int      `json:"tokens"`
	Qualifiers []string `json:"qualifiers"`
	Content    string   `json:"content,omitempty"`
}

// MatchDetails describes one observation match and its sources.
type MatchDetails struct {
	Status                 string               `json:"status"`
	ObservationEntryID     string               `json:"observationEntryId"`
	ObservationRecordIndex int                  `json:"observationRecordIndex"`
	Observation            ObservationDetails   `json:"observation"`
	SourceEntryIDs         []string             `json:"sourceEntryIds"`
	SourceEntries          []SourceEntryDetails `json:"sourceEntries"`
	MissingSourceEntryIDs  []string             `json:"missingSourceEntryIds"`
	NonSourceEntryIDs      []string             `json:"nonSourceEntryIds"`
	SourceCharacterCount   *int                 `json:"sourceCharacterCount,omitempty"`
}

// UnavailableSupportingObservation names a supporting observation missing from the branch.
type UnavailableSupportingObservation struct {
	ObservationID string `json:"observationId"`
}

// Details is the tool result's structured details.
type Details struct {
	Status                            Status                             `json:"status"`
	MemoryID                          string                             `json:"memoryId"`
	ObservationID                     string                             `json:"observationId"`
	Collision                         bool                               `json:"collision"`
	Partial                           bool                               `json:"partial"`
	Reflections                       []ReflectionDetails                `json:"reflections"`
	DirectObservationMatches          []MatchDetails                     `json:"directObservationMatches"`
	Observations                      []MatchDetails                     `json:"observations"`
	Matches                           []MatchDetails                     `json:"matches"`
	SourceEntries                     []SourceEntryDetails               `json:"sourceEntries"`
	UnavailableSupportingObservations []UnavailableSupportingObservation `json:"unavailableSupportingObservations"`
	MissingSourceEntryIDs             []string                           `json:"missingSourceEntryIds"`
	NonSourceEntryIDs                 []string                           `json:"nonSourceEntryIds"`
	SourceCharacterCount              *int                               `json:"sourceCharacterCount,omitempty"`
	Message                           string                             `json:"message,omitempty"`
}

// nonNil keeps an empty list a JSON [] rather than null.
func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func uniqueStrings(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func sourceOriginAndQualifiers(entry ledger.Entry) (origin, timestamp string, qualifiers []string) {
	qualifiers = []string{}
	if entry.Type == "message" && entry.Message != nil {
		msg := entry.Message
		timestamp = serialize.FormatRecallTimestamp(msg["timestamp"], entryTimestamp(entry))
		switch msg["role"] {
		case "user":
			return "User", timestamp, qualifiers
		case "assistant":
			names := []string{}
			if blocks, ok := msg["content"].([]any); ok {
				for _, raw := range blocks {
					block, _ := raw.(map[string]any)
					if block != nil && block["type"] == "toolCall" {
						if name, ok := block["name"].(string); ok {
							names = append(names, name)
						}
					}
				}
			}
			names = uniqueStrings(names)
			if len(names) > 0 {
				qualifiers = append(qualifiers, "tool calls: "+strings.Join(names, ", "))
			}
			return "Assistant", timestamp, qualifiers
		}
		toolName, _ := msg["toolName"].(string)
		if toolName == "" {
			toolName = "unknown"
		}
		return "Tool result: " + toolName, timestamp, qualifiers
	}
	timestamp = serialize.FormatRecallTimestamp(entryTimestamp(entry))
	switch entry.Type {
	case "custom_message":
		if entry.CustomType != "" {
			qualifiers = append(qualifiers, "custom: "+entry.CustomType)
		}
		return "Custom message", timestamp, qualifiers
	case "branch_summary":
		return "Branch summary", timestamp, qualifiers
	}
	origin = entry.Type
	if origin == "" {
		origin = "Entry"
	}
	return origin, timestamp, qualifiers
}

func entryTimestamp(entry ledger.Entry) any {
	if entry.Timestamp == "" {
		return nil
	}
	return entry.Timestamp
}

var headerPrefix = regexp.MustCompile(`^\[[^\]]+\]:\s?`)

func renderSourceEntryContentOnly(entry ledger.Entry) string {
	rendered, ok := serialize.RenderRecallSourceEntry(entry)
	if !ok {
		return ""
	}
	return headerPrefix.ReplaceAllString(rendered, "")
}

func sourceEntryDetails(entry ledger.Entry, includeContent bool) SourceEntryDetails {
	origin, timestamp, qualifiers := sourceOriginAndQualifiers(entry)
	details := SourceEntryDetails{ID: entry.ID, Origin: origin, Timestamp: timestamp, Tokens: tokens.EstimateEntryTokens(entry), Qualifiers: qualifiers}
	if includeContent {
		details.Content = renderSourceEntryContentOnly(entry)
	}
	return details
}

func observationDetails(observation ledger.Observation, status string) ObservationDetails {
	return ObservationDetails{ID: observation.ID, Content: observation.Content, Timestamp: observation.Timestamp, Relevance: string(observation.Relevance), Status: status}
}

func reflectionDetails(reflection ledger.Reflection, index int) ReflectionDetails {
	return ReflectionDetails{ID: reflection.ID, Content: reflection.Content, SupportingObservationIDs: reflection.SupportingObservationIDs, ReflectionIndex: index}
}

func observationMatchDetails(match ledger.RecalledObservation, includeSourceContent bool) MatchDetails {
	unavailable := len(match.MissingSourceEntryIDs) > 0 || len(match.NonSourceEntryIDs) > 0
	status := match.Status
	if unavailable {
		status = string(StatusSourceUnavailable)
	} else if len(match.SourceEntries) == 0 {
		status = string(StatusNoSource)
	}
	sources := make([]SourceEntryDetails, 0, len(match.SourceEntries))
	for _, entry := range match.SourceEntries {
		sources = append(sources, sourceEntryDetails(entry, includeSourceContent))
	}
	count := jsfmt.UTF16Len(serialize.RenderRecallSourceEntries(match.SourceEntries))
	return MatchDetails{
		Status:                 status,
		ObservationEntryID:     match.ObservationEntryID,
		ObservationRecordIndex: match.ObservationRecordIndex,
		Observation:            observationDetails(match.Observation, match.Status),
		SourceEntryIDs:         nonNil(match.SourceEntryIDs),
		SourceEntries:          sources,
		MissingSourceEntryIDs:  nonNil(match.MissingSourceEntryIDs),
		NonSourceEntryIDs:      nonNil(match.NonSourceEntryIDs),
		SourceCharacterCount:   &count,
	}
}

func emptyDetails(status Status, memoryID, message string) Details {
	return Details{
		Status:                            status,
		MemoryID:                          memoryID,
		ObservationID:                     memoryID,
		Reflections:                       []ReflectionDetails{},
		DirectObservationMatches:          []MatchDetails{},
		Observations:                      []MatchDetails{},
		Matches:                           []MatchDetails{},
		SourceEntries:                     []SourceEntryDetails{},
		UnavailableSupportingObservations: []UnavailableSupportingObservation{},
		MissingSourceEntryIDs:             []string{},
		NonSourceEntryIDs:                 []string{},
		Message:                           message,
	}
}

func aggregateStatus(details Details) Status {
	observationOnly := len(details.Reflections) == 0 && len(details.UnavailableSupportingObservations) == 0
	if details.Partial {
		return StatusPartial
	}
	if observationOnly {
		for _, match := range details.Observations {
			if match.Status == string(StatusSourceUnavailable) {
				return StatusSourceUnavailable
			}
		}
	}
	if observationOnly && len(details.Observations) > 0 && len(details.SourceEntries) == 0 {
		allEmpty := true
		for _, match := range details.Matches {
			if len(match.SourceEntries) > 0 {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			return StatusNoSource
		}
	}
	return StatusOK
}

func friendlyNoSourceMessage(memoryID string) string {
	return fmt.Sprintf("Observation %s has no source entries associated with it.", memoryID)
}

func friendlySourceUnavailableMessage(match MatchDetails) string {
	missing := ""
	if len(match.MissingSourceEntryIDs) > 0 {
		missing = " missing: " + strings.Join(match.MissingSourceEntryIDs, ", ")
	}
	nonSource := ""
	if len(match.NonSourceEntryIDs) > 0 {
		nonSource = " non-source: " + strings.Join(match.NonSourceEntryIDs, ", ")
	}
	return fmt.Sprintf("Observation %s has source entries associated, but some are unavailable on the current branch or are not source-renderable.%s%s", match.Observation.ID, missing, nonSource)
}

func reflectionLineText(reflection ReflectionDetails) string {
	return "[" + reflection.ID + "] " + reflection.Content
}

func observationLineText(observation ObservationDetails) string {
	status := ""
	if observation.Status == "dropped" {
		status = " [dropped]"
	}
	return "[" + observation.ID + "]" + status + " " + observation.Timestamp + " [" + observation.Relevance + "] " + observation.Content
}

func directObservationMatches(result ledger.RecallResult) []ledger.RecalledObservation {
	matches := []ledger.RecalledObservation{}
	for _, match := range result.Observations {
		if match.Observation.ID == result.MemoryID {
			matches = append(matches, match)
		}
	}
	return matches
}

func renderObservationOnlyText(result ledger.RecallResult) string {
	sections := []string{}
	if result.Collision {
		sections = append(sections, fmt.Sprintf("Memory id %s matched multiple observations; returning all matching source results from the current branch.", result.MemoryID))
	}
	for _, match := range directObservationMatches(result) {
		if match.Status == "dropped" {
			sections = append(sections, fmt.Sprintf("Observation %s is dropped from active memory but remains recallable.", match.Observation.ID))
		}
		unavailable := len(match.MissingSourceEntryIDs) > 0 || len(match.NonSourceEntryIDs) > 0
		if unavailable {
			sections = append(sections, friendlySourceUnavailableMessage(observationMatchDetails(match, false)))
		}
		if len(match.SourceEntries) == 0 {
			if !unavailable {
				sections = append(sections, friendlyNoSourceMessage(match.Observation.ID))
			}
			continue
		}
		// Sources that did resolve are still evidence the agent should see,
		// as the TUI already shows them.
		sourceText := serialize.RenderRecallSourceEntries(match.SourceEntries)
		if strings.TrimSpace(sourceText) != "" {
			sections = append(sections, sourceText)
		} else {
			sections = append(sections, fmt.Sprintf("Observation %s has source entries associated, but they rendered no text content.", match.Observation.ID))
		}
	}
	return strings.Join(sections, "\n\n")
}

func unavailableSupportingLineText(item UnavailableSupportingObservation) string {
	return fmt.Sprintf("Supporting observation %s is unavailable on the current branch.", item.ObservationID)
}

func renderMemoryText(result ledger.RecallResult) string {
	sections := []string{}
	if result.Collision {
		sections = append(sections, fmt.Sprintf("Memory id %s matched multiple observations/reflections; returning all available evidence from the current branch.", result.MemoryID))
	}
	if len(result.Reflections) > 0 {
		lines := []string{}
		for _, match := range result.Reflections {
			lines = append(lines, reflectionLineText(reflectionDetails(match.Reflection, match.ReflectionRecordIndex)))
		}
		sections = append(sections, "Reflections:\n"+strings.Join(lines, "\n"))
	}
	if len(result.Observations) > 0 {
		lines := []string{}
		for _, match := range result.Observations {
			lines = append(lines, observationLineText(observationDetails(match.Observation, match.Status)))
		}
		sections = append(sections, "Observations:\n"+strings.Join(lines, "\n"))
	}
	if len(result.MissingSupportingObservationIDs) > 0 {
		lines := []string{}
		for _, id := range result.MissingSupportingObservationIDs {
			lines = append(lines, unavailableSupportingLineText(UnavailableSupportingObservation{ObservationID: id}))
		}
		sections = append(sections, "Unavailable supporting observations:\n"+strings.Join(lines, "\n"))
	}
	if len(result.MissingSourceEntryIDs) > 0 || len(result.NonSourceEntryIDs) > 0 {
		parts := []string{}
		if len(result.MissingSourceEntryIDs) > 0 {
			parts = append(parts, "missing: "+strings.Join(result.MissingSourceEntryIDs, ", "))
		}
		if len(result.NonSourceEntryIDs) > 0 {
			parts = append(parts, "non-source: "+strings.Join(result.NonSourceEntryIDs, ", "))
		}
		sections = append(sections, "Unavailable source entries: "+strings.Join(parts, "; "))
	}
	if sourceText := serialize.RenderRecallSourceEntries(result.SourceEntries); strings.TrimSpace(sourceText) != "" {
		sections = append(sections, "Sources:\n"+sourceText)
	}
	if len(sections) == 0 {
		sections = append(sections, fmt.Sprintf("Memory %s was found, but no source evidence rendered.", result.MemoryID))
	}
	return strings.Join(sections, "\n\n")
}

func resultDetails(result ledger.RecallResult, includeSourceContent bool) Details {
	details := emptyDetails("", result.MemoryID, "")
	details.Collision = result.Collision
	details.Partial = result.Partial
	for _, match := range result.Reflections {
		details.Reflections = append(details.Reflections, reflectionDetails(match.Reflection, match.ReflectionRecordIndex))
	}
	for _, match := range result.Observations {
		details.Observations = append(details.Observations, observationMatchDetails(match, includeSourceContent))
	}
	for _, match := range directObservationMatches(result) {
		details.DirectObservationMatches = append(details.DirectObservationMatches, observationMatchDetails(match, includeSourceContent))
	}
	details.Matches = details.DirectObservationMatches
	for _, entry := range result.SourceEntries {
		details.SourceEntries = append(details.SourceEntries, sourceEntryDetails(entry, includeSourceContent))
	}
	for _, id := range result.MissingSupportingObservationIDs {
		details.UnavailableSupportingObservations = append(details.UnavailableSupportingObservations, UnavailableSupportingObservation{ObservationID: id})
	}
	details.MissingSourceEntryIDs = nonNil(result.MissingSourceEntryIDs)
	details.NonSourceEntryIDs = nonNil(result.NonSourceEntryIDs)
	count := jsfmt.UTF16Len(serialize.RenderRecallSourceEntries(result.SourceEntries))
	details.SourceCharacterCount = &count
	details.Status = aggregateStatus(details)
	return details
}

func isObservationOnly(details Details) bool {
	return len(details.Reflections) == 0 && len(details.UnavailableSupportingObservations) == 0
}

// Execute runs the recall tool over the branch entries.
func Execute(entries []ledger.Entry, memoryID string) (text string, details Details) {
	if !memoryIDPattern.MatchString(memoryID) {
		message := "Memory id must be 12 lowercase hex characters. Received: " + memoryID
		return message, emptyDetails(StatusInvalidID, memoryID, message)
	}
	result := ledger.RecallMemorySources(entries, memoryID)
	if result.Status == "not_found" {
		message := fmt.Sprintf("No observation or reflection with id %s was found on the current branch.", memoryID)
		return message, emptyDetails(StatusNotFound, memoryID, message)
	}
	details = resultDetails(result, true)
	if result.Kind == "observation" {
		return renderObservationOnlyText(result), details
	}
	return renderMemoryText(result), details
}

func plural(n int, singular string) string {
	return jsfmt.Locale(n) + " " + jsfmt.Plural(n, singular)
}

func sourceEntriesFromDetails(details Details) []SourceEntryDetails {
	if !isObservationOnly(details) {
		return details.SourceEntries
	}
	sources := []SourceEntryDetails{}
	for _, match := range details.Matches {
		sources = append(sources, match.SourceEntries...)
	}
	return sources
}

func tokenSummary(count int) string {
	return "~" + jsfmt.Locale(count) + " " + jsfmt.Plural(count, "token")
}

func isFailureStatus(status Status) bool {
	return status == StatusInvalidID || status == StatusNotFound
}

func observationCountForHeader(details Details) int {
	if isObservationOnly(details) {
		return len(details.Matches)
	}
	return len(details.Observations)
}

// FormatHeaderForTui renders the result header line.
func FormatHeaderForTui(details Details) string {
	if isFailureStatus(details.Status) {
		return "× failure"
	}
	parts := []string{"✓ success"}
	if len(details.Reflections) > 0 {
		parts = append(parts, plural(len(details.Reflections), "reflection"))
	}
	if observations := observationCountForHeader(details); observations > 0 {
		parts = append(parts, plural(observations, "observation"))
	}
	sources := sourceEntriesFromDetails(details)
	if len(sources) > 0 {
		parts = append(parts, plural(len(sources), "source"))
	}
	total := 0
	for _, source := range sources {
		total += source.Tokens
	}
	if total > 0 {
		parts = append(parts, tokenSummary(total))
	}
	if details.Partial && details.Status != StatusOK {
		parts = append(parts, strings.ReplaceAll(string(details.Status), "_", " "))
	}
	return strings.Join(parts, " · ")
}

const (
	tuiTypeWidth = 15
	tuiMetaWidth = 31
)

func padEnd(text string, width int) string {
	for jsfmt.UTF16Len(text) < width {
		text += " "
	}
	return text
}

func alignedRow(kind, meta, text string) string {
	return strings.TrimRight(padEnd(kind, tuiTypeWidth)+" "+padEnd(meta, tuiMetaWidth)+" "+text, " \t\n\r")
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func sourceTag(source SourceEntryDetails) string {
	origin := strings.ToLower(strings.TrimSpace(source.Origin))
	switch {
	case origin == "user":
		return "user"
	case origin == "assistant":
		return "assistant"
	case strings.HasPrefix(origin, "tool result"):
		return "tool"
	case strings.HasPrefix(origin, "custom message"):
		return "custom"
	case strings.HasPrefix(origin, "branch summary"):
		return "summary"
	}
	for _, part := range nonAlnum.Split(origin, -1) {
		if part != "" {
			return part
		}
	}
	return "entry"
}

func sourceMetadataLine(source SourceEntryDetails) string {
	return alignedRow("✓ source", source.Timestamp+" ["+sourceTag(source)+"]", tokenSummary(source.Tokens))
}

func observationLine(observation ObservationDetails) string {
	status := ""
	if observation.Status == "dropped" {
		status = " dropped"
	}
	return alignedRow("✓ observation", observation.Timestamp+" ["+observation.Relevance+"]"+status, observation.Content)
}

func reflectionLine(reflection ReflectionDetails) string {
	return alignedRow("✓ reflection", "", reflection.Content)
}

func noteLine(kind, text string) string {
	return alignedRow("• note", "["+kind+"]", text)
}

func indentContent(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}

func memoryRows(details Details) []string {
	rows := []string{}
	if isObservationOnly(details) {
		for _, match := range details.Matches {
			rows = append(rows, observationLine(match.Observation))
		}
		return rows
	}
	for _, reflection := range details.Reflections {
		rows = append(rows, reflectionLine(reflection))
	}
	for _, match := range details.Observations {
		rows = append(rows, observationLine(match.Observation))
	}
	return rows
}

func noteRows(details Details, sources []SourceEntryDetails) []string {
	notes := []string{}
	switch details.Status {
	case StatusInvalidID:
		return append(notes, noteLine("invalid id", "memory ids must be 12 lowercase hex characters; received "+details.MemoryID))
	case StatusNotFound:
		return append(notes, noteLine("not found", "no observation or reflection with id "+details.MemoryID+" was found on the current branch"))
	}
	if details.Collision {
		notes = append(notes, noteLine("id collision", "multiple memory items share "+details.MemoryID))
	}
	for _, match := range details.Observations {
		if match.Observation.Status == "dropped" {
			notes = append(notes, noteLine("dropped", "one or more observations are dropped from active memory but remain recallable"))
			break
		}
	}
	if len(details.UnavailableSupportingObservations) > 0 {
		ids := []string{}
		for _, item := range details.UnavailableSupportingObservations {
			ids = append(ids, item.ObservationID)
		}
		notes = append(notes, noteLine("missing support", strings.Join(ids, ", ")))
	}
	if len(details.MissingSourceEntryIDs) > 0 {
		notes = append(notes, noteLine("missing source", strings.Join(details.MissingSourceEntryIDs, ", ")))
	}
	if len(details.NonSourceEntryIDs) > 0 {
		notes = append(notes, noteLine("non-source", strings.Join(details.NonSourceEntryIDs, ", ")))
	}
	if len(sources) == 0 && (len(details.Reflections) > 0 || len(details.Observations) > 0 || len(details.Matches) > 0) {
		notes = append(notes, noteLine("unavailable evidence", "no source entries are available for this memory id"))
	}
	return notes
}

// FormatResultForTui renders the result body. details is nil when the result
// carried none; text is then the result's text content.
func FormatResultForTui(details *Details, text string, expanded bool) string {
	if details == nil {
		if text == "" {
			return "recall"
		}
		return text
	}
	sources := sourceEntriesFromDetails(*details)
	lines := []string{}
	rows := memoryRows(*details)
	notes := noteRows(*details, sources)
	lines = append(lines, rows...)
	if len(rows) > 0 && len(notes) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, notes...)
	if (len(rows) > 0 || len(notes) > 0) && len(sources) > 0 {
		lines = append(lines, "")
	}
	hasContent := false
	for _, source := range sources {
		lines = append(lines, sourceMetadataLine(source))
		if source.Content != "" {
			hasContent = true
		}
		if expanded && source.Content != "" {
			lines = append(lines, indentContent(source.Content), "")
		}
	}
	if !expanded && hasContent {
		lines = append(lines, "", "(Ctrl+O to expand)")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\n\r")
}

// FormatCallForTui renders the tool call line.
func FormatCallForTui(id string) string {
	if id == "" {
		id = "..."
	}
	return "recall " + id
}

// FormatRenderedResultForTui renders the header and body.
func FormatRenderedResultForTui(details *Details, text string, expanded bool) string {
	body := FormatResultForTui(details, text, expanded)
	header := ""
	if details != nil {
		header = FormatHeaderForTui(*details)
	}
	switch {
	case header != "" && body != "":
		return "\n" + header + "\n\n" + body
	case header != "":
		return "\n" + header
	case body != "":
		return "\n" + body
	}
	return ""
}
