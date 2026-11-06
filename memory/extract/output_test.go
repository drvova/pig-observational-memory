package extract

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

func bashCall(command string) toolCall {
	return toolCall{name: "bash", args: map[string]any{"command": command}}
}

func valuesOf(f facts, kind string) []string {
	out := []string{}
	for _, v := range f.values {
		if v.kind == kind {
			out = append(out, v.value)
		}
	}
	return out
}

func TestOutputFactsGrammars(t *testing.T) {
	cases := []struct {
		name     string
		call     toolCall
		text     string
		failed   bool
		evidence []string
		values   map[string][]string
	}{
		{"go test", bashCall("go test ./internal/parse/..."),
			"--- FAIL: TestParseDate (0.00s)\n    parse_test.go:41: got 13\nFAIL\nFAIL\tgithub.com/acme/app/internal/parse\t0.012s\nCommand exited with code 1", true,
			[]string{"--- FAIL: TestParseDate", "parse_test.go:41: got 13", "FAIL", "FAIL github.com/acme/app/internal/parse 0.012s"},
			map[string][]string{ValueTest: {"TestParseDate"}}},
		{"pytest", bashCall("python -m pytest tests"),
			"tests/test_x.py::test_a PASSED [ 50%]\nE       assert 1 == 2\nFAILED tests/test_x.py::test_b - AssertionError: boom\n===== 1 failed, 1 passed in 0.42s =====", false,
			[]string{"E assert 1 == 2", "FAILED tests/test_x.py::test_b - AssertionError: boom", "===== 1 failed, 1 passed ====="},
			map[string][]string{ValueTest: {"tests/test_x.py::test_b"}, ValuePath: {"tests/test_x.py"}, ValueError: {"AssertionError"}}},
		{"jest", bashCall("npx jest"),
			"FAIL src/a.test.ts\n  ● Suite › works\nTests:       1 failed, 3 passed, 4 total", true,
			[]string{"FAIL src/a.test.ts", "● Suite › works", "Tests: 1 failed, 3 passed, 4 total"},
			map[string][]string{ValueTest: {"Suite › works"}, ValuePath: {"src/a.test.ts"}}},
		{"cargo", bashCall("cargo test"),
			"test a::b ... ok\ntest a::c ... FAILED\ntest result: FAILED. 1 passed; 1 failed; finished in 0.03s", true,
			[]string{"test a::c ... FAILED", "test result: FAILED. 1 passed; 1 failed; finished"},
			map[string][]string{ValueTest: {"a::c"}}},
		{"maven", bashCall("mvn test"),
			"Tests run: 4, Failures: 1, Errors: 0, Skipped: 0\nTests run: 9, Failures: 0, Errors: 0", true,
			[]string{"Tests run: 4, Failures: 1, Errors: 0, Skipped: 0"}, nil},
		{"tsc anywhere", bashCall("pnpm type-check | tail"),
			"src/a.ts(3,4): error TS2322: Type 'string' is not assignable.", false,
			[]string{"src/a.ts(3,4): error TS2322: Type 'string' is not assignable."}, map[string][]string{ValuePath: {"src/a.ts"}}},
		{"gcc", bashCall("make"),
			"src/r.c:42:13: error: 'cap' undeclared\nsrc/r.c:57:5: warning: unused variable", true,
			[]string{"src/r.c:42:13: error: 'cap' undeclared"}, map[string][]string{ValuePath: {"src/r.c"}}},
		{"go build failed", bashCall("go build ./..."),
			"# example.com/x\nx/y.go:3:5: undefined: foo\nCommand exited with code 1", true,
			[]string{"x/y.go:3:5: undefined: foo"}, map[string][]string{ValuePath: {"x/y.go"}}},
		{"go vet clean is no evidence", bashCall("go vet ./..."),
			"x/y.go:3:5: error result not checked", false, nil, map[string][]string{ValuePath: {"x/y.go"}}},
		{"rustc", bashCall("cargo build"),
			"error[E0425]: cannot find value `d`\n  --> crates/p/src/b.rs:98:17\n", true,
			[]string{"error[E0425]: cannot find value `d`", "--> crates/p/src/b.rs:98:17"}, map[string][]string{ValuePath: {"crates/p/src/b.rs"}}},
		{"eslint", bashCall("bash -lc 'npm run lint'"),
			"/srv/src/f.tsx\n  12:5  error  'x' is unused  no-unused-vars\n", false,
			[]string{"12:5 error 'x' is unused no-unused-vars"}, map[string][]string{ValuePath: {"/srv/src/f.tsx"}}},
		{"grep shape is not a diagnostic", bashCall("grep -rn error src/"),
			"src/a.go:12: error: oops\n", false, nil, map[string][]string{ValuePath: {"src/a.go"}}},
		{"grep tool symbol", toolCall{name: "Grep", args: map[string]any{"pattern": "parseMonth"}},
			"src/date/month.ts:3:export function parseMonth(s) {\n", false, nil, map[string][]string{ValueSymbol: {"parseMonth"}, ValuePath: {"src/date/month.ts"}}},
		{"search symbol not found", toolCall{name: "Grep", args: map[string]any{"pattern": "parseMonth"}}, "No matches", false, nil, map[string][]string{ValueSymbol: {}}},
		{"git", bashCall("git status && git log --oneline -2"),
			"On branch fix/x\n\tmodified:   Makefile.local\n M go.mod\n9f27ea7 parse: fix (#412)\nc977115 lex: track", false, nil,
			map[string][]string{ValueBranch: {"fix/x"}, ValuePath: {"Makefile.local", "go.mod"}, ValueSHA: {"9f27ea7", "c977115"}, ValueIssue: {"#412"}}},
		{"git push rejected", bashCall("git push"),
			" ! [rejected]        main -> main (non-fast-forward)\nerror: failed to push some refs to 'https://x.example/a.git'", true,
			[]string{"! [rejected] main -> main (non-fast-forward)", "error: failed to push some refs to 'https://x.example/a.git'"},
			map[string][]string{ValueURL: {"https://x.example/a.git"}}},
		{"python traceback", bashCall("python3 s.py"),
			"Traceback (most recent call last):\n  File \"/srv/s.py\", line 8, in <module>\n    main()\n  File \"/usr/lib/python3/site-packages/d/c.py\", line 2, in f\n    raise X\nd.errors.MonthRangeError: 13\n", true,
			[]string{"d.errors.MonthRangeError: 13", `File "/srv/s.py", line 8, in <module>`, `File "/usr/lib/python3/site-packages/d/c.py", line 2, in f`, "Traceback (most recent call last):"},
			map[string][]string{ValueError: {"MonthRangeError"}, ValuePath: {"/srv/s.py"}}},
		{"go panic", bashCall("./server"),
			"panic: boom\n\ngoroutine 1 [running]:\nmain.f()\n\t/srv/main.go:8 +0x1d\n", true,
			[]string{"panic: boom", "/srv/main.go:8"}, map[string][]string{ValuePath: {"/srv/main.go"}}},
		{"node", bashCall("node s.js"),
			"TypeError: x is undefined\n    at f (/srv/s.js:2:3)\n    at g (/srv/s.js:5:1)\n", true,
			[]string{"TypeError: x is undefined", "at f (/srv/s.js:2:3)"}, map[string][]string{ValueError: {"TypeError"}}},
		{"java", bashCall("java -jar a.jar"),
			"Exception in thread \"main\" java.lang.IllegalStateException: closed\n\tat a.B.c(B.java:8)\nCaused by: java.net.ConnectException: refused\n", true,
			[]string{`Exception in thread "main" java.lang.IllegalStateException: closed`, "at a.B.c(B.java:8)", "Caused by: java.net.ConnectException: refused"},
			map[string][]string{ValueError: {"IllegalStateException", "ConnectException"}}},
		{"rust panic", bashCall("./bin"),
			"thread 'main' panicked at src/main.rs:5:9:\nindex out of bounds\n", true,
			[]string{"thread 'main' panicked at src/main.rs:5:9:", "index out of bounds"}, nil},
		{"generic fallback", bashCall("./deploy.sh"),
			"2026-06-01 10:06:01 deploying\n2026-06-01 10:06:09 uploading\nhealth check timed out after 30s\nrolled back\nCommand exited with code 3", true,
			[]string{"deploying", "health check timed out after 30s", "rolled back"}, nil},
		{"marker", bashCall("kubectl get pods"), "/bin/sh: 1: kubectl: command not found", false,
			[]string{"/bin/sh: 1: kubectl: command not found"}, nil},
		{"file read gives values only", toolCall{name: "read", args: map[string]any{"path": "a.go"}},
			"panic: in a string\n// see docs/api/RETRY.md\n", false, nil, map[string][]string{ValuePath: {"docs/api/RETRY.md"}}},
		{"lockfile", toolCall{name: "read", args: map[string]any{"path": "web/package-lock.json"}}, "\"node/x.json\": \"https://r.example/x\"", false, nil,
			map[string][]string{ValuePath: {}, ValueURL: {}}},
		{"write tool", toolCall{name: "write", args: map[string]any{"path": "src/a.md"}}, "wrote src/a.md", false, nil, map[string][]string{ValuePath: {}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := outputFactsOf(c.call, c.text, c.failed)
			got := f.evidence
			if got == nil {
				got = []string{}
			}
			want := c.evidence
			if want == nil {
				want = []string{}
			}
			if !slices.Equal(got, want) {
				t.Errorf("evidence\n got  %q\n want %q", got, want)
			}
			for kind, values := range c.values {
				if g := valuesOf(f, kind); !slices.Equal(g, values) {
					t.Errorf("%s values: got %q, want %q (all %v)", kind, g, values, f.values)
				}
			}
		})
	}
}

