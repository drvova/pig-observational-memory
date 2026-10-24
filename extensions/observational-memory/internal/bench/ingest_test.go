package bench

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

var (
	sourceIDPattern  = regexp.MustCompile(`\[Source entry id: ([^\]]+)\]\n\[User @ ([0-9-]+ [0-9:]+)\]: ([^\n]*)`)
	localTimePattern = regexp.MustCompile(`Current local time: ([0-9-]+ [0-9:]+)`)
)

// scriptedModels answers like a cooperative observer: one record_observations
// call per user message in the chunk, dated by its inline timestamp, then a
// plain-text confirmation. Other workers answer with plain text.
type scriptedModels struct {
	Models
	mu         sync.Mutex
	localTimes []string
	calls      map[string]int
}

func (m *scriptedModels) Stream(model host.Model, request map[string]any) host.ModelStream {
	m.mu.Lock()
	defer m.mu.Unlock()
	role := WorkerRole(request)
	m.calls[role]++
	messages := request["messages"].([]any)
	reply := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done"}}, "stopReason": "stop"}
	if role == RoleObserver && len(messages) == 1 {
		prompt := blockText(messages[0].(map[string]any)["content"])
		if match := localTimePattern.FindStringSubmatch(prompt); match != nil {
			m.localTimes = append(m.localTimes, match[1])
		}
		observations := []any{}
		for _, match := range sourceIDPattern.FindAllStringSubmatch(prompt, -1) {
			observations = append(observations, map[string]any{"timestamp": match[2], "content": "User said " + match[3], "relevance": "high", "sourceEntryIds": []any{match[1]}})
		}
		if len(observations) > 0 {
			reply = map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
				map[string]any{"type": "toolCall", "id": "c1", "name": "record_observations", "arguments": map[string]any{"observations": observations}},
			}}
		}
	}
	return doneStream{message: reply}
}

func testQuestion() Question {
	long := strings.Repeat("filler words about the weather ", 40)
	return Question{
		ID: "q1", Type: "single-session-user", Question: "Where do I live?", RawAnswer: json.RawMessage(`"Paris"`), QuestionDate: "2023/06/01 (Thu) 09:00",
		HaystackDates: []string{"2023/05/21 (Sun) 10:00", "2023/05/20 (Sat) 08:30"},
		HaystackSessions: [][]Turn{
			{{Role: "user", Content: "I moved to Paris last week. " + long}, {Role: "assistant", Content: "Congratulations! " + long}},
			{{Role: "user", Content: "I adopted a cat named Miso. " + long}, {Role: "assistant", Content: "Lovely. " + long}},
		},
	}
}

func testOptions() IngestOptions {
	spec := ModelSpec{ID: "m", ContextWindow: 128_000, MaxTokens: 32_000, Reasoning: true}
	cfg := WorkerConfig(spec, "")
	cfg.ObserveAfterTokens = 200
	cfg.ReflectAfterTokens = 100_000
	return IngestOptions{Config: cfg, Spec: spec}
}

