package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// session is the harness's current session in dir: the one the hooks recorded (remember), else
// the newest transcript the harness wrote for dir (discover), so the memory tool works before
// any hook has run.
func session(root, harnessName, dir string) (Session, bool) {
	if s, ok := current(root, harnessName, dir); ok {
		return s, true
	}
	return discover(harnessName, dir)
}

// discover finds the newest transcript of dir: Claude Code's under
// ~/.claude/projects/<dir with every / and . as ->, Codex's the newest rollout whose
// session_meta names dir.
func discover(harnessName, dir string) (Session, bool) {
	dir = filepath.Clean(dir)
	switch harnessName {
	case "claude-code":
		project := strings.NewReplacer("/", "-", ".", "-", "\\", "-", ":", "-").Replace(dir)
		path, ok := newest(filepath.Join(homeDir("CLAUDE_CONFIG_DIR", ".claude"), "projects", project), func(string) bool { return true })
		if !ok {
			return Session{}, false
		}
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		return Session{Harness: harnessName, ID: id, Transcript: path, Format: "claude-code", Cwd: dir}, true
	case "codex":
		var meta struct {
			Payload struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		path, ok := newest(filepath.Join(homeDir("CODEX_HOME", ".codex"), "sessions"), func(p string) bool {
			f, err := os.Open(p)
			if err != nil {
				return false
			}
			defer f.Close()
			line, err := bufio.NewReader(f).ReadBytes('\n')
			return (err == nil || len(line) > 0) && json.Unmarshal(line, &meta) == nil && filepath.Clean(meta.Payload.Cwd) == dir && meta.Payload.ID != ""
		})
		if !ok {
			return Session{}, false
		}
		return Session{Harness: harnessName, ID: meta.Payload.ID, Transcript: path, Format: "codex", Cwd: dir}, true
	}
	return Session{}, false
}

// newest is the most recently modified .jsonl file under root that keep accepts (keep runs
// newest first, and the last call's state is the match's).
func newest(root string, keep func(path string) bool) (string, bool) {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			if info, err := d.Info(); err == nil {
				files = append(files, file{p, info.ModTime()})
			}
		}
		return nil
	})
	for len(files) > 0 {
		best := 0
		for i, f := range files {
			if f.mod.After(files[best].mod) {
				best = i
			}
		}
		if keep(files[best].path) {
			return files[best].path, true
		}
		files = append(files[:best], files[best+1:]...)
	}
	return "", false
}
