package config

import "path/filepath"

// Engine placements the engine setting takes (docs/engine-rpc.md).
const (
	// EngineInProcess links the engine into the adapter's process: the default.
	EngineInProcess = "in-process"
	// EngineProcess runs the engine as a long-lived `om serve` process the adapter talks to
	// over stdio.
	EngineProcess = "process"
)

// EngineSetting is where the engine runs: the observational-memory settings "engine" and
// "enginePath" of the user's global settings file only. A project's settings never choose it:
// enginePath names a program the adapter runs, so a repository must not set it. It is read
// once, when the adapter loads; a change applies after the harness restarts.
type EngineSetting struct {
	// Mode is EngineInProcess or EngineProcess.
	Mode string
	// Path is the om binary to run, "" for `om` on PATH.
	Path string
}

// Process reports whether the engine runs as its own process.
func (s EngineSetting) Process() bool { return s.Mode == EngineProcess }

// ReadEngine reads the engine setting from paths' global settings.json. A missing, malformed
// or unknown value is EngineInProcess; enginePath is kept only when absolute.
func ReadEngine(paths Paths) EngineSetting {
	setting := EngineSetting{Mode: EngineInProcess}
	raw, ok := readJSONObject(filepath.Join(paths.AgentDir, "settings.json"))
	if !ok {
		return setting
	}
	nested, _ := raw[SettingsKey].(map[string]any)
	if mode, _ := nested["engine"].(string); mode == EngineProcess {
		setting.Mode = EngineProcess
	}
	if path, _ := nested["enginePath"].(string); filepath.IsAbs(path) {
		setting.Path = path
	}
	return setting
}
