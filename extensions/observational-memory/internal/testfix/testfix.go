// Package testfix builds session entries and fake hosts for tests.
package testfix

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// The V3 custom entry types and the old V2 ones.
const (
	V3ObservationsRecorded = "om.observations.recorded"
	V3ReflectionsRecorded  = "om.reflections.recorded"
	V3ObservationsDropped  = "om.observations.dropped"
	V3Folded               = "om.folded"
	V2Observation          = "om.observation"
	V2DetailsType          = "observational-memory"
)

// DefaultTimestamp is every fixture entry's timestamp.
const DefaultTimestamp = "2026-05-02T10:00:00.000Z"

// ToJSONValue round-trips v through JSON, as data read back from the session is.
func ToJSONValue(v any) any {
	encoded, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		panic(err)
	}
	return decoded
}

// Entry builds an entry from a decoded JSON-like object.
func Entry(m map[string]any) session.Entry {
	return session.FromMap(ToJSONValue(m).(map[string]any))
}

// RawMessage is a user message entry with one text block.
func RawMessage(id, text string, overrides ...map[string]any) session.Entry {
	m := map[string]any{
		"type": "message", "id": id, "parentId": nil, "timestamp": DefaultTimestamp,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}},
	}
	return Entry(merge(m, overrides...))
}

// MessageEntry is a message entry with the given message object.
func MessageEntry(id string, message map[string]any, overrides ...map[string]any) session.Entry {
	m := map[string]any{"type": "message", "id": id, "parentId": nil, "timestamp": DefaultTimestamp, "message": message}
	return Entry(merge(m, overrides...))
}

// CustomMessage is a custom_message entry.
func CustomMessage(id string, content any, overrides ...map[string]any) session.Entry {
	m := map[string]any{"type": "custom_message", "id": id, "parentId": nil, "timestamp": DefaultTimestamp, "content": content}
	return Entry(merge(m, overrides...))
}

// TextCustomMessage is a custom_message entry with string content.
func TextCustomMessage(id, text string, overrides ...map[string]any) session.Entry {
	return CustomMessage(id, text, overrides...)
}

// BranchSummary is a branch_summary entry.
func BranchSummary(id, summary string, overrides ...map[string]any) session.Entry {
	m := map[string]any{"type": "branch_summary", "id": id, "parentId": nil, "timestamp": DefaultTimestamp, "summary": summary}
	return Entry(merge(m, overrides...))
}

// CompactionArgs configure CompactionEntry.
type CompactionArgs struct {
	FirstKeptEntryID string
	Details          any
	Summary          string
}

// CompactionEntry is a compaction entry.
func CompactionEntry(id string, args CompactionArgs, overrides ...map[string]any) session.Entry {
	summary := args.Summary
	if summary == "" {
		summary = "compacted memory"
	}
	m := map[string]any{"type": "compaction", "id": id, "parentId": nil, "timestamp": DefaultTimestamp, "summary": summary}
	if args.FirstKeptEntryID != "" {
		m["firstKeptEntryId"] = args.FirstKeptEntryID
	}
	if args.Details != nil {
		m["details"] = args.Details
	}
	return Entry(merge(m, overrides...))
}

// MemoryDetailsArgs configure MemoryDetails.
type MemoryDetailsArgs struct {
	FullFold           bool
	Observations       []ledger.Observation
	Reflections        []ledger.Reflection
	FoldThroughEntryID string
}

// MemoryDetails is an om.folded details object.
func MemoryDetails(args MemoryDetailsArgs) map[string]any {
	observations := args.Observations
	if observations == nil {
		observations = []ledger.Observation{}
	}
	reflections := args.Reflections
	if reflections == nil {
		reflections = []ledger.Reflection{}
	}
	m := map[string]any{"type": V3Folded, "version": 1, "fullFold": args.FullFold, "observations": observations, "reflections": reflections}
	if args.FoldThroughEntryID != "" {
		m["foldThroughEntryId"] = args.FoldThroughEntryID
	}
	return ToJSONValue(m).(map[string]any)
}

// Observation is a fixture observation; apply changes with the modifier.
func Observation(id string, modify ...func(*ledger.Observation)) ledger.Observation {
	o := ledger.Observation{ID: id, Content: "Observation " + id, Timestamp: DefaultTimestamp, Relevance: "medium", SourceEntryIDs: []string{"raw-1"}, TokenCount: 10}
	for _, fn := range modify {
		fn(&o)
	}
	return o
}