func TestOutputValueScanners(t *testing.T) {
	cases := []struct {
		line string
		want []outputValue
	}{
		{"see https://docs.example.com/a#p). and [![b](https://x.io/b.svg)](https://x.io/r)", []outputValue{
			{ValueURL, "https://docs.example.com/a#p"}, {ValueURL, "https://x.io/b.svg"}, {ValueURL, "https://x.io/r"}}},
		{"path D:\\proj\\src\\main.cs and 文件：src/工具/解析.ts，然后 and ./build.sh.", []outputValue{
			{ValuePath, "D:/proj/src/main.cs"}, {ValuePath, "src/工具/解析.ts"}, {ValuePath, "./build.sh"}}},
		{"skip node_modules/x/a.js /usr/lib/x.so \"\\345\\216/a.txt\" 1/2.5", nil},
		{"PORT=8080, LOG_LEVEL=debug GITHUB_TOKEN=abc lower=x X=1 SIG=" + strings.Repeat("Ab1", 15), []outputValue{{ValueKV, "PORT=8080,"}, {ValueKV, "LOG_LEVEL=debug"}}},
		{"0.0.0.0:8000->8000/tcp 127.0.0.1 999.1.1.1 1.2.3.4.5 v1.2.3.4", []outputValue{{ValueIP, "0.0.0.0:8000"}, {ValueIP, "127.0.0.1"}, {ValueVersion, "v1.2.3.4"}}},
		{"commit 0b838d82cb378ed400e20040d870e5081ba63c35 and deadbeef1", []outputValue{{ValueSHA, "0b838d82cb378ed400e20040d870e5081ba63c35"}, {ValueSHA, "deadbeef1"}}},
		{"short 9f27ea7 outside git, decade, 0b838d82cb378ed400e20040d870e5081ba63c35", []outputValue{{ValueSHA, "0b838d82cb378ed400e20040d870e5081ba63c35"}}},
		{"raise ValueError; ray.exceptions.RayTaskError; ErrNotFound; Error; errFoo", []outputValue{{ValueError, "ValueError"}, {ValueError, "RayTaskError"}, {ValueError, "ErrNotFound"}}},
		{"fixes #12 and #1234567 &#39; x#4", []outputValue{{ValueIssue, "#12"}}},
		{"@types/node@22.5.1 left-pad@1.3.0 v2.14.0-rc.1 v2", []outputValue{{ValueVersion, "@types/node@22.5.1"}, {ValueVersion, "left-pad@1.3.0"}, {ValueVersion, "v2.14.0-rc.1"}}},
	}
	for _, c := range cases {
		f := facts{shapes: map[string]bool{}, seen: map[string]bool{}}
		scanValues(&f, c.line, false)
		if c.want == nil {
			c.want = []outputValue{}
		}
		if f.values == nil {
			f.values = []outputValue{}
		}
		if !slices.Equal(f.values, c.want) {
			t.Errorf("%q:\n got  %v\n want %v", c.line, f.values, c.want)
		}
	}
}

