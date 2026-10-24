// Command longmemeval runs the LongMemEval benchmark against the extension's
// observational-memory pipeline. See docs/bench.md.
//
//	longmemeval run -data longmemeval_s.json -out /path/runs/<name> [flags]
//	longmemeval report -out /path/runs/<name>
//
// Each question runs in its own worker process (the observer's "current
// local time" is a process-wide clock pointed at the replayed conversation),
// writing its stages under <out>/questions/<id>/ so an interrupted run resumes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/bench"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// APIKeyEnv names the environment variable holding the endpoint's API key.
// The key is never written to disk or logs.
const APIKeyEnv = "LONGMEMEVAL_API_KEY"

type settings struct {
	Mode           string `json:"mode"`
	Model          string `json:"model"`
	BaseURL        string `json:"baseUrl"`
	ContextWindow  int    `json:"contextWindow"`
	ModelMaxTokens int    `json:"modelMaxTokens"`
	WorkerThinking string `json:"workerThinking,omitempty"`
	ActorMaxTokens int    `json:"actorMaxTokens"`
	JudgeMaxTokens int    `json:"judgeMaxTokens"`
	Flush          bool   `json:"flush"`
	Recall         bool   `json:"recall"`
	MaxAttempts    int    `json:"maxAttempts"`
	UserAgent      string `json:"userAgent,omitempty"`
	SessionHeader  string `json:"sessionHeader,omitempty"`
	Backend        string `json:"backend,omitempty"`
	ActorThinking  string `json:"actorThinking,omitempty"`
	JudgeThinking  string `json:"judgeThinking,omitempty"`
	CodexAuth      string `json:"codexAuth,omitempty"`
}

func (s *settings) register(fs *flag.FlagSet) {
	fs.StringVar(&s.Mode, "mode", "om", `"om" (observational memory) or "full-context" (raw sessions baseline)`)
	fs.StringVar(&s.Model, "model", "deepseek-v4.1-flash-uncensored", "model for every role (observer, reflector, dropper, actor, judge)")
	fs.StringVar(&s.BaseURL, "base-url", "", "OpenAI-compatible chat-completions endpoint, e.g. https://api.example.com/v1 (required unless -backend codex)")
	fs.StringVar(&s.Backend, "backend", "openai", `"openai" (chat completions, key from `+APIKeyEnv+`) or "codex" (a ChatGPT plan signed in with `+"`codex login`"+`)`)
	fs.StringVar(&s.ActorThinking, "actor-thinking", "", `actor thinking level ("" = provider default; "medium" with -backend codex)`)
	fs.StringVar(&s.JudgeThinking, "judge-thinking", "", `judge thinking level ("" = provider default; "medium" with -backend codex)`)
	fs.StringVar(&s.CodexAuth, "codex-auth", bench.DefaultCodexAuthPath(), "with -backend codex, the Codex CLI's auth.json")
	fs.IntVar(&s.ContextWindow, "context-window", 128_000, "model context window (sizes observer chunks, worker memory, summary budget, full-context truncation)")
	fs.IntVar(&s.ModelMaxTokens, "model-max-tokens", 32_000, "model output limit (clamps the workers' agentMaxTokens)")
	fs.StringVar(&s.WorkerThinking, "worker-thinking", "", `worker thinking level ("" = extension default "low")`)
	fs.IntVar(&s.ActorMaxTokens, "actor-max-tokens", 16_000, "actor output budget (reasoning included)")
	fs.IntVar(&s.JudgeMaxTokens, "judge-max-tokens", 4_000, "judge output budget (reasoning included)")
	fs.BoolVar(&s.Recall, "recall", false, "give the actor the extension's recall tool (off matches Mastra, whose runner disables recall)")
	fs.BoolVar(&s.Flush, "flush", false, "observe the unobserved tail after the last session instead of showing it raw")
	fs.IntVar(&s.MaxAttempts, "max-attempts", 8, "HTTP attempts per model call (429/5xx/network)")
	fs.StringVar(&s.UserAgent, "user-agent", "", "User-Agent sent on every model call")
	fs.StringVar(&s.SessionHeader, "session-header", "", "header carrying a stable per-question session id, for providers that route or cache by session")
}

