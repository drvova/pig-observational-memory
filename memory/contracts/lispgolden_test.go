package contracts

// W5's parity check (docs/lisp-port-hooks.md): the golden corpus end to end through the engine
// image. Each fixture runs as TestGolden runs it on the Go engine, a turn end and then the
// harness's compaction (the generic protocol's turn-end and compact), but sent to the image over
// its socket, with the image's clock fixed to the corpus's. The store lines and the summary must
// equal contracts/golden byte for byte after the same id normalization. Like the other Lisp
// checks, it runs only with OM_LISP_PARITY=1 and sbcl on PATH.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lispImage builds the engine image into a temporary directory and starts it on a fresh root
// with the corpus's clock; it returns the root and a function that sends one request.
func lispImage(t *testing.T) (string, func(method string, params map[string]any) (map[string]any, error)) {
	t.Helper()
	lispParity(t)
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "build-image.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	image := filepath.Join(dir, "om-engine")
	if out, err := exec.Command(shell, script, image).CombinedOutput(); err != nil {
		t.Fatalf("build-image: %v\n%s", err, out)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "engine.sock")
	cmd := exec.Command(image, "--root", root, "--socket", socket, "--idle-seconds", "0")
	cmd.Env = append(os.Environ(), "OM_ENGINE_CLOCK="+clock.Format("2006-01-02T15:04:05.000Z"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.Remove(image)
	})
	var conn net.Conn
	for range 500 {
		if conn, err = net.Dial("unix", socket); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the image did not listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	reader := bufio.NewReader(conn)
	id := 0
	return root, func(method string, params map[string]any) (map[string]any, error) {
		id++
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := conn.Write(append(line, '\n')); err != nil {
			return nil, err
		}
		answer, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var res struct {
			Result map[string]any `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(answer, &res); err != nil {
			return nil, err
		}
		if res.Error != nil {
			return nil, fmt.Errorf("%s (%d)", res.Error.Message, res.Error.Code)
		}
		return res.Result, nil
	}
}

func TestLispGolden(t *testing.T) {
	root, call := lispImage(t)
	for _, f := range fixtures {
		t.Run(f.name(), func(t *testing.T) {
			params := map[string]any{"root": root, "harness": "golden", "session_id": f.name(), "transcript_path": f.path(t),
				"format": f.format, "cwd": "/work", "env": map[string]any{}, "reason": "manual", "with_summary": true}
			if _, err := call("turn-end", params); err != nil {
				t.Fatalf("turn-end: %v", err)
			}
			path := filepath.Join(root, "sessions", "golden", f.name()+".jsonl")
			turn, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			res, err := call("compact", params)
			if err != nil {
				t.Fatalf("compact: %v", err)
			}
			all, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text, _ := res["text"].(string)
			g := normalizeIDs(t, golden{records: string(turn), compaction: string(all[len(turn):]), summary: text + "\n"}, all)
			g = sameEngine(g)
			for name, content := range map[string]string{recordsFile: g.records, compactionFile: g.compaction, summaryFile: g.summary} {
				want, err := os.ReadFile(filepath.Join(goldenDir, f.name(), name))
				if err != nil {
					t.Fatal(err)
				}
				if string(want) != content {
					t.Errorf("%s/%s differs from the image's output:\n%s", f.name(), name, firstDiff(string(want), content))
				}
			}
		})
	}
}

// sameEngine reads the image's records as the Go engine's: the one member the engines write
// differently by design is the writer's engine (contracts/encoding.md), so "lisp" is compared as
// "go"; every other byte must be the same.
func sameEngine(g golden) golden {
	swap := func(s string) string { return strings.ReplaceAll(s, `"engine":"lisp"`, `"engine":"go"`) }
	return golden{records: swap(g.records), compaction: swap(g.compaction), summary: swap(g.summary)}
}