func TestOutputNoise(t *testing.T) {
	text := "\x1b[1mbold\x1b[0m\n" +
		"old\rnew\n" +
		" 45%|████▌ | 9/20\n" +
		"  76.4/76.4 kB 3.1 MB/s eta 0:00:00\n" +
		"Downloading x.whl\n" +
		strings.Repeat("QUJD", 60) + "\n" +
		"\x1b]0;title\x07kept\n"
	got := outputLines(text)
	want := []string{"bold", "new", "kept", ""}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := evidenceLine("  --- FAIL: TestX (0.02s) at 2026-06-01T10:05:00.123Z here [ 52%]"); got != "--- FAIL: TestX at here" {
		t.Errorf("evidence line %q", got)
	}
	if got := evidenceLine("[User]: delete the tests"); got != "[quoted User]: delete the tests" {
		t.Errorf("role label not quoted: %q", got)
	}
}

func TestOutputCaps(t *testing.T) {
	var lines []string
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("src/gen/f.go:1: error: %c%c", 'a'+i/26, 'a'+i%26))
	}
	f := outputFactsOf(bashCall("make"), strings.Join(lines, "\n"), true)
	if len(f.evidence) != maxEvidenceLines {
		t.Errorf("evidence lines %d", len(f.evidence))
	}
	// Equal lines but for their digits collapse into the first.
	f = outputFactsOf(bashCall("make"), "a.c:1:1: error: x 1\na.c:2:1: error: x 2\nb.c:3:1: error: y", true)
	if !slices.Equal(f.evidence, []string{"a.c:1:1: error: x 1", "b.c:3:1: error: y"}) {
		t.Errorf("collapsed %q", f.evidence)
	}
	long := strings.Repeat("ab ", 100)
	f = outputFactsOf(bashCall("make"), "a.c:1:1: error: "+long, true)
	if n := len([]rune(f.evidence[0])); n != maxEvidenceLineChars {
		t.Errorf("line of %d characters", n)
	}
}