func TestIngestRunsTheObserverOverChronologicalSessions(t *testing.T) {
	opts := testOptions()
	models := &scriptedModels{Models: Models{Spec: opts.Spec}, calls: map[string]int{}}
	checkpoints := []Checkpoint{}
	opts.OnSession = func(c Checkpoint) error { checkpoints = append(checkpoints, c); return nil }
	result, err := Ingest(context.Background(), testQuestion(), models, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 2 || checkpoints[1].SessionsDone != 2 {
		t.Fatalf("checkpoints = %d", len(checkpoints))
	}
	observations := ledger.FullProjection(result.Entries, "").Observations
	if len(observations) != 2 {
		t.Fatalf("observations = %+v, errors = %v", observations, result.Errors)
	}
	// The earlier-dated session is replayed first, and observations carry the
	// conversation's own timestamps.
	if !strings.Contains(observations[0].Content, "Miso") || observations[0].Timestamp != "2023-05-20 08:30" {
		t.Fatalf("first observation = %+v", observations[0])
	}
	if !strings.Contains(observations[1].Content, "Paris") || observations[1].Timestamp != "2023-05-21 10:00" {
		t.Fatalf("second observation = %+v", observations[1])
	}
	// The observer's "current local time" is the replay clock, not the wall clock.
	if len(models.localTimes) == 0 || !strings.HasPrefix(models.localTimes[0], "2023-05-20") {
		t.Fatalf("local times = %v", models.localTimes)
	}
	memory := RenderMemory(result.Entries, opts.Config, opts.Spec.ContextWindow)
	if !strings.Contains(memory.Summary, "\nnotes[2]{") || !strings.Contains(memory.Summary, "Paris") || memory.ActiveObservations != 2 {
		t.Fatalf("memory = %+v", memory)
	}
	messages := OMMessages(testQuestion(), memory, false)
	if len(messages) != 3 || messages[0].Text() != ActorInstructions || messages[2].Text() != "Current Date: 2023/06/01 (Thu) 09:00\nQuestion: Where do I live?" {
		t.Fatalf("actor messages = %+v", messages)
	}
	// Without the memory tool the summary's paragraph about it is left out, in
	// either format.
	if strings.Contains(messages[1].Text(), "memory tool") || !strings.Contains(MemoryMessage(memory, true), ledger.TablesRecallParagraph) {
		t.Fatalf("recall paragraph handling: %q", messages[1].Text())
	}
	lines := opts.Config
	lines.MemoryFormat = config.MemoryFormatLines
	linesMemory := RenderMemory(result.Entries, lines, opts.Spec.ContextWindow)
	if !strings.Contains(linesMemory.Summary, "## Observations") || strings.Contains(MemoryMessage(linesMemory, false), "memory tool") || !strings.Contains(MemoryMessage(linesMemory, true), "use the memory tool") {
		t.Fatalf("lines recall paragraph handling: %q", MemoryMessage(linesMemory, false))
	}
	if withRecall := OMMessages(testQuestion(), memory, true); !strings.Contains(withRecall[0].Text(), "Use memory(query)") {
		t.Fatalf("recall prompt missing: %q", withRecall[0].Text())
	}
}

func TestIngestResumesFromACheckpoint(t *testing.T) {
	opts := testOptions()
	models := &scriptedModels{Models: Models{Spec: opts.Spec}, calls: map[string]int{}}
	var first Checkpoint
	opts.OnSession = func(c Checkpoint) error {
		if c.SessionsDone == 1 {
			first = c
			return context.Canceled // stop after the first session
		}
		return nil
	}
	if _, err := Ingest(context.Background(), testQuestion(), models, opts); err == nil {
		t.Fatal("expected the stop")
	}
	firstCalls := models.calls[RoleObserver]
	opts.OnSession = nil
	opts.Resume = &first
	result, err := Ingest(context.Background(), testQuestion(), models, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ledger.FullProjection(result.Entries, "").Observations); got != 2 {
		t.Fatalf("observations after resume = %d", got)
	}
	if models.calls[RoleObserver] <= firstCalls {
		t.Fatal("resume made no observer call")
	}
}

func TestIngestLeavesASmallTailRawUnlessFlushed(t *testing.T) {
	opts := testOptions()
	opts.Config.ObserveAfterTokens = 100_000
	models := &scriptedModels{Models: Models{Spec: opts.Spec}, calls: map[string]int{}}
	result, err := Ingest(context.Background(), testQuestion(), models, opts)
	if err != nil {
		t.Fatal(err)
	}
	memory := RenderMemory(result.Entries, opts.Config, opts.Spec.ContextWindow)
	if memory.Summary != "" || !strings.Contains(memory.Tail, "[User @ 2023-05-20 08:30]: I adopted a cat") || models.calls[RoleObserver] != 0 {
		t.Fatalf("memory = %+v, calls = %v", memory, models.calls)
	}
	if !strings.Contains(MemoryMessage(memory, false), TailHeading) {
		t.Fatal("tail heading missing")
	}

	opts.Flush = true
	result, err = Ingest(context.Background(), testQuestion(), models, opts)
	if err != nil {
		t.Fatal(err)
	}
	memory = RenderMemory(result.Entries, opts.Config, opts.Spec.ContextWindow)
	if memory.ActiveObservations != 2 || memory.Tail != "" {
		t.Fatalf("flushed memory = %+v", memory)
	}
}

func TestRenderMemoryRecordsItsBudgetAndFlagsAnEmptyRender(t *testing.T) {
	opts := testOptions()
	opts.Flush = true
	models := &scriptedModels{Models: Models{Spec: opts.Spec}, calls: map[string]int{}}
	result, err := Ingest(context.Background(), testQuestion(), models, opts)
	if err != nil {
		t.Fatal(err)
	}
	memory := RenderMemory(result.Entries, opts.Config, opts.Spec.ContextWindow)
	if memory.ActiveObservations == 0 || memory.NoRecordFits || memory.SummaryBudget != config.ResolveSummaryBudget(opts.Config, opts.Spec.ContextWindow, 0, 0) {
		t.Fatalf("memory = %+v", memory)
	}
	// A budget too small for any record keeps none; the flag says so.
	tiny := RenderMemoryWithBudget(result.Entries, opts.Config, 1, ledger.RenderOptions{})
	if !tiny.NoRecordFits || tiny.SummaryBudget != 1 {
		t.Fatalf("tiny memory = %+v", tiny)
	}
}

func TestStreamReportsHTTPFailureAsAStreamError(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "", NewMeter(nil))
	client.MaxAttempts = 1
	models := &Models{Spec: ModelSpec{ID: "m"}, Client: client}
	message := models.Stream(models.Spec.HostModel(), map[string]any{"systemPrompt": agents.DropperSystem})
	if message.Result()["stopReason"] != "error" {
		t.Fatalf("message = %v", message.Result())
	}
	if client.Meter.Snapshot()[RoleDropper].FailedCalls != 1 {
		t.Fatalf("usage = %v", client.Meter.Snapshot())
	}
}

func TestAskWithRecallRunsTheExtensionsRecallTool(t *testing.T) {
	opts := testOptions()
	models := &scriptedModels{Models: Models{Spec: opts.Spec}, calls: map[string]int{}}
	result, err := Ingest(context.Background(), testQuestion(), models, opts)
	if err != nil {
		t.Fatal(err)
	}
	id := ledger.FullProjection(result.Entries, "").Observations[1].ID
	var calls atomic.Int32
	var toolText string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if calls.Add(1) == 1 {
			if len(req.Tools) != 1 || req.Tools[0].Function.Name != "memory" {
				t.Errorf("tools = %+v", req.Tools)
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"t1","type":"function","function":{"name":"memory","arguments":"{\"query\":\"recall `+id+`\"}"}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		toolText = req.Messages[len(req.Messages)-1].Text()
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Paris"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "", NewMeter(nil))
	memory := RenderMemory(result.Entries, opts.Config, opts.Spec.ContextWindow)
	answer, recalls, err := AskWithRecall(context.Background(), client, OMMessages(testQuestion(), memory, true), result.Entries, AnswerOptions{Model: "m"})
	if err != nil || answer.Hypothesis != "Paris" || recalls != 1 {
		t.Fatalf("answer = %+v, recalls = %d, err = %v", answer, recalls, err)
	}
	if !strings.Contains(toolText, "I moved to Paris last week") {
		t.Fatalf("recall result = %q", toolText)
	}
}
