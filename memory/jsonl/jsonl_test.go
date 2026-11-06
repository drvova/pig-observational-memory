package jsonl

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func appendOK(t *testing.T, path, line string, wantTorn bool) {
	t.Helper()
	torn, err := Append(path, []byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if torn != wantTorn {
		t.Fatalf("Append(%q) torn = %v, want %v", line, torn, wantTorn)
	}
}

func content(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAppendEndsATornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	appendOK(t, path, `{"a":1}`, false)
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	appendOK(t, path, `{"a":2}`, false)
	// A crash cut the next write off: the fragment becomes a line of its own, nothing is
	// truncated, and the new line is whole.
	if err := os.WriteFile(path, []byte(content(t, path)+`{"a":3,"b`), 0o600); err != nil {
		t.Fatal(err)
	}
	appendOK(t, path, `{"a":4}`, true)
	if got, want := content(t, path), "{\"a\":1}\n{\"a\":2}\n{\"a\":3,\"b\n{\"a\":4}\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A whole last line without its newline stays the line it was.
	if err := os.WriteFile(path, []byte(`{"a":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	appendOK(t, path, `{"a":6}`, true)
	if got, want := content(t, path), "{\"a\":5}\n{\"a\":6}\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// An empty file is not torn.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	appendOK(t, path, `{"a":7}`, false)
	if got, want := content(t, path), "{\"a\":7}\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