// resolveBackend checks -backend and fills the Codex backend's base URL.
func (s *settings) resolveBackend() error {
	switch s.Backend {
	case "openai":
	case "codex":
		if s.BaseURL == "" {
			s.BaseURL = bench.DefaultCodexBaseURL
		}
		// Sent no effort, the Codex backend answers with next to no reasoning,
		// unlike chat-completions reasoning models, which reason by default; medium
		// is the plan's catalog default for its models.
		if s.ActorThinking == "" {
			s.ActorThinking = "medium"
		}
		if s.JudgeThinking == "" {
			s.JudgeThinking = "medium"
		}
	default:
		return fmt.Errorf("unknown -backend %q", s.Backend)
	}
	return nil
}

// client builds a worker's model client for the configured backend.
func (s settings) client(meter *bench.Meter, questionID string) (*bench.Client, error) {
	if s.Backend == "codex" {
		auth, err := bench.NewCodexAuth(s.CodexAuth)
		if err != nil {
			return nil, err
		}
		client := bench.NewClient(s.BaseURL, "", meter)
		client.Codex = auth
		client.CacheKey = "longmemeval-" + questionID
		client.OnLimit = func(reset time.Time) {
			fmt.Fprintf(os.Stderr, "%s usage limit: waiting until %s\n", time.Now().UTC().Format(time.RFC3339), reset.UTC().Format(time.RFC3339))
		}
		return client, nil
	}
	key := os.Getenv(APIKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("%s is not set", APIKeyEnv)
	}
	return bench.NewClient(s.BaseURL, key, meter), nil
}

