package config

import "path/filepath"

// Engine placements the engine setting takes (docs/engine-rpc.md).
const (
	// EngineInProcess links the engine into the adapter's process: the default.
	EngineInProcess = "in-process"
	// EngineProcess runs the engine as a long-lived `om serve` process the adapter talks to
	// over stdio.
	EngineProcess = "process"
	// EngineLisp runs the user's SBCL engine image, shared by every client, over its unix
	// socket, with the in-process Go engine answering what the image has not ported
	// (docs/lisp-port-peer.md).
	EngineLisp = "lisp"
)

// EngineSetting is where the engine runs: the observational-memory settings "engine",
// "enginePath" and "lispImage" of the user's global settings file only. A project's settings
// never choose it: enginePath and lispImage name a program the adapter runs, so a repository
// must not set them. It is read once, when the adapter loads; a change applies after the
// harness restarts.
type EngineSetting struct {
	// Mode is EngineInProcess, EngineProcess or EngineLisp.
	Mode string
	// Path is the om binary to run, "" for `om` on PATH.
	Path string
	// Image is the Lisp engine image to start when none listens, "" for the om root's
	// lispImage setting or OM_LISP_IMAGE.
	Image string
}

// Process reports whether the engine runs as its own process.
func (s EngineSetting) Process() bool { return s.Mode == EngineProcess }

// Lisp reports whether the engine is the Lisp image.
func (s EngineSetting) Lisp() bool { return s.Mode == EngineLisp }

// ReadEngine reads the engine setting from paths' global settings.json. A missing, malformed
// or unknown value is EngineInProcess; enginePath and lispImage are kept only when absolute.
func ReadEngine(paths Paths) EngineSetting {
	setting := EngineSetting{Mode: EngineInProcess}
	raw, ok := readJSONObject(filepath.Join(paths.AgentDir, "settings.json"))
	if !ok {
		return setting
	}
	nested, _ := raw[SettingsKey].(map[string]any)
	if mode, _ := nested["engine"].(string); mode == EngineProcess || mode == EngineLisp {
		setting.Mode = mode
	}
	if path, _ := nested["enginePath"].(string); filepath.IsAbs(path) {
		setting.Path = path
	}
	if image, _ := nested["lispImage"].(string); filepath.IsAbs(image) {
		setting.Image = image
	}
	return setting
}
