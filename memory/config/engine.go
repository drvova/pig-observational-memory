package config

import (
	"os"
	"path/filepath"
)

// Engine placements the engine setting takes (docs/engine-rpc.md, docs/lisp-core.md).
const (
	// EngineInProcess links the Go engine into the adapter's process: the setting "go" (also
	// "in-process"), the kill switch for the Lisp image, and the default where the image has no
	// transport (LispSupported false: Windows).
	EngineInProcess = "in-process"
	// EngineGo is the setting value (and OM_ENGINE value) that selects EngineInProcess.
	EngineGo = "go"
	// EngineProcess runs the engine as a long-lived `om serve` process the adapter talks to
	// over stdio.
	EngineProcess = "process"
	// EngineLisp runs the user's SBCL engine image, shared by every client, over its unix
	// socket, with the in-process Go engine answering what the image could not before it acted
	// (docs/lisp-port-peer.md): the default where LispSupported.
	EngineLisp = "lisp"
)

// EngineEnv overrides the engine setting for om and the PiG adapter: "go" (or "in-process") is
// the Go engine, "lisp" the image, "process" (the adapter only) an `om serve` process.
const EngineEnv = "OM_ENGINE"

// EngineSetting is where the engine runs: OM_ENGINE, else the observational-memory settings
// "engine", "enginePath" and "lispImage" of the user's global settings file only. A project's
// settings never choose it: enginePath and lispImage name a program the adapter runs, so a
// repository must not set them. It is read once, when the adapter loads; a change applies after
// the harness restarts.
type EngineSetting struct {
	// Mode is EngineInProcess, EngineProcess or EngineLisp.
	Mode string
	// Default is true when neither OM_ENGINE nor the setting chose Mode.
	Default bool
	// Path is the om binary to run, "" for `om` on PATH.
	Path string
	// Image is the Lisp engine image to start when none listens, "" for the om root's
	// lispImage setting or OM_LISP_IMAGE, else the image built from the embedded sources.
	Image string
}

// Process reports whether the engine runs as its own process.
func (s EngineSetting) Process() bool { return s.Mode == EngineProcess }

// Lisp reports whether the engine is the Lisp image.
func (s EngineSetting) Lisp() bool { return s.Mode == EngineLisp }

// DefaultEngine is the mode with no OM_ENGINE and no setting: the image where it has a
// transport, else the Go engine.
func DefaultEngine() string {
	if LispSupported {
		return EngineLisp
	}
	return EngineInProcess
}

// EngineMode is the mode an OM_ENGINE or "engine" value names, "" for an empty one: "lisp" and
// "process" name themselves; "go", "in-process" and any other value the user wrote are the Go
// engine (a typo never turns the image on).
func EngineMode(value string) string {
	switch value {
	case "":
		return ""
	case EngineLisp, EngineProcess:
		return value
	}
	return EngineInProcess
}

// ReadEngine reads the engine setting: OM_ENGINE, else the "engine" value in paths' global
// settings.json, else DefaultEngine. enginePath and lispImage are kept only when absolute.
func ReadEngine(paths Paths) EngineSetting {
	setting := EngineSetting{Mode: EngineMode(os.Getenv(EngineEnv))}
	var nested map[string]any
	if raw, ok := readJSONObject(filepath.Join(paths.AgentDir, "settings.json")); ok {
		nested, _ = raw[SettingsKey].(map[string]any)
	}
	if setting.Mode == "" {
		mode, _ := nested["engine"].(string)
		setting.Mode = EngineMode(mode)
	}
	if setting.Mode == "" {
		setting.Mode, setting.Default = DefaultEngine(), true
	}
	if path, _ := nested["enginePath"].(string); filepath.IsAbs(path) {
		setting.Path = path
	}
	if image, _ := nested["lispImage"].(string); filepath.IsAbs(image) {
		setting.Image = image
	}
	return setting
}
