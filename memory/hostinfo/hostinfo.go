// Package hostinfo is what the memory engine asks the harness it runs in about its own
// directories. Each adapter sets these at start (the PiG extension from its SDK); with none set
// they fail, and the engine falls back to Pi's conventions.
package hostinfo

import "errors"

// ErrUnavailable is what the defaults return: no adapter told the engine.
var ErrUnavailable = errors.New("hostinfo: not provided by this harness")

// AgentDir is the harness's agent directory (where its global settings.json lives).
var AgentDir = func() (string, error) { return "", ErrUnavailable }

// ConfigDirName is the harness's per-project configuration directory name (".pig", ".pi").
var ConfigDirName = func() (string, error) { return "", ErrUnavailable }
