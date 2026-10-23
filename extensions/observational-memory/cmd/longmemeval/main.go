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
}

func (s *settings) register(fs *flag.FlagSet) {
	fs.StringVar(&s.Mode, "mode", "om", `"om" (observational memory) or "full-context" (raw sessions baseline)`)
	fs.StringVar(&s.Model, "model", "deepseek-v4.1-flash-uncensored", "model for every role (observer, reflector, dropper, actor, judge)")
	fs.StringVar(&s.BaseURL, "base-url", "", "OpenAI-compatible chat-completions endpoint, e.g. https://api.example.com/v1 (required)")
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
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "longmemeval:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: longmemeval run|report [flags]; see -h of each")
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
	if *data == "" || *out == "" || s.BaseURL == "" {
		return errors.New("-data, -out and -base-url are required")
	}
	if s.Mode != "om" && s.Mode != "full-context" {
		return fmt.Errorf("unknown -mode %q", s.Mode)
	}
	if os.Getenv(APIKeyEnv) == "" {
		return fmt.Errorf("%s is not set", APIKeyEnv)
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
	key := os.Getenv(APIKeyEnv)
	if key == "" {
		return fmt.Errorf("%s is not set", APIKeyEnv)
	}
	var q bench.Question
	if err := readJSON(filepath.Join(*dir, "question.json"), &q); err != nil {
		return err
	}
	var st state
	_ = readJSON(filepath.Join(*dir, "state.json"), &st)
	meter := bench.NewMeter(st.Usage)
	client := bench.NewClient(s.BaseURL, key, meter)
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
		opts := bench.AnswerOptions{Model: s.Model, MaxTokens: s.ActorMaxTokens}
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
	verdict, err := bench.Judge(ctx, client, s.Model, s.JudgeMaxTokens, q, answer.Hypothesis)
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
