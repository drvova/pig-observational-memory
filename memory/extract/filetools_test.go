package extract

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

// files are a result's file records, in order.
func files(r Result) []string {
	out := []string{}
	for _, o := range r.Observations() {
		if o.Kind == ledger.KindFile {
			out = append(out, o.Content)
		}
	}
	return out
}

func wantFiles(t *testing.T, r Result, want ...string) {
	t.Helper()
	if got := files(r); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("files\n%s\nwant\n%s\nall\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), strings.Join(contents(r), "\n"))
	}
}

// Claude Code's file tools, as its transcript reader passes them on (tool_use names and input
// unchanged): Read, Write, Edit and MultiEdit take file_path, NotebookEdit notebook_path. Each
// leaves its state only when its result did not fail.
func TestClaudeCodeFileTools(t *testing.T) {
	ok := func(id, callID, name string) session.Entry { return result(id, callID, name, "ok", false) }
	entries := []session.Entry{
		user("u1", "Tidy the handlers"),
		assistant("a1",
			call{"toolu_01", "Read", map[string]any{"file_path": "/work/app/src/read.go"}},
			call{"toolu_02", "Write", map[string]any{"file_path": "/work/app/src/new.go", "content": "package src\n"}},
			call{"toolu_03", "Edit", map[string]any{"file_path": "/work/app/src/edit.go", "old_string": "a", "new_string": "b", "replace_all": false}},
			call{"toolu_04", "MultiEdit", map[string]any{"file_path": "/work/app/src/multi.go", "edits": []any{map[string]any{"old_string": "a", "new_string": "b"}}}},
			call{"toolu_05", "NotebookEdit", map[string]any{"notebook_path": "/work/app/nb/analysis.ipynb", "cell_id": "c1", "new_source": "x = 1", "edit_mode": "replace"}},
			call{"toolu_06", "Edit", map[string]any{"file_path": "/work/app/src/missing.go", "old_string": "a", "new_string": "b"}},
		),
		ok("t1", "toolu_01", "Read"), ok("t2", "toolu_02", "Write"), ok("t3", "toolu_03", "Edit"), ok("t4", "toolu_04", "MultiEdit"), ok("t5", "toolu_05", "NotebookEdit"),
		result("t6", "toolu_06", "Edit", "<tool_use_error>String to replace not found in file.</tool_use_error>", true),
	}
	r := run(entries)
	wantFiles(t, r, "src/read.go read", "src/new.go written", "src/edit.go edited", "src/multi.go edited", "nb/analysis.ipynb edited")
	absent(t, r, "missing.go edited")
	find(t, r, "attempt", `Edit "src/missing.go" failed: <tool_use_error>String to replace not found`)
	absent(t, r, "NotebookEdit") // a call that worked, without the model's words, is no attempt
}

// Other harnesses' file tools, in the shapes their readers' fixtures hold: Gemini's replace and
// write_file (file_path), Kimi's ReadFile and WriteFile (path), Droid's Create and Edit
// (file_path), Kiro's write with command create or strReplace (path).
func TestOtherHarnessFileTools(t *testing.T) {
	ok := func(id, callID, name string) session.Entry { return result(id, callID, name, "ok", false) }
	entries := []session.Entry{
		user("u1", "Go"),
		assistant("a1",
			call{"g1", "replace", map[string]any{"file_path": "/work/app/g/replace.ts", "old_string": "a", "new_string": "b", "instruction": "fix", "allow_multiple": false}},
			call{"g2", "write_file", map[string]any{"file_path": "/work/app/g/write.ts", "content": "x"}},
			call{"k1", "ReadFile", map[string]any{"path": "k/read.py"}},
			call{"k2", "WriteFile", map[string]any{"path": "k/write.py", "content": "x", "mode": "overwrite"}},
			call{"d1", "Create", map[string]any{"file_path": "/work/app/d/create.md", "content": "x"}},
			call{"d2", "Edit", map[string]any{"file_path": "/work/app/d/edit.md", "old_str": "a", "new_str": "b"}},
			call{"r1", "write", map[string]any{"command": "create", "path": "/work/app/r/create.rs", "content": "x", "__tool_use_purpose": "p"}},
			call{"r2", "write", map[string]any{"command": "strReplace", "path": "/work/app/r/replace.rs", "oldStr": "a", "newStr": "b", "__tool_use_purpose": "p"}},
		),
		ok("t1", "g1", "replace"), ok("t2", "g2", "write_file"), ok("t3", "k1", "ReadFile"), ok("t4", "k2", "WriteFile"),
		ok("t5", "d1", "Create"), ok("t6", "d2", "Edit"), ok("t7", "r1", "write"), ok("t8", "r2", "write"),
	}
	wantFiles(t, run(entries), "g/replace.ts edited", "g/write.ts written", "k/read.py read", "k/write.py written", "d/create.md written", "d/edit.md edited", "r/create.rs written", "r/replace.rs edited")
}

// codexPatchCall is Codex's apply_patch as its reader passes it on: a custom tool call whose
// arguments are the patch itself.
func codexPatchCall(id, callID, patch string) session.Entry {
	return tf.MessageEntry(id, map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
		map[string]any{"type": "toolCall", "id": callID, "name": "apply_patch", "arguments": patch},
	}})
}

