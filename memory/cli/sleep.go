package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/drvova/pig-observational-memory/memory/filelock"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// om sleep runs the sleep pass by hand (docs/universal.md, "om sleep"; docs/brain-memory.md,
// decision 13): the pass the image runs at its idle (lisp/src/server/sleep.lisp), under the same
// lock, <root>/sleep/.lock, with the same checkpoint file. It makes no model request. With
// --harness or --dir it sleeps the current session of that harness and directory, its transcript
// and repository read too, so its project records are verified against HEAD; without, every
// session store under the root, each from its store alone.

// SleepLockPath is the sleep pass's lock under root, which the image's idle child takes too.
func SleepLockPath(root string) string { return filepath.Join(root, "sleep", ".lock") }

// SleepStatePath is the pass's checkpoint: each store's size when it was last slept.
func SleepStatePath(root string) string { return filepath.Join(root, "sleep", "state.json") }

// sleepState is the checkpoint file's content (contracts/schemas/sleep-state.schema.json).
type sleepState struct {
	Version int              `json:"version"`
	Stores  map[string]int64 `json:"stores"`
}

func readSleepState(root string) sleepState {
	st := sleepState{Version: 1, Stores: map[string]int64{}}
	data, err := os.ReadFile(SleepStatePath(root))
	if err == nil {
		var read sleepState
		if json.Unmarshal(data, &read) == nil && read.Stores != nil {
			st.Stores = read.Stores
		}
	}
	return st
}

func writeSleepState(root string, st sleepState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	path := SleepStatePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// storeFiles are the session stores under root, sorted: <root>/sessions/<harness>/<session>.jsonl.
func storeFiles(root string) []string {
	paths, _ := filepath.Glob(filepath.Join(root, "sessions", "*", "*.jsonl"))
	sort.Strings(paths)
	return paths
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return st.Size()
}

// sleepLine is one session's report line.
func sleepLine(name string, data map[string]any) string {
	count := func(key string) int {
		list, _ := data[key].([]any)
		return len(list)
	}
	records, _ := data["records"].(float64)
	return fmt.Sprintf("%s: %d records read; %d duplicates, %d links, %d decayed, %d verified, %d retired, %d checks",
		name, int(records), count("duplicates"), count("links"), count("decayed"), count("verified"), count("retired"), count("checks"))
}

// sleepOne runs the pass over h's branch; the report line, "" when nothing was appended.
func sleepOne(rt *runtime.Runtime, h host.Host, name string) (string, error) {
	rt.EnsureConfig(h)
	entries, err := h.GetBranch()
	if err != nil {
		return "", err
	}
	e, ok := hooks.RunSleep(context.Background(), rt, h, rt.Config(), entries, ledger.SleepManual)
	if !ok {
		return "", nil
	}
	data, _ := e.Data.(map[string]any)
	return sleepLine(name, data), nil
}

// runSleep is om sleep. session, when set, is the one session to sleep (with its host).
func runSleep(root string, session func() (*neutral.Host, string, error), stdout io.Writer) error {
	if err := os.MkdirAll(filepath.Join(root, "sleep"), 0o700); err != nil {
		return err
	}
	var lines []string
	held, err := filelock.TryWithLock(SleepLockPath(root), func() error {
		rt := runtime.New()
		if session != nil {
			h, name, err := session()
			if err != nil {
				return err
			}
			line, err := sleepOne(rt, h, name)
			if line != "" {
				lines = append(lines, line)
			}
			return err
		}
		st := readSleepState(root)
		for _, path := range storeFiles(root) {
			rel, _ := filepath.Rel(filepath.Join(root, "sessions"), path)
			key := filepath.ToSlash(rel)
			if size, ok := st.Stores[key]; ok && size == fileSize(path) {
				continue
			}
			harness := filepath.Base(filepath.Dir(path))
			h := &neutral.Host{Session: key, Root: root, Store: neutral.Store{Path: path}, HarnessName: harness}
			line, err := sleepOne(rt, h, key)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if line != "" {
				lines = append(lines, line)
			}
			// The checkpoint: a session start that stops the pass loses at most this store.
			st.Stores[key] = fileSize(path)
			if err := writeSleepState(root, st); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !held {
		_, err := fmt.Fprintf(stdout, "om sleep: another sleep pass holds %s; nothing done\n", SleepLockPath(root))
		return err
	}
	if len(lines) == 0 {
		lines = []string{"om sleep: nothing new since the last pass"}
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}