// Reflection is a fixture reflection.
func Reflection(id string, supporting []string, modify ...func(*ledger.Reflection)) ledger.Reflection {
	if supporting == nil {
		supporting = []string{"obs-1"}
	}
	r := ledger.Reflection{ID: id, Content: "Reflection " + id, SupportingObservationIDs: supporting, TokenCount: 5}
	for _, fn := range modify {
		fn(&r)
	}
	return r
}

// ObservationsRecordedEntry is an om.observations.recorded entry.
func ObservationsRecordedEntry(id string, observations []ledger.Observation, coversUpToID string, overrides ...map[string]any) session.Entry {
	m := map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": DefaultTimestamp, "customType": V3ObservationsRecorded,
		"data": map[string]any{"observations": observations, "coversUpToId": coversUpToID}}
	return Entry(merge(m, overrides...))
}

// ReflectionsRecordedEntry is an om.reflections.recorded entry.
func ReflectionsRecordedEntry(id string, reflections []ledger.Reflection, coversUpToID string, overrides ...map[string]any) session.Entry {
	m := map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": DefaultTimestamp, "customType": V3ReflectionsRecorded,
		"data": map[string]any{"reflections": reflections, "coversUpToId": coversUpToID}}
	return Entry(merge(m, overrides...))
}

// ObservationsDroppedEntry is an om.observations.dropped entry.
func ObservationsDroppedEntry(id string, observationIDs []string, coversUpToID string, overrides ...map[string]any) session.Entry {
	m := map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": DefaultTimestamp, "customType": V3ObservationsDropped,
		"data": map[string]any{"observationIds": observationIDs, "coversUpToId": coversUpToID}}
	return Entry(merge(m, overrides...))
}

// OldV2ObservationEntry is an old V2 observation entry the V3 ledger ignores.
func OldV2ObservationEntry(id string) session.Entry {
	return Entry(map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": DefaultTimestamp, "customType": V2Observation,
		"data": map[string]any{"records": []ledger.Observation{Observation("v2-obs")}, "coversFromId": "raw-1", "coversUpToId": "raw-1", "tokenCount": 10}})
}

// OldV2CompactionDetails are old V2 compaction details the V3 ledger ignores.
func OldV2CompactionDetails() map[string]any {
	return ToJSONValue(map[string]any{"type": V2DetailsType, "version": 4, "observations": []ledger.Observation{Observation("v2-obs")}, "reflections": []ledger.Reflection{}}).(map[string]any)
}

func merge(base map[string]any, overrides ...map[string]any) map[string]any {
	for _, override := range overrides {
		for k, v := range override {
			base[k] = v
		}
	}
	return base
}

// IDs lists observation ids.
func IDs(observations []ledger.Observation) []string {
	ids := []string{}
	for _, o := range observations {
		ids = append(ids, o.ID)
	}
	return ids
}

// RefIDs lists reflection ids.
func RefIDs(reflections []ledger.Reflection) []string {
	ids := []string{}
	for _, r := range reflections {
		ids = append(ids, r.ID)
	}
	return ids
}

// Appended is one AppendEntry call.
type Appended struct {
	CustomType string
	Data       any
}

// Notification is one Notify call.
type Notification struct {
	Message string
	Level   string
}

// ScriptedStream is a model stream that delivers prepared events and a terminal message.
type ScriptedStream struct {
	Message map[string]any
	// Block, when set, holds the stream until ctx is cancelled.
	Block bool
}

// Events delivers the terminal event.
func (s ScriptedStream) Events(ctx context.Context) <-chan map[string]any {
	out := make(chan map[string]any)
	go func() {
		defer close(out)
		if s.Block {
			<-ctx.Done()
			return
		}
		event := map[string]any{"type": "done", "reason": s.Message["stopReason"], "message": s.Message}
		select {
		case out <- event:
		case <-ctx.Done():
		}
	}()
	return out
}

// Result is the terminal message.
func (s ScriptedStream) Result() map[string]any { return s.Message }

// AssistantText is an assistant message that answers with text.
func AssistantText(text string) map[string]any {
	return ToJSONValue(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "stopReason": "stop", "usage": map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2}, "timestamp": 1}).(map[string]any)
}

