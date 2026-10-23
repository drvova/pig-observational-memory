package bench

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Result is one graded question.
type Result struct {
	QuestionID    string  `json:"question_id"`
	QuestionType  string  `json:"question_type"`
	Abstention    bool    `json:"abstention"`
	Question      string  `json:"question"`
	Answer        string  `json:"answer"`
	QuestionDate  string  `json:"question_date"`
	Mode          string  `json:"mode"`
	Hypothesis    string  `json:"hypothesis"`
	FinishReason  string  `json:"finish_reason"`
	Correct       bool    `json:"correct"`
	StrictCorrect bool    `json:"strict_correct"`
	JudgeReply    string  `json:"judge_reply"`
	Usage         Usage   `json:"usage"`
	Memory        *Memory `json:"memory,omitempty"`
	// DroppedSessions counts haystack sessions a full-context prompt left out.
	DroppedSessions int `json:"dropped_sessions,omitempty"`
	// Recalls counts the actor's recall tool calls (with -recall).
	Recalls       int      `json:"recalls,omitempty"`
	IngestErrors  []string `json:"ingest_errors,omitempty"`
	PipelineRuns  int      `json:"pipeline_runs,omitempty"`
	IngestSeconds float64  `json:"ingest_seconds"`
	AnswerSeconds float64  `json:"answer_seconds"`
}

// TypeScore is one category's tally.
type TypeScore struct {
	Correct  int     `json:"correct"`
	Total    int     `json:"total"`
	Accuracy float64 `json:"accuracy"`
}

// Summary is a run's report.
type Summary struct {
	RunInfo map[string]any       `json:"run"`
	Results int                  `json:"results"`
	ByType  map[string]TypeScore `json:"accuracy_by_type"`
	// OverallAccuracy is the unweighted mean of the category accuracies
	// present, Mastra's headline number.
	OverallAccuracy float64 `json:"overall_accuracy"`
	// PlainAccuracy is correct answers over all answered questions.
	PlainAccuracy       float64    `json:"plain_accuracy"`
	StrictOverall       float64    `json:"strict_overall_accuracy"`
	Abstention          *TypeScore `json:"abstention,omitempty"`
	TypesPresent        int        `json:"types_present"`
	Usage               Usage      `json:"usage"`
	UsagePerQuestion    Usage      `json:"usage_per_question"`
	IngestSecondsMean   float64    `json:"ingest_seconds_mean"`
	QuestionSecondsMean float64    `json:"question_seconds_mean"`
	// Extrapolated is usage and time scaled to the population (the full
	// dataset), weighting per-type means by the population's type counts.
	Extrapolated *Extrapolation `json:"extrapolated,omitempty"`
}

// Extrapolation scales the measured cost to the whole dataset.
type Extrapolation struct {
	Questions        int     `json:"questions"`
	PromptTokens     float64 `json:"prompt_tokens"`
	CompletionTokens float64 `json:"completion_tokens"`
	ReasoningTokens  float64 `json:"reasoning_tokens"`
	Calls            float64 `json:"calls"`
	QuestionSeconds  float64 `json:"question_seconds"`
	Concurrency      int     `json:"concurrency"`
	WallHours        float64 `json:"wall_hours"`
	TypeWeighted     bool    `json:"type_weighted"`
}

