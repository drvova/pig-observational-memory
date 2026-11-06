package lispimage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
)

// Selection is which engine om runs (docs/lisp-core.md, "The switch").
type Selection struct {
	// Lisp is true for the image, false for the Go engine.
	Lisp bool
	// Explicit is true when OM_ENGINE or the engine setting chose it, false for the default.
	Explicit bool
}

// Select is om's engine for root: OM_ENGINE ("go" is the kill switch, "lisp" the image), else
// the engine setting in root's settings.json, else the image where it has a transport
// (config.LispSupported; Windows stays on Go). Any other value, "process" included (an adapter
// placement), is the Go engine.
func Select(root string) Selection {
	value := os.Getenv(config.EngineEnv)
	if value == "" {
		if raw, err := os.ReadFile(filepath.Join(root, "settings.json")); err == nil {
			var s map[string]map[string]any
			if json.Unmarshal(raw, &s) == nil {
				value, _ = s[config.SettingsKey]["engine"].(string)
			}
		}
	}
	if mode := config.EngineMode(value); mode != "" {
		return Selection{Lisp: mode == config.EngineLisp, Explicit: true}
	}
	return Selection{Lisp: config.DefaultEngine() == config.EngineLisp}
}

// logLimit is the size past which engine.log is moved to engine.log.1 before a line is added.
const logLimit = 1 << 20

// LogPath is root's engine log: the image's own output and every fallback to the Go engine.
func LogPath(root string) string { return filepath.Join(root, "engine.log") }

// LogFailover appends one line to root's engine log saying that an event ran on the Go engine
// and why (every fallback is logged, docs/lisp-core.md). The log is rotated past 1 MB.
func LogFailover(root, event, reason string) {
	path := LogPath(root)
	if info, err := os.Stat(path); err == nil && info.Size() > logLimit {
		_ = os.Rename(path, path+".1")
	}
	if os.MkdirAll(root, 0o700) != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s"+fallbackMark+"%s ran on the Go engine: %s\n", time.Now().UTC().Format(time.RFC3339), event, reason)
}

// fallbackMark is what every LogFailover line holds.
const fallbackMark = " om: fallback: "

// FallbackCount is how many fallbacks root's engine log holds (engine.log, without the rotated
// engine.log.1): om status shows it, since the image's own refusal counter misses the events it
// never saw.
func FallbackCount(root string) int {
	raw, err := os.ReadFile(LogPath(root))
	if err != nil {
		return 0
	}
	return bytes.Count(raw, []byte(fallbackMark))
}
