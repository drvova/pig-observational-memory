package transcript

// The readers' parity harness of the Lisp port (docs/lisp-core.md, W2; docs/lisp-port-readers-store.md):
// every real transcript is read in every format by Go and by lisp/readers-parity.lisp, the SQLite
// dumps and assembled rows by each rows reader, and stored entries are merged into a transcript
// (neutral.Branch) and appended (neutral.Store.Append) by both; each output is compared line by
// line, byte for byte. It runs only with OM_LISP_PARITY=1 and sbcl on PATH, so CI needs no SBCL.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/session"
)

func lispReaders(t *testing.T) (sbcl, script string) {
	t.Helper()
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to diff the Go readers against the SBCL port in lisp/")
	}
	sbcl, err := exec.LookPath("sbcl")
	if err != nil {
		t.Skip("sbcl is not on PATH")
	}
	script, err = filepath.Abs(filepath.Join("..", "..", "lisp", "readers-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	return sbcl, script
}

func runLisp(t *testing.T, sbcl, script string, args ...string) []string {
	t.Helper()
	cmd := exec.Command(sbcl, append([]string{"--script", script}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sbcl %v: %v\n%s", args, err, stderr.String())
	}
	if len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

func entryLines(t *testing.T, es []session.Entry) []string {
	t.Helper()
	var out []string
	for _, e := range es {
		b, err := json.Marshal(e.ToMap())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, "entry "+string(b))
	}
	return out
}

func diffReaderLines(t *testing.T, what string, want, got []string) int {
	t.Helper()
	entries := 0
	for i := 0; i < len(want) || i < len(got); i++ {
		w, g := "", ""
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			t.Fatalf("%s: line %d differs (Go %d lines, Lisp %d lines):\n go:   %.1500s\n lisp: %.1500s", what, i+1, len(want), len(got), w, g)
		}
		if strings.HasPrefix(w, "entry ") {
			entries++
		}
	}
	return entries
}

func realFixtures(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "real", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range paths {
		if ext := filepath.Ext(p); ext == ".jsonl" || ext == ".json" {
			out = append(out, p)
		}
	}
	return out
}

// Every real transcript, read in every format (the right one and all the wrong ones, which
// exercise each reader on shapes it was not written for).
func TestLispReadersParity(t *testing.T) {
	sbcl, script := lispReaders(t)
	var names []string
	for name := range Formats {
		names = append(names, name)
	}
	sort.Strings(names)
	total := 0
	for _, path := range realFixtures(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			lines, err := readLines(path)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, name := range names {
				want = append(want, "format "+name)
				want = append(want, entryLines(t, Formats[name](lines))...)
			}
			n := diffReaderLines(t, path, want, runLisp(t, sbcl, script, "all", path))
			total += n
			t.Logf("identical: %d entries over %d formats", n, len(names))
		})
	}
	t.Logf("identical: %d entries", total)
}