// Summarize aggregates results. population is the dataset's question count
// per type (nil skips extrapolation).
func Summarize(results []Result, population map[string]int, concurrency int, info map[string]any) Summary {
	s := Summary{RunInfo: info, Results: len(results), ByType: map[string]TypeScore{}, Usage: Usage{}, UsagePerQuestion: Usage{}}
	correct := 0
	strictByType := map[string]int{}
	var ingestSeconds, questionSeconds float64
	for _, r := range results {
		score := s.ByType[r.QuestionType]
		score.Total++
		if r.Correct {
			score.Correct++
			correct++
		}
		if r.StrictCorrect {
			strictByType[r.QuestionType]++
		}
		s.ByType[r.QuestionType] = score
		if r.Abstention {
			if s.Abstention == nil {
				s.Abstention = &TypeScore{}
			}
			s.Abstention.Total++
			if r.Correct {
				s.Abstention.Correct++
			}
		}
		s.Usage.Merge(r.Usage)
		ingestSeconds += r.IngestSeconds
		questionSeconds += r.IngestSeconds + r.AnswerSeconds
	}
	strictSum := 0.0
	for t, score := range s.ByType {
		score.Accuracy = ratio(score.Correct, score.Total)
		s.ByType[t] = score
		s.OverallAccuracy += score.Accuracy
		strictSum += ratio(strictByType[t], score.Total)
	}
	if s.Abstention != nil {
		s.Abstention.Accuracy = ratio(s.Abstention.Correct, s.Abstention.Total)
	}
	s.TypesPresent = len(s.ByType)
	if s.TypesPresent > 0 {
		s.OverallAccuracy /= float64(s.TypesPresent)
		s.StrictOverall = strictSum / float64(s.TypesPresent)
	}
	s.PlainAccuracy = ratio(correct, len(results))
	if len(results) == 0 {
		return s
	}
	n := float64(len(results))
	for role, u := range s.Usage {
		s.UsagePerQuestion[role] = scaleUsage(u, 1/n)
	}
	s.IngestSecondsMean = ingestSeconds / n
	s.QuestionSecondsMean = questionSeconds / n
	if len(population) > 0 {
		s.Extrapolated = extrapolate(results, population, concurrency)
	}
	return s
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func scaleUsage(u RoleUsage, f float64) RoleUsage {
	round := func(v int) int { return int(math.Round(float64(v) * f)) }
	return RoleUsage{
		Calls: round(u.Calls), FailedCalls: round(u.FailedCalls), Retries: round(u.Retries),
		PromptTokens: round(u.PromptTokens), CompletionTokens: round(u.CompletionTokens),
		ReasoningTokens: round(u.ReasoningTokens), CachedTokens: round(u.CachedTokens), LatencyMs: int64(math.Round(float64(u.LatencyMs) * f)),
	}
}

// extrapolate weights each sampled type's per-question means by the
// population's count of that type; when the sample misses a type, it falls
// back to the overall per-question mean for it.
func extrapolate(results []Result, population map[string]int, concurrency int) *Extrapolation {
	type acc struct {
		n                                    int
		prompt, completion, reasoning, calls float64
		seconds                              float64
	}
	byType := map[string]*acc{}
	all := &acc{}
	for _, r := range results {
		t := r.Usage.Total()
		for _, a := range []*acc{all, getAcc(byType, r.QuestionType, func() *acc { return &acc{} })} {
			a.n++
			a.prompt += float64(t.PromptTokens)
			a.completion += float64(t.CompletionTokens)
			a.reasoning += float64(t.ReasoningTokens)
			a.calls += float64(t.Calls)
			a.seconds += r.IngestSeconds + r.AnswerSeconds
		}
	}
	e := &Extrapolation{Concurrency: max(1, concurrency), TypeWeighted: true}
	for t, count := range population {
		a := byType[t]
		if a == nil || a.n == 0 {
			a = all
			e.TypeWeighted = false
		}
		f := float64(count) / float64(a.n)
		e.Questions += count
		e.PromptTokens += a.prompt * f
		e.CompletionTokens += a.completion * f
		e.ReasoningTokens += a.reasoning * f
		e.Calls += a.calls * f
		e.QuestionSeconds += a.seconds * f
	}
	e.WallHours = e.QuestionSeconds / float64(e.Concurrency) / 3600
	return e
}

func getAcc[T any](m map[string]T, key string, make func() T) T {
	v, ok := m[key]
	if !ok {
		v = make()
		m[key] = v
	}
	return v
}

// Bar is Mastra's 20-cell accuracy bar.
func Bar(accuracy float64) string {
	filled := int(math.Round(accuracy * 20))
	filled = max(0, min(20, filled))
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", 20-filled) + "]"
}

