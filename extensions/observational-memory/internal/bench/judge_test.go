package bench

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJudgePromptPicksTheOfficialTemplate(t *testing.T) {
	cases := []struct {
		id, qtype, marker string
	}{
		{"q1", "single-session-user", "Correct Answer: gold"},
		{"q2", "multi-session", "Correct Answer: gold"},
		{"q3", "temporal-reasoning", "do not penalize off-by-one errors"},
		{"q4", "knowledge-update", "along with an updated answer"},
		{"q5", "single-session-preference", "Rubric: gold"},
		{"q6_abs", "temporal-reasoning", "Explanation: gold"},
	}
	for _, c := range cases {
		q := Question{ID: c.id, Type: c.qtype, Question: "what?", RawAnswer: json.RawMessage(`"gold"`)}
		prompt, err := JudgePrompt(q, "resp")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, c.marker) || !strings.Contains(prompt, "Model Response: resp") || !strings.Contains(prompt, "Question: what?") {
			t.Fatalf("%s: prompt %q lacks %q", c.id, prompt, c.marker)
		}
	}
	if _, err := JudgePrompt(Question{ID: "x", Type: "bogus"}, ""); err == nil {
		t.Fatal("unknown type accepted")
	}
}

func TestVerdictParsing(t *testing.T) {
	cases := []struct {
		reply          string
		correct, stric bool
	}{
		{"yes", true, true},
		{"Yes.", true, true},
		{" YES", true, true},
		{"no", false, false},
		{"No.", false, false},
		{"The answer is yes", true, false},
	}
	for _, c := range cases {
		if got := ParseVerdict(c.reply); got != c.correct {
			t.Errorf("ParseVerdict(%q) = %v", c.reply, got)
		}
		if got := StrictVerdict(c.reply); got != c.stric {
			t.Errorf("StrictVerdict(%q) = %v", c.reply, got)
		}
	}
}