func (s settings) spec() bench.ModelSpec {
	return bench.ModelSpec{ID: s.Model, ContextWindow: s.ContextWindow, MaxTokens: s.ModelMaxTokens, Reasoning: true}
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runCmd(os.Args[2:])
	case "report":
		err = reportCmd(os.Args[2:])
	case "worker":
		err = workerCmd(os.Args[2:])
	case "replay":
		err = replayCmd(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "longmemeval:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: longmemeval run|report|replay [flags]; see -h of each")
	os.Exit(2)
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var s settings
	s.register(fs)
	data := fs.String("data", "", "LongMemEval JSON file (longmemeval_s.json)")
	out := fs.String("out", "", "run directory (created; reused to resume)")
	concurrency := fs.Int("concurrency", 8, "questions in parallel")
	limit := fs.Int("limit", 0, "keep the first N selected questions")
	sample := fs.Int("sample", 0, "draw N questions at random")
	stratified := fs.Bool("stratified", false, "spread -sample evenly across the six question types")
	seed := fs.Int64("seed", 1, "sampling seed")
	ids := fs.String("ids", "", "comma-separated question ids")
	types := fs.String("types", "", "comma-separated question types")
	workerAttempts := fs.Int("worker-attempts", 3, "worker process attempts per question (resuming from its checkpoint)")
	_ = fs.Parse(args)
	if err := s.resolveBackend(); err != nil {
		return err
	}
	if *data == "" || *out == "" || s.BaseURL == "" {
		return errors.New("-data, -out and -base-url are required")
	}
	if s.Mode != "om" && s.Mode != "full-context" {
		return fmt.Errorf("unknown -mode %q", s.Mode)
	}
	if s.Backend == "openai" && os.Getenv(APIKeyEnv) == "" {
		return fmt.Errorf("%s is not set", APIKeyEnv)
	}
	if s.Backend == "codex" {
		if _, err := bench.NewCodexAuth(s.CodexAuth); err != nil {
			return err
		}
	}
	questions, err := bench.LoadDataset(*data)
	if err != nil {
		return err
	}
	population := map[string]int{}
	for _, q := range questions {
		population[q.Type]++
	}
	selected := bench.Select(questions, bench.Selection{IDs: split(*ids), Types: split(*types), Limit: *limit, Sample: *sample, Stratified: *stratified, Seed: *seed})
	questions = nil // the unselected haystacks need not stay in memory
	if len(selected) == 0 {
		return errors.New("no questions selected")
	}
	if err := os.MkdirAll(filepath.Join(*out, "questions"), 0o755); err != nil {
		return err
	}
	info := map[string]any{
		"run": filepath.Base(*out), "dataset": filepath.Base(*data), "settings": s, "population": population,
		"concurrency": *concurrency, "selected": len(selected), "started": time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(filepath.Join(*out, "run.json"), info); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var mu sync.Mutex
	done, failed := 0, 0
	sem := make(chan struct{}, max(1, *concurrency))
	var wg sync.WaitGroup
	for _, q := range selected {
		dir := filepath.Join(*out, "questions", q.ID)
		if exists(filepath.Join(dir, "result.json")) {
			mu.Lock()
			done++
			mu.Unlock()
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if !exists(filepath.Join(dir, "question.json")) {
			if err := writeJSON(filepath.Join(dir, "question.json"), q); err != nil {
				return err
			}
		}
		wg.Add(1)
		go func(q bench.Question, dir string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			started := time.Now()
			var err error
			for attempt := 1; attempt <= max(1, *workerAttempts) && ctx.Err() == nil; attempt++ {
				if err = runWorker(ctx, exe, dir, s); err == nil {
					break
				}
				fmt.Fprintf(os.Stderr, "%s: worker attempt %d failed: %v\n", q.ID, attempt, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				fmt.Printf("[fail] %s (%s): %v\n", q.ID, q.Type, err)
				return
			}
			done++
			var r bench.Result
			_ = readJSON(filepath.Join(dir, "result.json"), &r)
			fmt.Printf("[%d/%d] %s %-26s correct=%-5v %4.0fs %s\n", done, len(selected), q.ID, q.Type, r.Correct, time.Since(started).Seconds(), oneLine(r.Hypothesis, 80))
		}(q, dir)
	}
	wg.Wait()
	summary, err := writeReport(*out)
	if err != nil {
		return err
	}
	fmt.Println(summary.Markdown())
	if failed > 0 {
		return fmt.Errorf("%d questions failed; rerun the same command to resume", failed)
	}
	return ctx.Err()
}

func runWorker(ctx context.Context, exe, dir string, s settings) error {
	encoded, err := json.Marshal(s)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "worker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.CommandContext(ctx, exe, "worker", "-dir", dir, "-settings", string(encoded))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w (see %s)", err, filepath.Join(dir, "worker.log"))
	}
	return nil
}

func reportCmd(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	out := fs.String("out", "", "run directory")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("-out is required")
	}
	summary, err := writeReport(*out)
	if err != nil {
		return err
	}
	fmt.Println(summary.Markdown())
	return nil
}

func writeReport(out string) (bench.Summary, error) {
	info := map[string]any{}
	_ = readJSON(filepath.Join(out, "run.json"), &info)
	population := map[string]int{}
	if p, ok := info["population"].(map[string]any); ok {
		for t, v := range p {
			if n, ok := v.(float64); ok {
				population[t] = int(n)
			}
		}
	}
	concurrency := 1
	if c, ok := info["concurrency"].(float64); ok {
		concurrency = int(c)
	}
	if st, ok := info["settings"].(map[string]any); ok {
		info["mode"], info["model"] = st["mode"], st["model"]
	}
	dirs, _ := filepath.Glob(filepath.Join(out, "questions", "*", "result.json"))
	sort.Strings(dirs)
	results := []bench.Result{}
	for _, path := range dirs {
		var r bench.Result
		if err := readJSON(path, &r); err != nil {
			return bench.Summary{}, err
		}
		results = append(results, r)
	}
	summary := bench.Summarize(results, population, concurrency, info)
	if err := writeJSON(filepath.Join(out, "summary.json"), summary); err != nil {
		return summary, err
	}
	return summary, os.WriteFile(filepath.Join(out, "summary.md"), []byte(summary.Markdown()), 0o644)
}