// AssistantToolCall is an assistant message that calls one tool.
func AssistantToolCall(id, name string, args map[string]any) map[string]any {
	return ToJSONValue(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": id, "name": name, "arguments": args}}, "stopReason": "toolUse", "usage": map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2}, "timestamp": 1}).(map[string]any)
}

// AssistantError is an assistant message that ended with a stream failure.
func AssistantError(stopReason, errorMessage string) map[string]any {
	return ToJSONValue(map[string]any{"role": "assistant", "content": []any{}, "stopReason": stopReason, "errorMessage": errorMessage, "timestamp": 1}).(map[string]any)
}

// StreamRecorder scripts successive model calls and records the requests.
type StreamRecorder struct {
	mu       sync.Mutex
	Replies  []map[string]any
	Requests []map[string]any
	Models   []host.Model
	// Block holds every stream until cancellation when set.
	Block bool
	// Fail makes the stream function return nil.
	Fail bool
}

// Stream is a host-compatible stream function.
func (r *StreamRecorder) Stream(model host.Model, request map[string]any) host.ModelStream {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Requests = append(r.Requests, request)
	r.Models = append(r.Models, model)
	if r.Fail {
		return nil
	}
	if r.Block {
		return ScriptedStream{Block: true}
	}
	if len(r.Replies) == 0 {
		return ScriptedStream{Message: AssistantText("done")}
	}
	reply := r.Replies[0]
	r.Replies = r.Replies[1:]
	return ScriptedStream{Message: reply}
}

// Calls is how many model calls were made.
func (r *StreamRecorder) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.Requests)
}

// FakeModels is an in-memory model registry.
type FakeModels struct {
	mu           sync.Mutex
	Registry     map[string]host.Model
	Auth         map[string]host.Auth
	AuthErr      map[string]error
	OAuth        map[string]bool
	Configured   map[string]bool
	RefreshFn    func(ctx context.Context, options host.RefreshOptions) error
	RefreshCalls []host.RefreshOptions
	StreamFn     host.ModelStream
	Streams      *StreamRecorder
	// FindCalls records every Find lookup as provider/id.
	FindCalls []string
	// AuthCalls records the provider of every GetApiKeyAndHeaders call.
	AuthCalls []string
	// OnFind, when set, runs after every Find (outside the lock).
	OnFind func(provider, id string)
}

// NewFakeModels creates an empty registry.
func NewFakeModels() *FakeModels {
	return &FakeModels{Registry: map[string]host.Model{}, Auth: map[string]host.Auth{}, AuthErr: map[string]error{}, OAuth: map[string]bool{}, Configured: map[string]bool{}, Streams: &StreamRecorder{}}
}

// Key is a registry key.
func Key(provider, id string) string { return provider + "/" + id }

// Add registers a model and its auth.
func (m *FakeModels) Add(model host.Model, auth host.Auth) host.Model {
	m.Registry[Key(host.ModelProvider(model), host.ModelID(model))] = model
	m.Auth[host.ModelProvider(model)] = auth
	return model
}

// Model builds a model object.
func Model(provider, id string, extra ...map[string]any) host.Model {
	model := host.Model{"provider": provider, "id": id, "api": "test-api"}
	for _, e := range extra {
		for k, v := range e {
			model[k] = v
		}
	}
	return ToJSONValue(model).(map[string]any)
}

// KeyAuth is an API-key auth.
func KeyAuth(key string) host.Auth { k := key; return host.Auth{OK: true, APIKey: &k} }

func (m *FakeModels) Find(provider, id string) host.Model {
	m.mu.Lock()
	m.FindCalls = append(m.FindCalls, Key(provider, id))
	model := m.Registry[Key(provider, id)]
	hook := m.OnFind
	m.mu.Unlock()
	if hook != nil {
		hook(provider, id)
	}
	return model
}

func (m *FakeModels) GetApiKeyAndHeaders(model host.Model) (host.Auth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	provider := host.ModelProvider(model)
	m.AuthCalls = append(m.AuthCalls, provider)
	if err := m.AuthErr[provider]; err != nil {
		return host.Auth{}, err
	}
	auth, ok := m.Auth[provider]
	if !ok {
		return host.Auth{OK: false, Error: "no auth"}, nil
	}
	return auth, nil
}

func (m *FakeModels) IsUsingOAuth(model host.Model) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.OAuth[host.ModelProvider(model)]
}

func (m *FakeModels) HasConfiguredAuth(model host.Model) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Configured[host.ModelProvider(model)]
}

func (m *FakeModels) Refresh(ctx context.Context, options host.RefreshOptions) error {
	m.mu.Lock()
	m.RefreshCalls = append(m.RefreshCalls, options)
	fn := m.RefreshFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, options)
	}
	return nil
}

func (m *FakeModels) Stream(model host.Model, request map[string]any) host.ModelStream {
	if m.StreamFn != nil {
		return m.StreamFn
	}
	return m.Streams.Stream(model, request)
}

