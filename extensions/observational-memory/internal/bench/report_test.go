package bench

import (
	"math"
	"strings"
	"testing"
)

func TestSummarizeAveragesCategoriesUnweighted(t *testing.T) {
	usage := Usage{RoleObserver: {Calls: 2, PromptTokens: 1000, CompletionTokens: 100}}
	results := []Result{
		{QuestionID: "a", QuestionType: "multi-session", Correct: true, StrictCorrect: true, Usage: usage, IngestSeconds: 10},
		{QuestionID: "b", QuestionType: "multi-session", Correct: false, Usage: usage, IngestSeconds: 10},
		{QuestionID: "c", QuestionType: "multi-session", Correct: false, Usage: usage, IngestSeconds: 10},
		{QuestionID: "d_abs", QuestionType: "temporal-reasoning", Abstention: true, Correct: true, Usage: usage, IngestSeconds: 10},
	}
	s := Summarize(results, map[string]int{"multi-session": 30, "temporal-reasoning": 10}, 4, map[string]any{"run": "r", "model": "m"})
	if got := s.ByType["multi-session"]; got.Correct != 1 || got.Total != 3 {
		t.Fatalf("multi-session = %+v", got)
	}
	// Overall is the mean of 1/3 and 1/1, not 2/4.
	if math.Abs(s.OverallAccuracy-(1.0/3+1)/2) > 1e-9 || s.PlainAccuracy != 0.5 {
		t.Fatalf("overall = %v plain = %v", s.OverallAccuracy, s.PlainAccuracy)
	}
	if s.Abstention == nil || s.Abstention.Correct != 1 || s.Abstention.Total != 1 {
		t.Fatalf("abstention = %+v", s.Abstention)
	}
	if s.Usage[RoleObserver].PromptTokens != 4000 || s.UsagePerQuestion[RoleObserver].PromptTokens != 1000 {
		t.Fatalf("usage = %+v", s.Usage)
	}
	e := s.Extrapolated
	if e == nil || e.Questions != 40 || e.PromptTokens != 40_000 || !e.TypeWeighted || math.Abs(e.WallHours-400.0/4/3600) > 1e-9 {
		t.Fatalf("extrapolated = %+v", e)
	}
	md := s.Markdown()
	for _, want := range []string{"multi-session", "33.3% [███████░░░░░░░░░░░░░] (1/3)", "Overall Accuracy: 66.67% (average of 2 question types)", "not gpt-4o", "| observer |"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, md)
		}
	}
}

func TestBar(t *testing.T) {
	if Bar(1) != "["+strings.Repeat("█", 20)+"]" || Bar(0) != "["+strings.Repeat("░", 20)+"]" {
		t.Fatal("bar ends")
	}
}
