// Package transcript reads a harness's own conversation file as the Pi-shaped entries the
// memory engine reads (docs/universal.md, "Transcripts"): user and assistant messages and
// tool results, in branch order, with the harness's ids. It reads only; the engine never
// writes a harness's files.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// File is a transcript read from one file in one harness's format.
type File struct {
	Path   string
	Format Format
}

// Format turns a transcript file's decoded JSON lines (in file order) into entries.
type Format func(lines []map[string]any) []session.Entry

// Entries reads the file; a missing file is an empty conversation.
func (f File) Entries() ([]session.Entry, error) {
	lines, err := readLines(f.Path)
	if err != nil {
		return nil, err
	}
	return f.Format(lines), nil
}

var _ neutral.Transcript = File{}
var _ neutral.Versioned = File{}

// Version is the file's size and modification time ("" when it is missing).
func (f File) Version() string { return statVersion(f.Path) }

func statVersion(paths ...string) string {
	out := ""
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			out += "-;"
			continue
		}
		out += fmt.Sprintf("%d:%d;", st.Size(), st.ModTime().UnixNano())
	}
	return out
}

func readLines(path string) ([]map[string]any, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024*1024)
	firstFailed, seen := false, false
	for scanner.Scan() {
		if !seen && len(bytes.TrimSpace(scanner.Bytes())) > 0 {
			seen = true
			var probe map[string]any
			firstFailed = json.Unmarshal(scanner.Bytes(), &probe) != nil
		}
		var m map[string]any
		if json.Unmarshal(scanner.Bytes(), &m) == nil && m != nil {
			out = append(out, m)
		}
	}
	if err := scanner.Err(); err != nil || !firstFailed {
		return out, err
	}
	// A file that is one JSON document over several lines (Cline's messages.json) is one line.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if json.Unmarshal(data, &m) == nil && m != nil {
		return []map[string]any{m}, nil
	}
	return out, nil
}

// message builds a Pi message entry.
func message(id, timestamp string, msg map[string]any, aliases []string) session.Entry {
	if ms, ok := millis(timestamp); ok {
		msg["timestamp"] = float64(ms)
	}
	raw := map[string]any{"type": "message", "id": id, "timestamp": timestamp, "message": msg}
	if len(aliases) > 0 {
		raw[neutral.AliasesKey] = aliases
	}
	return session.FromMap(raw)
}

func millis(timestamp string) (int64, bool) {
	t, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func str(v any) string {
	s, _ := v.(string)
	return s
}

// resultText is a tool result's content as text: a string, or the text of its blocks.
func resultText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		out := ""
		for _, raw := range c {
			if b, ok := raw.(map[string]any); ok && str(b["type"]) == "text" {
				if out != "" {
					out += "\n"
				}
				out += str(b["text"])
			}
		}
		return out
	case map[string]any:
		if s := str(c["output"]); s != "" {
			return s
		}
		return resultText(c["content"])
	}
	return ""
}