// FakeHost is an in-memory host.Host.
type FakeHost struct {
	mu            sync.Mutex
	Entries       []session.Entry
	Appended      []Appended
	Notifications []Notification
	UI            bool
	WorkDir       string
	Model         host.Model
	ModelsFake    *FakeModels
	Usage         *host.ContextUsage
	UsageErr      error
	Idle          bool
	IdleErr       error
	// IdleFn, when set, answers IsIdle instead of Idle/IdleErr.
	IdleFn        func() (bool, error)
	CompactCalls  int
	CompactFn     func(callbacks host.CompactCallbacks)
	ID            string
	File          string
	BranchErr     error
	AppendErr     error
	appendedCount int
	// BranchQueue, when set, serves successive GetBranch calls (the last one repeats).
	BranchQueue    [][]session.Entry
	branchIndex    int
	GetBranchCalls int
	// SessionEntries are the whole session file GetEntries returns; nil means the branch.
	SessionEntries []session.Entry
	// Home is PiG's config root ConfigHome reports; "" means none (no store).
	Home string
	// Tools is the session's tool registry, Active the active names; RegisterTool adds to
	// both, as the host does for a tool that is active by default.
	Tools  []host.ToolInfo
	Active []string
	// Registered are the RegisterTool calls' specs, SetActiveCalls the SetActiveTools calls.
	Registered     []host.ToolSpec
	SetActiveCalls [][]string
	// ToolsErr fails AllTools and ActiveTools; RegisterErr fails RegisterTool; SetActiveErr
	// fails SetActiveTools, leaving Active unchanged.
	ToolsErr     error
	RegisterErr  error
	SetActiveErr error
	// Trusted is ProjectTrusted's answer, TrustErr its failure; TrustCalls
	// counts the checks.
	Trusted    bool
	TrustErr   error
	TrustCalls int
	// Ctx is the request's cancellation RequestContext reports (a tool call's: PiG cancels it
	// on the user's abort); nil never cancels.
	Ctx context.Context
}

// RequestContext is the cancellation of the request the host answers (Ctx).
func (h *FakeHost) RequestContext() context.Context {
	if h.Ctx == nil {
		return context.Background()
	}
	return h.Ctx
}

// NewFakeHost creates a host with a UI, a cwd and the given entries.
func NewFakeHost(entries ...session.Entry) *FakeHost {
	return &FakeHost{Entries: entries, UI: true, WorkDir: "/tmp/pig-observational-memory-test", ModelsFake: NewFakeModels(), Idle: true, ID: "session-1"}
}

func (h *FakeHost) GetBranch() ([]session.Entry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.GetBranchCalls++
	if h.BranchErr != nil {
		return nil, h.BranchErr
	}
	if len(h.BranchQueue) > 0 {
		idx := min(h.branchIndex, len(h.BranchQueue)-1)
		h.branchIndex++
		return append([]session.Entry{}, h.BranchQueue[idx]...), nil
	}
	return append([]session.Entry{}, h.Entries...), nil
}

// GetEntries returns SessionEntries, or the branch when it is nil (host.SessionFileReader).
func (h *FakeHost) GetEntries() ([]session.Entry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.SessionEntries != nil {
		return append([]session.Entry{}, h.SessionEntries...), nil
	}
	return append([]session.Entry{}, h.Entries...), nil
}

// SetBranch replaces the branch.
func (h *FakeHost) SetBranch(entries []session.Entry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Entries = entries
}

func (h *FakeHost) AppendEntry(customType string, data any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.AppendErr != nil {
		return h.AppendErr
	}
	decoded := ToJSONValue(data)
	h.Appended = append(h.Appended, Appended{CustomType: customType, Data: decoded})
	h.appendedCount++
	parent := ""
	if len(h.Entries) > 0 {
		parent = h.Entries[len(h.Entries)-1].ID
	}
	entry := map[string]any{"type": "custom", "id": fmt.Sprintf("appended-%d", h.appendedCount), "parentId": parent, "timestamp": DefaultTimestamp, "customType": customType, "data": decoded}
	h.Entries = append(h.Entries, session.FromMap(entry))
	return nil
}

func (h *FakeHost) SessionID() string   { return h.ID }
func (h *FakeHost) SessionFile() string { return h.File }
func (h *FakeHost) HasUI() bool         { return h.UI }

func (h *FakeHost) Notify(message, level string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Notifications = append(h.Notifications, Notification{Message: message, Level: level})
}

// Messages lists the notification messages.
func (h *FakeHost) Messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []string{}
	for _, n := range h.Notifications {
		out = append(out, n.Message)
	}
	return out
}