func pct(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }

// Markdown renders the summary in Mastra's report layout plus usage tables.
func (s Summary) Markdown() string {
	var b strings.Builder
	str := func(key string) string {
		if v, ok := s.RunInfo[key]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	fmt.Fprintf(&b, "# LongMemEval: %s\n\n", str("run"))
	fmt.Fprintf(&b, "```\nDataset: %s\nMode: %s\nModel: %s (Actor) / %s (Observer, Reflector, Dropper) / %s (Judge)\n", str("dataset"), str("mode"), str("model"), str("model"), str("model"))
	b.WriteString(strings.Repeat("─", 52) + "\nAccuracy by Question Type:\n")
	types := make([]string, 0, len(s.ByType))
	for t := range s.ByType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		score := s.ByType[t]
		fmt.Fprintf(&b, "%-26s %6s %s (%d/%d)\n", t, pct(score.Accuracy), Bar(score.Accuracy), score.Correct, score.Total)
	}
	fmt.Fprintf(&b, "\nOverall Accuracy: %.2f%% (average of %d question types)\n```\n\n", s.OverallAccuracy*100, s.TypesPresent)
	fmt.Fprintf(&b, "- Plain accuracy (correct / questions): %s (%d questions)\n", pct(s.PlainAccuracy), s.Results)
	if s.Abstention != nil {
		fmt.Fprintf(&b, "- Abstention (`_abs`, counted within their types above): %s (%d/%d)\n", pct(s.Abstention.Accuracy), s.Abstention.Correct, s.Abstention.Total)
	}
	fmt.Fprintf(&b, "- Overall with Mastra's strict verdict parsing (reply is `yes` or starts with `yes.`): %.2f%%\n", s.StrictOverall*100)
	if s.TypesPresent < len(QuestionTypes) {
		fmt.Fprintf(&b, "- Only %d of %d question types are present; the overall is not comparable to a full run.\n", s.TypesPresent, len(QuestionTypes))
	}
	fmt.Fprintf(&b, "- Judge: %s, not gpt-4o as in Mastra's and LongMemEval's published numbers, so scores are not directly comparable.\n\n", str("model"))

	b.WriteString("## Token usage\n\n| Role | Calls | Retries | Failed | Prompt | Completion | Reasoning | Cached | Prompt / question | Completion / question |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	roles := make([]string, 0, len(s.Usage))
	for role := range s.Usage {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		u, per := s.Usage[role], s.UsagePerQuestion[role]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n", role, u.Calls, u.Retries, u.FailedCalls, u.PromptTokens, u.CompletionTokens, u.ReasoningTokens, u.CachedTokens, per.PromptTokens, per.CompletionTokens)
	}
	t := s.Usage.Total()
	fmt.Fprintf(&b, "| **total** | %d | %d | %d | %d | %d | %d | %d | | |\n\n", t.Calls, t.Retries, t.FailedCalls, t.PromptTokens, t.CompletionTokens, t.ReasoningTokens, t.CachedTokens)
	fmt.Fprintf(&b, "Mean time per question: %.0fs (ingestion %.0fs).\n", s.QuestionSecondsMean, s.IngestSecondsMean)
	if e := s.Extrapolated; e != nil {
		weighting := "per-type means weighted by the dataset's type counts"
		if !e.TypeWeighted {
			weighting = "per-type means where sampled, the overall mean for unsampled types"
		}
		fmt.Fprintf(&b, "\n## Extrapolated to %d questions\n\n%s:\n\n- Prompt tokens: %.1fM\n- Completion tokens: %.1fM (reasoning %.1fM)\n- Model calls: %.0f\n- Sequential time: %.1f h; at concurrency %d: ~%.1f h wall\n",
			e.Questions, weighting, e.PromptTokens/1e6, e.CompletionTokens/1e6, e.ReasoningTokens/1e6, e.Calls, e.QuestionSeconds/3600, e.Concurrency, e.WallHours)
	}
	return b.String()
}
