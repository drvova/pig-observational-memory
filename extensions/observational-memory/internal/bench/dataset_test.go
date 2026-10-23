package bench

import (
	"encoding/json"
	"fmt"
	"testing"
)

func question(id, qtype string) Question {
	return Question{ID: id, Type: qtype, RawAnswer: json.RawMessage(`"x"`)}
}

func TestChronologicalSessionsSortsByDateStably(t *testing.T) {
	q := Question{
		ID:               "q",
		HaystackDates:    []string{"2023/05/21 (Sun) 10:00", "2023/05/20 (Sat) 02:21", "2023/05/21 (Sun) 10:00"},
		HaystackSessions: [][]Turn{{{Role: "user", Content: "a"}}, {{Role: "user", Content: "b"}}, {{Role: "user", Content: "c"}}},
	}
	sessions, err := q.ChronologicalSessions()
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	for _, s := range sessions {
		got += s.Turns[0].Content
	}
	if got != "bac" {
		t.Fatalf("order = %q, want bac", got)
	}
	if sessions[0].Date.Format("2006-01-02 15:04") != "2023-05-20 02:21" {
		t.Fatalf("date = %v", sessions[0].Date)
	}
}

func TestAnswerReadsStringsAndNumbers(t *testing.T) {
	if got := (Question{RawAnswer: json.RawMessage(`"Business Administration"`)}).Answer(); got != "Business Administration" {
		t.Fatalf("string answer = %q", got)
	}
	if got := (Question{RawAnswer: json.RawMessage(`3`)}).Answer(); got != "3" {
		t.Fatalf("number answer = %q", got)
	}
	if !(Question{ID: "abc_abs"}).Abstention() || (Question{ID: "abc"}).Abstention() {
		t.Fatal("abstention suffix not detected")
	}
}

func TestStratifiedSampleSpreadsTypesDeterministically(t *testing.T) {
	pool := []Question{}
	for _, qtype := range QuestionTypes {
		for i := range 10 {
			pool = append(pool, question(fmt.Sprintf("%s-%d", qtype, i), qtype))
		}
	}
	first := Select(pool, Selection{Sample: 8, Stratified: true, Seed: 7})
	if len(first) != 8 {
		t.Fatalf("len = %d", len(first))
	}
	counts := map[string]int{}
	for _, q := range first {
		counts[q.Type]++
	}
	if len(counts) != 6 {
		t.Fatalf("types = %v, want all six", counts)
	}
	for qtype, n := range counts {
		if n < 1 || n > 2 {
			t.Fatalf("type %s got %d", qtype, n)
		}
	}
	again := Select(pool, Selection{Sample: 8, Stratified: true, Seed: 7})
	for i := range first {
		if first[i].ID != again[i].ID {
			t.Fatal("same seed gave a different sample")
		}
	}
}

func TestSelectByIDsTypesAndLimit(t *testing.T) {
	pool := []Question{question("a", "multi-session"), question("b", "temporal-reasoning"), question("c", "multi-session")}
	if got := Select(pool, Selection{IDs: []string{"c", "a"}}); len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Fatalf("ids = %v", got)
	}
	if got := Select(pool, Selection{Types: []string{"multi-session"}, Limit: 1}); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("types+limit = %v", got)
	}
}