// replayCmd rebuilds each finished question's memory from its saved
// checkpoint with the current code: the drop floor is applied to the recorded
// drops (bench.ApplyDropFloor) and the summary is rendered by the current
// renderer. It makes no model calls. For each question it writes the new
// actor prompt and memory and reports whether the prompt changed and whether a
// short gold answer now appears in it, so a deterministic change can be
// measured on exactly the memory a model built before.
func replayCmd(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	from := fs.String("from", "", "finished run directory to replay")
	out := fs.String("out", "", "directory for the replayed prompts (created)")
	floor := fs.Bool("drop-floor", true, "apply the drop floor to the recorded drops")
	ids := fs.String("ids", "", "comma-separated question ids (default: all with a checkpoint)")
	clip := fs.Bool("clip-overflow", false, "list observations the summary cannot hold in full as clipped lines (ledger.RenderOptions.ClippedOverflow)")
	summaryTokens := fs.Int("summary-tokens", 0, "compactionSummaryMaxTokens for the replayed summary (0: the run's setting)")
	_ = fs.Parse(args)
	if *from == "" || *out == "" {
		return errors.New("-from and -out are required")
	}
	var info struct {
		Settings settings `json:"settings"`
	}
	_ = readJSON(filepath.Join(*from, "run.json"), &info)
	s := info.Settings
	if s.ContextWindow == 0 {
		s.ContextWindow = 128_000
	}
	cfg := bench.WorkerConfig(s.spec(), s.WorkerThinking)
	if *summaryTokens > 0 {
		cfg.CompactionSummaryMaxTokens = *summaryTokens
	}
	dirs, _ := filepath.Glob(filepath.Join(*from, "questions", "*", "checkpoint.json"))
	want := map[string]bool{}
	for _, id := range split(*ids) {
		want[id] = true
	}
	type row struct {
		ID            string            `json:"id"`
		Type          string            `json:"type"`
		Changed       bool              `json:"promptChanged"`
		GoldBefore    *bool             `json:"goldInPromptBefore,omitempty"`
		GoldAfter     *bool             `json:"goldInPromptAfter,omitempty"`
		Stats         bench.ReplayStats `json:"stats"`
		OmittedBefore int               `json:"omittedObservationsBefore"`
		OmittedAfter  int               `json:"omittedObservationsAfter"`
	}
	rows := []row{}
	for _, path := range dirs {
		dir := filepath.Dir(path)
		id := filepath.Base(dir)
		if len(want) > 0 && !want[id] {
			continue
		}
		var q bench.Question
		var cp bench.Checkpoint
		if readJSON(filepath.Join(dir, "question.json"), &q) != nil || readJSON(path, &cp) != nil || cp.SessionsDone < len(q.HaystackSessions) {
			continue
		}
		entries := session.FromMaps(cp.Entries)
		r := row{ID: q.ID, Type: q.Type}
		if *floor {
			entries, r.Stats = bench.ApplyDropFloor(entries, cfg)
		}
		memory := bench.RenderMemoryWithOptions(entries, cfg, s.ContextWindow, ledger.RenderOptions{ClippedOverflow: *clip})
		messages := bench.OMMessages(q, memory, s.Recall)
		qdir := filepath.Join(*out, "questions", q.ID)
		if err := os.MkdirAll(qdir, 0o755); err != nil {
			return err
		}
		if err := writePrompt(filepath.Join(qdir, "actor-prompt.md"), messages); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(qdir, "memory.json"), map[string]any{"memory": memory}); err != nil {
			return err
		}
		before, _ := os.ReadFile(filepath.Join(dir, "actor-prompt.md"))
		after, _ := os.ReadFile(filepath.Join(qdir, "actor-prompt.md"))
		r.Changed = string(before) != string(after)
		var old struct {
			Memory bench.Memory `json:"memory"`
		}
		if readJSON(filepath.Join(dir, "memory.json"), &old) == nil {
			r.OmittedBefore = old.Memory.OmittedObservations
		}
		r.OmittedAfter = memory.OmittedObservations
		if gold := strings.ToLower(strings.TrimSpace(q.Answer())); gold != "" && len(gold) <= 40 && !q.Abstention() {
			b := strings.Contains(strings.ToLower(string(before)), gold)
			a := strings.Contains(strings.ToLower(string(after)), gold)
			r.GoldBefore, r.GoldAfter = &b, &a
		}
		rows = append(rows, r)
	}
	changed, gb, ga, kept := 0, 0, 0, 0
	for _, r := range rows {
		if r.Changed {
			changed++
		}
		if r.GoldBefore != nil && *r.GoldBefore {
			gb++
		}
		if r.GoldAfter != nil && *r.GoldAfter {
			ga++
		}
		kept += r.Stats.KeptDrops
	}
	if err := writeJSON(filepath.Join(*out, "replay.json"), rows); err != nil {
		return err
	}
	fmt.Printf("replayed %d questions: %d prompts changed, %d drops kept by the floor; short gold answers in the prompt: %d before, %d after\n", len(rows), changed, kept, gb, ga)
	return nil
}