// Codex's apply_patch: each path of the patch with its state, only when apply_patch printed its
// success line (the custom tool's output, as the rollout holds it).
func TestCodexApplyPatch(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: package.json\n+{\n+  \"name\": \"app\"\n+}\n" +
		"*** Update File: src/main.jsx\n@@\n-old\n+new\n" +
		"*** Delete File: src/legacy.jsx\n" +
		"*** Update File: src/a.css\n*** Move to: src/b.css\n@@\n-x\n+y\n" +
		"*** End Patch\n"
	success := `{"output":"Success. Updated the following files:\nA package.json\nM src/main.jsx\nD src/legacy.jsx\nM src/b.css\n","metadata":{"exit_code":0,"duration_seconds":0.1}}`
	entries := []session.Entry{
		user("u1", "Scaffold it"),
		codexPatchCall("a1", "call_1", patch),
		result("t1", "call_1", "apply_patch", success, false),
	}
	r := run(entries)
	wantFiles(t, r, "package.json written", "src/main.jsx edited", "src/legacy.jsx deleted", "src/a.css moved to src/b.css", "src/b.css moved from src/a.css")
	if f := find(t, r, "file", "package.json written"); f.Target != "/work/app/package.json" || strings.Join(f.SourceEntryIDs, ",") != "a1,t1" {
		t.Fatalf("file %+v", f)
	}
	absent(t, r, "apply_patch")

	// A patch that did not apply touches nothing, and its attempt names its paths.
	failed := []session.Entry{
		user("u1", "Scaffold it"),
		codexPatchCall("a1", "call_1", "*** Begin Patch\n*** Update File: src/main.jsx\n@@\n-gone\n+new\n*** End Patch\n"),
		result("t1", "call_1", "apply_patch", "apply_patch verification failed: Failed to find expected lines in src/main.jsx", false),
	}
	r = run(failed)
	wantFiles(t, r)
	find(t, r, "attempt", `apply_patch "src/main.jsx" failed: apply_patch verification failed`)

	// A hunk line that reads like a marker is content, not a path.
	inner := "*** Begin Patch\n*** Add File: notes.md\n+*** Delete File: keep.md\n*** End Patch"
	r = run([]session.Entry{user("u1", "x"), codexPatchCall("a1", "c1", inner), result("t1", "c1", "apply_patch", "Success. Updated the following files:\nA notes.md\n", false)})
	wantFiles(t, r, "notes.md written")
}

// apply_patch run through a shell: Codex's argv form and a heredoc script.
func TestCodexApplyPatchThroughShell(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: app.py\n@@\n-a\n+b\n*** End Patch\n"
	out := "Chunk ID: 1\nWall time: 0.0 seconds\nProcess exited with code 0\nOutput:\nSuccess. Updated the following files:\nM app.py\n"
	entries := []session.Entry{
		user("u1", "x"),
		assistant("a1",
			call{"c1", "shell", map[string]any{"command": []any{"apply_patch", patch}}},
			call{"c2", "exec_command", map[string]any{"cmd": "apply_patch <<'EOF'\n*** Begin Patch\n*** Add File: b.py\n+x\n*** End Patch\nEOF\n", "workdir": "/work/app"}},
			call{"c3", "shell", map[string]any{"command": []any{"bash", "-lc", "apply_patch <<'EOF'\n*** Begin Patch\n*** Delete File: c.py\n*** End Patch\nEOF"}}},
			call{"c4", "exec_command", map[string]any{"cmd": "cat <<'EOF'\n*** Begin Patch\n*** Delete File: d.py\n*** End Patch\nEOF"}},
		),
		result("t1", "c1", "shell", out, false), result("t2", "c2", "exec_command", out, false), result("t3", "c3", "shell", out, false), result("t4", "c4", "exec_command", out, false),
	}
	wantFiles(t, run(entries), "app.py edited", "b.py written", "c.py deleted")
}

// A shell result that reports its exit as "Process exited with code N" (Codex's exec_command,
// and the bracketed form other harnesses write) fails on a nonzero code even when the tool
// result is not marked an error, and its failure line skips that note.
func TestProcessExitCode(t *testing.T) {
	entries := []session.Entry{
		user("u1", "Run the tests"),
		assistant("a1", call{"c1", "exec_command", map[string]any{"cmd": "go test ./..."}}),
		result("t1", "c1", "exec_command", "Chunk ID: 1f\nProcess exited with code 1\nOutput:\n--- FAIL: TestX (0.00s)", false),
		assistant("a2", call{"c2", "shell", map[string]any{"command": "make lint"}}),
		result("t2", "c2", "shell", "lint.go:3: unused x\r\n\n\n[Process exited with code 2]", false),
		assistant("a3", call{"c3", "shell", map[string]any{"command": "echo hi"}}),
		result("t3", "c3", "shell", "hi\r\n\n\n[Process exited with code 0]", false),
	}
	r := run(entries)
	find(t, r, "attempt", `exec_command "go test ./..." failed: Chunk ID: 1f`)
	find(t, r, "attempt", `shell "make lint" failed: lint.go:3: unused x`)
	absent(t, r, `"echo hi" failed`)
}