// The SQLite dumps loaded into databases and read through SQLiteCommands, and rows assembled
// from Goose's message samples and an Amazon Q conversation, by each rows reader.
func TestLispReadersParityRows(t *testing.T) {
	sbcl, script := lispReaders(t)
	if py, err := exec.LookPath("python3"); err == nil {
		for _, c := range []struct{ dump, format, session string }{
			{"crush-db.sql", "crush", "05966aef-d75c-4975-9531-9c4be1c9a43b"},
			{"goose-db.sql", "goose", "20261002_1"},
		} {
			db := filepath.Join(t.TempDir(), "s.db")
			load := "import sqlite3, sys\nc = sqlite3.connect(sys.argv[1])\nc.executescript(open(sys.argv[2]).read())\nc.commit()\n"
			if out, err := exec.Command(py, "-I", "-c", load, db, filepath.Join("testdata", "real", c.dump)).CombinedOutput(); err != nil {
				t.Fatalf("%s: %v %s", c.dump, err, out)
			}
			es, err := DB{Path: db, Session: c.session, Kind: DBFormats[c.format]}.Entries()
			if err != nil {
				t.Fatal(err)
			}
			n := diffReaderLines(t, c.dump, entryLines(t, es), runLisp(t, sbcl, script, "db", c.format, db, c.session))
			t.Logf("%s: identical, %d entries", c.dump, n)
		}
	} else {
		t.Log("python3 is needed to load the SQL dumps; skipped them")
	}
	dir := t.TempDir()
	rowsCase := func(name, format string, rows []map[string]any) {
		data, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		var decoded []map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		var es []session.Entry
		switch format {
		case "crush":
			es = CrushRows(decoded)
		case "goose":
			es = GooseRows(decoded)
		case "amazon-q":
			es = AmazonQRows(decoded)
		}
		n := diffReaderLines(t, name, entryLines(t, es), runLisp(t, sbcl, script, "rows", format, p))
		t.Logf("%s: identical, %d entries", name, n)
	}
	var goose []map[string]any
	for i, n := range []string{"3", "4", "5"} {
		data, err := os.ReadFile(filepath.Join("testdata", "real", "goose-message-"+n+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		content, _ := json.Marshal(m["content"])
		meta, _ := json.Marshal(m["metadata"])
		goose = append(goose, map[string]any{"message_id": "msg_" + n, "role": m["role"], "content_json": string(content), "metadata_json": string(meta), "created_timestamp": float64(1793787302 + i)})
	}
	goose = append(goose, map[string]any{"message_id": "msg_6", "role": "user", "created_timestamp": 1793787310.9,
		"content_json":  `[{"type":"toolResponse","id":"tool123","toolResult":{"status":"error","error":{"code":-32603,"message":"boom","data":[1,2.5,null,true]}}}]`,
		"metadata_json": `{"userVisible":true}`})
	goose = append(goose, map[string]any{"message_id": "msg_7", "role": "user", "content_json": `[{"type":"text","text":"<turn-context>now</turn-context>"}]`, "metadata_json": `{"userVisible":false}`})
	rowsCase("goose-messages", "goose", goose)
	state := map[string]any{"conversation_id": "conv-1", "history": []any{
		map[string]any{"user": map[string]any{"timestamp": "2026-11-04T10:15:02.000Z", "content": map[string]any{"Prompt": map[string]any{"prompt": "List the Go files"}}},
			"assistant": map[string]any{"ToolUse": map[string]any{"message_id": "m1", "content": "Listing.", "tool_uses": []any{map[string]any{"id": "t1", "name": "execute_bash", "args": map[string]any{"command": "ls *.go"}}}}}},
		map[string]any{"user": map[string]any{"timestamp": "2026-11-04T10:15:05.000Z", "content": map[string]any{"ToolUseResults": map[string]any{"tool_use_results": []any{
			map[string]any{"tool_use_id": "t1", "status": "Success", "content": []any{map[string]any{"Text": "main.go"}, map[string]any{"Json": map[string]any{"exit": 0.0, "html": "<b>&</b>"}}, map[string]any{"Json": nil}}},
			map[string]any{"tool_use_id": "t2", "status": "Error", "content": []any{map[string]any{"Text": "denied"}}}}}}},
			"assistant": map[string]any{"Response": map[string]any{"content": "There is one file: main.go."}}},
	}}
	value, _ := json.Marshal(state)
	rowsCase("amazon-q-conversation", "amazon-q", []map[string]any{{"key": "/home/dev/app", "value": string(value)}})
	rowsCase("crush-messages", "crush", []map[string]any{
		{"id": "c1", "role": "user", "created_at": 1793787302.7, "is_summary_message": 0.0, "parts": `[{"type":"text","data":{"text":"Run the tests"}}]`},
		{"id": "c2", "role": "assistant", "created_at": 1793787303.0, "is_summary_message": 0.0, "parts": `[{"type":"reasoning","data":{"thinking":"go test"}},{"type":"tool_call","data":{"id":"k1","name":"bash","input":"{\"command\":\"go test ./...\"}"}},{"type":"tool_call","data":{"id":"k2","name":"bash","input":"not json"}}]`},
		{"id": "c3", "role": "tool", "created_at": 1793787304.0, "is_summary_message": 0.0, "parts": `[{"type":"tool_result","data":{"tool_call_id":"k1","name":"bash","content":"ok","is_error":false}},{"type":"tool_result","data":{"tool_call_id":"k2","name":"bash","content":"fail","is_error":true}}]`},
		{"id": "c4", "role": "assistant", "created_at": 1793787305.0, "is_summary_message": 1.0, "parts": `[{"type":"text","data":{"text":"summary"}}]`},
		{"id": "c5", "role": "user", "created_at": nil, "parts": `not json`},
	})
}

// Stored entries merged into real transcripts (neutral.Branch), and appended to a store
// (neutral.Store.Append) by both, byte for byte.
func TestLispStoreParity(t *testing.T) {
	sbcl, script := lispReaders(t)
	dir := t.TempDir()
	for _, c := range []struct{ file, format string }{
		{"claude-code-preserved.jsonl", "claude-code"}, {"kimi-wire.jsonl", "kimi"}, {"codex-rollout.jsonl", "codex"},
		{"oh-my-pi-session.jsonl", "pi"}, {"gemini-legacy-session.json", "gemini"},
	} {
		path := filepath.Join("testdata", "real", c.file)
		es, err := File{Path: path, Format: Formats[c.format]}.Entries()
		if err != nil || len(es) < 3 {
			t.Fatalf("%s: %d entries, %v", c.file, len(es), err)
		}
		// After the first entry, an alias of a grouped entry (when the reader made one), a stored
		// entry, the tip, an entry the transcript no longer holds, and the start.
		afterAlias := es[1].ID
		for _, e := range es {
			if a := neutral.Aliases(e); len(a) > 0 {
				afterAlias = a[len(a)-1]
				break
			}
		}
		pairs := []neutral.Stored{
			{After: es[0].ID, Entry: map[string]any{"type": "custom", "id": "x1", "customType": "om.extracted", "data": map[string]any{"through": es[0].ID}}},
			{After: "x1", Entry: map[string]any{"type": "custom", "id": "x2", "customType": "om.observations.recorded", "data": map[string]any{"records": []any{map[string]any{"text": "a <b> &   é", "n": 1.5e-7}}}}},
			{After: afterAlias, Entry: map[string]any{"type": "custom", "id": "x3", "customType": "om.cut", "data": map[string]any{"reason": "auto"}}},
			{After: es[0].ID, Entry: map[string]any{"type": "custom", "id": "x4"}},
			{After: es[len(es)-1].ID, Entry: map[string]any{"type": "compaction", "id": "x5", "summary": "s", "firstKeptEntryId": "x3", "tokensBefore": 1234.0, "details": nil}},
			{After: "gone", Entry: map[string]any{"type": "custom", "id": "x6"}},
			{After: "", Entry: map[string]any{"type": "custom", "id": "x0"}},
		}
		goStore := neutral.Store{Path: filepath.Join(dir, c.format+"-go", "s.jsonl")}
		var pairLines []string
		for _, p := range pairs {
			if err := goStore.Append(p.After, p.Entry); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(p)
			pairLines = append(pairLines, string(b))
		}
		// A line cut off and one without an entry are skipped on load.
		f, err := os.OpenFile(goStore.Path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(f, "{\"after\":\"x1\",\"entry\":{\"id\":\"y1\"}}\n{\"after\":\"x1\"}\n{\"after\":5,\"entry\":{\"id\":\"y2\"}}\n{\"after\":\"x1\",\"ent")
		f.Close()
		stored, err := goStore.Load()
		if err != nil {
			t.Fatal(err)
		}
		n := diffReaderLines(t, c.file+" branch", entryLines(t, neutral.Branch(es, stored)), runLisp(t, sbcl, script, "branch", c.format, path, goStore.Path))
		t.Logf("%s: branch identical, %d entries", c.file, n)

		pairsPath := filepath.Join(dir, c.format+"-pairs.jsonl")
		if err := os.WriteFile(pairsPath, []byte(strings.Join(pairLines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		lispStore := filepath.Join(dir, c.format+"-lisp", "sub", "s.jsonl")
		runLisp(t, sbcl, script, "append", lispStore, pairsPath)
		want, _ := os.ReadFile(goStore.Path)
		got, err := os.ReadFile(lispStore)
		if err != nil {
			t.Fatal(err)
		}
		wantLines := strings.Split(string(want), "\n")[:len(pairs)]
		diffReaderLines(t, c.file+" append", wantLines, strings.Split(strings.TrimSuffix(string(got), "\n"), "\n"))
	}
}