// state is a worker's accumulated cost across attempts.
type state struct {
	Usage         bench.Usage `json:"usage"`
	IngestSeconds float64     `json:"ingestSeconds"`
	AnswerSeconds float64     `json:"answerSeconds"`
}

func workerCmd(args []string) error {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	dir := fs.String("dir", "", "question directory")
	raw := fs.String("settings", "", "settings JSON")
	_ = fs.Parse(args)
	var s settings
	if err := json.Unmarshal([]byte(*raw), &s); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	var q bench.Question
	if err := readJSON(filepath.Join(*dir, "question.json"), &q); err != nil {
		return err
	}
	var st state
	_ = readJSON(filepath.Join(*dir, "state.json"), &st)
	meter := bench.NewMeter(st.Usage)
	client, err := s.client(meter, q.ID)
	if err != nil {
		return err
	}
	client.MaxAttempts = s.MaxAttempts
	client.UserAgent = s.UserAgent
	if s.SessionHeader != "" {
		client.Headers = map[string]string{s.SessionHeader: "longmemeval-" + q.ID}
	}
	saveState := func() error {
		st.Usage = meter.Snapshot()
		return writeJSON(filepath.Join(*dir, "state.json"), st)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer func() { _ = saveState() }()

	result := bench.Result{QuestionID: q.ID, QuestionType: q.Type, Abstention: q.Abstention(), Question: q.Question, Answer: q.Answer(), QuestionDate: q.QuestionDate, Mode: s.Mode}
	var messages []bench.ChatMessage
	switch s.Mode {
	case "om":
		memory, ingest, err := ingestStage(ctx, *dir, q, s, client, &st, saveState)
		if err != nil {
			return err
		}
		result.Memory, result.IngestErrors, result.PipelineRuns = &memory, ingest.Errors, ingest.PipelineRuns
		messages = bench.OMMessages(q, memory, s.Recall)
	case "full-context":
		budget := s.ContextWindow - s.ActorMaxTokens - 2_000
		prompt, dropped, err := bench.FullContextPrompt(q, budget)
		if err != nil {
			return err
		}
		result.DroppedSessions = dropped
		messages = []bench.ChatMessage{{Role: "user", Content: bench.Text(prompt)}}
	default:
		return fmt.Errorf("unknown mode %q", s.Mode)
	}

	var answer bench.Answer
	if err := readJSON(filepath.Join(*dir, "answer.json"), &answer); err != nil {
		if err := writePrompt(filepath.Join(*dir, "actor-prompt.md"), messages); err != nil {
			return err
		}
		started := time.Now()
		opts := bench.AnswerOptions{Model: s.Model, MaxTokens: s.ActorMaxTokens, ReasoningEffort: bench.ReasoningEffort(s.ActorThinking)}
		if s.Mode == "om" && s.Recall {
			var checkpoint bench.Checkpoint
			if err := readJSON(filepath.Join(*dir, "checkpoint.json"), &checkpoint); err != nil {
				return fmt.Errorf("recall needs the ingested branch: %w", err)
			}
			answer, answer.Recalls, err = bench.AskWithRecall(ctx, client, messages, session.FromMaps(checkpoint.Entries), opts)
		} else {
			answer, err = bench.Ask(ctx, client, messages, opts)
		}
		st.AnswerSeconds += time.Since(started).Seconds()
		if err != nil {
			return fmt.Errorf("actor: %w", err)
		}
		answer.DroppedSessions = result.DroppedSessions
		if err := writeJSON(filepath.Join(*dir, "answer.json"), answer); err != nil {
			return err
		}
	}
	result.Hypothesis, result.FinishReason, result.Recalls = answer.Hypothesis, answer.FinishReason, answer.Recalls
	started := time.Now()
	verdict, err := bench.Judge(ctx, client, s.Model, s.JudgeMaxTokens, bench.ReasoningEffort(s.JudgeThinking), q, answer.Hypothesis)
	st.AnswerSeconds += time.Since(started).Seconds()
	if err != nil {
		return fmt.Errorf("judge: %w", err)
	}
	result.Correct, result.StrictCorrect, result.JudgeReply = verdict.Correct, verdict.Strict, verdict.Reply
	if err := saveState(); err != nil {
		return err
	}
	result.Usage, result.IngestSeconds, result.AnswerSeconds = st.Usage, st.IngestSeconds, st.AnswerSeconds
	return writeJSON(filepath.Join(*dir, "result.json"), result)
}

// ingestStage ingests the haystack (resuming from checkpoint.json) and renders
// the memory, or reuses memory.json from a finished ingestion.
func ingestStage(ctx context.Context, dir string, q bench.Question, s settings, client *bench.Client, st *state, saveState func() error) (bench.Memory, bench.IngestResult, error) {
	type saved struct {
		Memory bench.Memory       `json:"memory"`
		Ingest bench.IngestResult `json:"ingest"`
	}
	var done saved
	if readJSON(filepath.Join(dir, "memory.json"), &done) == nil {
		return done.Memory, done.Ingest, nil
	}
	cfg := bench.WorkerConfig(s.spec(), s.WorkerThinking)
	opts := bench.IngestOptions{Config: cfg, Spec: s.spec(), Flush: s.Flush}
	var checkpoint bench.Checkpoint
	if readJSON(filepath.Join(dir, "checkpoint.json"), &checkpoint) == nil {
		opts.Resume = &checkpoint
		fmt.Printf("resuming after %d sessions\n", checkpoint.SessionsDone)
	}
	started := time.Now()
	lap := started
	opts.OnSession = func(c bench.Checkpoint) error {
		st.IngestSeconds += time.Since(lap).Seconds()
		lap = time.Now()
		fmt.Printf("%s session %d/%d done (%.0fs)\n", time.Now().UTC().Format(time.TimeOnly), c.SessionsDone, len(q.HaystackSessions), time.Since(started).Seconds())
		if err := writeJSON(filepath.Join(dir, "checkpoint.json"), c); err != nil {
			return err
		}
		return saveState()
	}
	models := &bench.Models{Spec: s.spec(), Client: client}
	ingest, err := bench.Ingest(ctx, q, models, opts)
	st.IngestSeconds += time.Since(lap).Seconds()
	if err != nil {
		return bench.Memory{}, bench.IngestResult{}, err
	}
	memory := bench.RenderMemory(ingest.Entries, cfg, s.ContextWindow)
	fmt.Println("memory:", memory)
	if err := writeJSON(filepath.Join(dir, "checkpoint.json"), bench.Checkpoint{SessionsDone: len(q.HaystackSessions), Entries: entryMaps(ingest)}); err != nil {
		return bench.Memory{}, bench.IngestResult{}, err
	}
	trimmed := bench.IngestResult{Errors: ingest.Errors, PipelineRuns: ingest.PipelineRuns}
	if err := writeJSON(filepath.Join(dir, "memory.json"), saved{Memory: memory, Ingest: trimmed}); err != nil {
		return bench.Memory{}, bench.IngestResult{}, err
	}
	return memory, trimmed, saveState()
}

func entryMaps(r bench.IngestResult) []map[string]any {
	out := make([]map[string]any, 0, len(r.Entries))
	for _, e := range r.Entries {
		out = append(out, e.ToMap())
	}
	return out
}

func writePrompt(path string, messages []bench.ChatMessage) error {
	var b strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", m.Role, m.Text())
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func split(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// writeJSON writes atomically, so an interrupted worker never leaves a torn checkpoint.
func writeJSON(path string, v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