func (h *FakeHost) IsIdle() (bool, error) {
	if h.IdleFn != nil {
		return h.IdleFn()
	}
	return h.Idle, h.IdleErr
}

func (h *FakeHost) Compact(callbacks host.CompactCallbacks) {
	h.mu.Lock()
	h.CompactCalls++
	fn := h.CompactFn
	h.mu.Unlock()
	if fn != nil {
		fn(callbacks)
	}
}

// CompactCount is how many times Compact was called (safe to read while the
// trigger's goroutine runs).
func (h *FakeHost) CompactCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.CompactCalls
}

func (h *FakeHost) GetContextUsage() (*host.ContextUsage, error) { return h.Usage, h.UsageErr }
func (h *FakeHost) Cwd() string                                  { return h.WorkDir }

// ProjectTrusted answers Trusted, or fails with TrustErr.
func (h *FakeHost) ProjectTrusted() (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.TrustCalls++
	if h.TrustErr != nil {
		return false, h.TrustErr
	}
	return h.Trusted, nil
}
func (h *FakeHost) SessionModel() host.Model { return h.Model }
func (h *FakeHost) Models() host.Models      { return h.ModelsFake }

// AppendedTypes lists the appended custom types in order.
func (h *FakeHost) AppendedTypes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []string{}
	for _, a := range h.Appended {
		out = append(out, a.CustomType)
	}
	return out
}

// ConfigHome is Home.
func (h *FakeHost) ConfigHome() string { return h.Home }

// AllTools lists Tools.
func (h *FakeHost) AllTools() ([]host.ToolInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ToolsErr != nil {
		return nil, h.ToolsErr
	}
	return append([]host.ToolInfo{}, h.Tools...), nil
}

// ActiveTools lists Active.
func (h *FakeHost) ActiveTools() ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ToolsErr != nil {
		return nil, h.ToolsErr
	}
	return append([]string{}, h.Active...), nil
}

// SetActiveTools replaces Active and records the call.
func (h *FakeHost) SetActiveTools(names []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.SetActiveErr != nil {
		return h.SetActiveErr
	}
	h.Active = append([]string{}, names...)
	h.SetActiveCalls = append(h.SetActiveCalls, append([]string{}, names...))
	return nil
}

// RegisterTool records spec and, for a new name, lists it in Tools and Active.
func (h *FakeHost) RegisterTool(spec host.ToolSpec) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.RegisterErr != nil {
		return h.RegisterErr
	}
	h.Registered = append(h.Registered, spec)
	for _, t := range h.Tools {
		if t.Name == spec.Name {
			return nil
		}
	}
	h.Tools = append(h.Tools, host.ToolInfo{Name: spec.Name, Source: "observational-memory"})
	h.Active = append(h.Active, spec.Name)
	return nil
}

// RegisteredSpec is the last spec registered under name.
func (h *FakeHost) RegisteredSpec(name string) (host.ToolSpec, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.Registered) - 1; i >= 0; i-- {
		if h.Registered[i].Name == name {
			return h.Registered[i], true
		}
	}
	return host.ToolSpec{}, false
}

// RequestCounter counts the model requests the extension makes, per stage, from the debug
// event every request logs at the single stream call site (debuglog.IsRequest).
type RequestCounter struct {
	mu      sync.Mutex
	byStage map[string]int
}

// CountRequests counts every model request from now until the test ends. The count is
// process-wide (debuglog.Subscribe), so a test that uses it must not call t.Parallel and must
// wait for the runs it starts. Assert on Total: 0 between compactions, at most 1 per compaction.
func CountRequests(t testing.TB) *RequestCounter {
	t.Helper()
	counter := &RequestCounter{byStage: map[string]int{}}
	unsubscribe := debuglog.Subscribe(func(event string, data map[string]any) {
		if !debuglog.IsRequest(data) {
			return
		}
		stage, _ := data["stage"].(string)
		counter.mu.Lock()
		defer counter.mu.Unlock()
		counter.byStage[stage]++
	})
	t.Cleanup(unsubscribe)
	return counter
}

// Total is how many requests were made.
func (c *RequestCounter) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, n := range c.byStage {
		total += n
	}
	return total
}

// Stage is how many requests stage made.
func (c *RequestCounter) Stage(stage string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byStage[stage]
}

// ByStage is the request count of every stage that made one.
func (c *RequestCounter) ByStage() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.byStage))
	for stage, n := range c.byStage {
		out[stage] = n
	}
	return out
}