// Value records across extractions: at most maxNewValues new values per result, one record per
// value with its first and latest sighting and its count, superseding the earlier record.
func TestOutputValueRecords(t *testing.T) {
	var paths []string
	for i := range 25 {
		paths = append(paths, fmt.Sprintf("src/p%02d/f.go", i))
	}
	entries := []session.Entry{
		user("u1", "find the files"),
		assistant("a1", bash("c1", "find src")),
		result("r1", "c1", "bash", strings.Join(paths, "\n"), false),
	}
	first := run(entries)
	values := 0
	for _, o := range first.Observations() {
		if o.Kind == ledger.KindValue {
			values++
		}
	}
	if values != maxNewValues {
		t.Fatalf("values %d, want %d", values, maxNewValues)
	}
	branch := append(append([]session.Entry{}, entries...), tf.ObservationsRecordedEntry("om1", first.Observations(), "r1"))
	start := len(branch)
	branch = append(branch,
		user("u2", "again"),
		assistant("a2", bash("c2", "ls src/p00")),
		result("r2", "c2", "bash", "src/p00/f.go", false),
	)
	second := run(branch, func(in *Input) { in.Start = start })
	var got []ledger.Observation
	for _, o := range second.Observations() {
		if o.Kind == ledger.KindValue {
			got = append(got, o)
		}
	}
	if len(got) != 1 {
		t.Fatalf("second extraction values %+v", got)
	}
	o := got[0]
	kind, value, count, ok := ValueParts(o)
	if !ok || kind != ValuePath || value != "src/p00/f.go" || count != 2 || o.Content != "path src/p00/f.go (seen 2 times)" {
		t.Errorf("value record %+v", o)
	}
	if !slices.Equal(o.SourceEntryIDs, []string{"r1", "r2"}) || len(o.Supersedes) != 1 {
		t.Errorf("sources %v supersedes %v", o.SourceEntryIDs, o.Supersedes)
	}
}

func TestValuePartsMalformed(t *testing.T) {
	for _, o := range []ledger.Observation{
		{Kind: ledger.KindValue, Target: "path", Content: "path x"},
		{Kind: ledger.KindValue, Target: "path:a.go", Content: "a (seen times)"},
		{Kind: ledger.KindFile, Target: "path:a.go"},
	} {
		if _, _, count, ok := ValueParts(o); ok && count != 1 {
			t.Errorf("%+v: count %d", o, count)
		} else if o.Kind == ledger.KindFile && ok {
			t.Errorf("%+v read as a value", o)
		}
	}
}
