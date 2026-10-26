package sandbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// A crash leaves the extension running and returns crashed with the child's fatal line, and
// the next call works with the saved definitions. The goroutine cases bypass the policy, which
// refuses go statements.
func TestCrashesLeaveTheExtensionRunning(t *testing.T) {
	units := []Component{{Units: []Unit{{Name: "seven", Src: "func seven() int { return 7 }"}}}}
	cases := []struct {
		name   string
		chunks []wireChunk
		fatal  string
	}{
		{"out of memory", []wireChunk{stmt("var xs [][]byte"), stmt("for { xs = append(xs, bytes.Repeat([]byte{1}, 1<<30)) }")}, ""},
		{"stack overflow", []wireChunk{decl("func deep(n int) int { return deep(n+1) + 1 }", "deep"), stmt("deep(0)")}, "stack"},
		{"concurrent map writes", []wireChunk{stmt("m := map[int]int{}"), stmt("for i := 0; i < 4; i++ { go func() { for j := 0; ; j++ { m[j%64] = j } }() }"), stmt("for j := 0; ; j++ { m[j%64] = j }")}, "concurrent map"},
		{"goroutine panic", []wireChunk{stmt("go func() { panic(\"boom in a goroutine\") }()"), stmt("for {}")}, "boom in a goroutine"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "out of memory" && testing.Short() {
				t.Skip("allocates until the child is killed")
			}
			s := newSup(t, Config{Units: func() []Component { return units }})
			ok(t, runCode(t, context.Background(), s, "1", nil))
			pid := livePID(s)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			r := runRaw(ctx, s, c.chunks, false, nil)
			if r.Outcome != OutcomeCrashed || !r.Restarted || !strings.Contains(r.Err, "the child exited") {
				t.Fatalf("%+v", r)
			}
			if c.fatal != "" && !strings.Contains(r.Err, c.fatal) {
				t.Fatalf("the report lacks %q: %s", c.fatal, r.Err)
			}
			if len([]rune(r.Err)) > ResultChars {
				t.Fatalf("the report is %d characters", len([]rune(r.Err)))
			}
			if !gone(pid) {
				t.Fatal("the crashed child still runs")
			}
			r = runCode(t, context.Background(), s, "seven()", nil)
			if ok(t, r); r.Val != "7" {
				t.Fatalf("%+v", r)
			}
			if st := s.Status(); st.LastCrash == "" {
				t.Fatalf("status %+v", st)
			}
		})
	}
}

// A callback from another child generation is refused: the child is killed and the handler
// never sees the call.
func TestCallbackFromAnotherGenerationRefused(t *testing.T) {
	called := false
	h := fake{omwire.MNotes: func(context.Context, []json.RawMessage) (any, error) { called = true; return nil, nil }}
	s := newSup(t, Config{Mode: "fixture-forge-gen"})
	r := runRaw(context.Background(), s, []wireChunk{stmt("1")}, false, h)
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, "another child generation") || called {
		t.Fatalf("%+v (called %v)", r, called)
	}
}

// A forged write from a child whose table does not list it kills the child and writes nothing.
func TestForgedWriteKillsTheChild(t *testing.T) {
	called := false
	h := fake{omwire.MSave: func(context.Context, []json.RawMessage) (any, error) { called = true; return "", nil }}
	s := newSup(t, Config{Mode: "fixture-forge-save", Methods: []string{omwire.MNotes, omwire.MHistory}})
	r := runRaw(context.Background(), s, []wireChunk{stmt("1")}, false, h)
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, `"Save"`) || called {
		t.Fatalf("%+v (called %v)", r, called)
	}
}

// A child line over the child-to-host bound kills the child with crashed, and the extension's
// memory stays flat; a call within the line bound but over the argument bound is refused by the
// host whatever the child's stub did.
func TestProtocolBounds(t *testing.T) {
	before := rssKB(t)
	s := newSup(t, Config{Mode: "fixture-endless-line"})
	r := runRaw(context.Background(), s, []wireChunk{stmt("1")}, false, nil)
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, "protocol message over bound") {
		t.Fatalf("%+v", r)
	}
	assertFlat(t, before)

	called := false
	h := fake{omwire.MSave: func(context.Context, []json.RawMessage) (any, error) { called = true; return "", nil }}
	s = newSup(t, Config{Mode: "fixture-big-args"})
	r = runRaw(context.Background(), s, []wireChunk{stmt("1")}, false, h)
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, "over the bound") || called {
		t.Fatalf("%+v (called %v)", r, called)
	}
}

// A child writing far more than one result to stderr still exits and is reported as crashed,
// with the report cut to one result and ending with its last line.
func TestStderrFloodIsReported(t *testing.T) {
	s := newSup(t, Config{Mode: "fixture-stderr-flood"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r := runRaw(ctx, s, []wireChunk{stmt("1")}, false, nil)
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, "fixture flood done") {
		t.Fatalf("%s: %.200s", r.Outcome, r.Err)
	}
	if n := len([]rune(r.Err)); n > ResultChars {
		t.Fatalf("the report is %d characters", n)
	}
}
