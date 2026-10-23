// Package bench runs the LongMemEval benchmark against the extension's real
// memory pipeline: haystack sessions are fed through the observer, reflector
// and dropper, the rendered memory answers the question, and LongMemEval's
// judge prompts grade the answer. cmd/longmemeval is its command line.
package bench

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"time"
)

// QuestionTypes are LongMemEval's six categories, in report order.
var QuestionTypes = []string{
	"knowledge-update",
	"multi-session",
	"single-session-assistant",
	"single-session-preference",
	"single-session-user",
	"temporal-reasoning",
}

// Turn is one haystack chat turn.
type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Question is one LongMemEval item.
type Question struct {
	ID                 string          `json:"question_id"`
	Type               string          `json:"question_type"`
	Question           string          `json:"question"`
	RawAnswer          json.RawMessage `json:"answer"`
	QuestionDate       string          `json:"question_date"`
	HaystackDates      []string        `json:"haystack_dates"`
	HaystackSessionIDs []string        `json:"haystack_session_ids"`
	HaystackSessions   [][]Turn        `json:"haystack_sessions"`
	AnswerSessionIDs   []string        `json:"answer_session_ids"`
}

// Answer is the gold answer as text (a few answers are JSON numbers).
func (q Question) Answer() string {
	var s string
	if json.Unmarshal(q.RawAnswer, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(q.RawAnswer))
}

// Abstention reports an unanswerable question, which LongMemEval marks with an
// "_abs" id suffix and grades with its own judge prompt.
func (q Question) Abstention() bool { return strings.HasSuffix(q.ID, "_abs") }

// DateLayout is LongMemEval's timestamp format, e.g. "2023/05/30 (Tue) 23:40".
const DateLayout = "2006/01/02 (Mon) 15:04"

// ParseDate reads a LongMemEval timestamp as a wall-clock time in UTC.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(DateLayout, strings.TrimSpace(s))
	if err != nil {
		// Tolerate a missing weekday.
		if t2, err2 := time.Parse("2006/01/02 15:04", strings.TrimSpace(s)); err2 == nil {
			return t2, nil
		}
		return time.Time{}, fmt.Errorf("parse date %q: %w", s, err)
	}
	return t, nil
}

// Session is one haystack session with its parsed date.
type Session struct {
	Index int
	Date  time.Time
	Turns []Turn
}

// ChronologicalSessions are the haystack sessions oldest first. The source
// order breaks ties, and longmemeval_s is not always sorted.
func (q Question) ChronologicalSessions() ([]Session, error) {
	if len(q.HaystackDates) != len(q.HaystackSessions) {
		return nil, fmt.Errorf("question %s: %d dates for %d sessions", q.ID, len(q.HaystackDates), len(q.HaystackSessions))
	}
	sessions := make([]Session, 0, len(q.HaystackSessions))
	for i, turns := range q.HaystackSessions {
		date, err := ParseDate(q.HaystackDates[i])
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, Session{Index: i, Date: date, Turns: turns})
	}
	sort.SliceStable(sessions, func(a, b int) bool { return sessions[a].Date.Before(sessions[b].Date) })
	return sessions, nil
}

// LoadDataset reads a LongMemEval JSON file (a top-level array).
func LoadDataset(path string) ([]Question, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var questions []Question
	if err := json.NewDecoder(f).Decode(&questions); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return questions, nil
}

// Selection picks the questions to run.
type Selection struct {
	// IDs, when set, picks exactly these questions (in dataset order).
	IDs []string
	// Limit keeps the first Limit questions after the other filters.
	Limit int
	// Sample draws Sample questions at random (seeded).
	Sample int
	// Stratified spreads the sample evenly across the six question types.
	Stratified bool
	Seed       int64
	// Types restricts the pool to these question types.
	Types []string
}

// Select applies a selection, keeping dataset order in the result.
func Select(questions []Question, sel Selection) []Question {
	pool := questions
	if len(sel.IDs) > 0 || len(sel.Types) > 0 {
		ids := setOf(sel.IDs)
		types := setOf(sel.Types)
		pool = nil
		for _, q := range questions {
			if (len(ids) == 0 || ids[q.ID]) && (len(types) == 0 || types[q.Type]) {
				pool = append(pool, q)
			}
		}
	}
	if sel.Sample > 0 && sel.Sample < len(pool) {
		rng := rand.New(rand.NewSource(sel.Seed))
		picked := map[string]bool{}
		if sel.Stratified {
			for _, id := range stratifiedSample(pool, sel.Sample, rng) {
				picked[id] = true
			}
		} else {
			for _, i := range rng.Perm(len(pool))[:sel.Sample] {
				picked[pool[i].ID] = true
			}
		}
		kept := []Question{}
		for _, q := range pool {
			if picked[q.ID] {
				kept = append(kept, q)
			}
		}
		pool = kept
	}
	if sel.Limit > 0 && sel.Limit < len(pool) {
		pool = pool[:sel.Limit]
	}
	return pool
}

// stratifiedSample deals n picks round-robin over the question types (in a
// seeded order), each type drawing its questions in a seeded order, so every
// type gets floor(n/6) or one more while it has questions left.
func stratifiedSample(pool []Question, n int, rng *rand.Rand) []string {
	byType := map[string][]string{}
	for _, q := range pool {
		byType[q.Type] = append(byType[q.Type], q.ID)
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	rng.Shuffle(len(types), func(i, j int) { types[i], types[j] = types[j], types[i] })
	for _, t := range types {
		ids := byType[t]
		rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	}
	picked := []string{}
	for round := 0; len(picked) < n; round++ {
		progressed := false
		for _, t := range types {
			if len(picked) == n {
				break
			}
			if round < len(byType[t]) {
				picked = append(picked, byType[t][round])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return picked
}

func setOf(items []string) map[string]bool {
	set := map[string]bool{}
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			set[item] = true
		}
	}
	return set
}
