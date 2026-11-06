package observationalmemory

import (
	"os"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/config"
)

// TestMain runs the extension on the in-process Go engine unless a test chooses another: the
// Lisp image is the default only where sbcl can build it, which no test may depend on, and a
// default build would write under the real ~/.om. OM_ENGINE in the environment overrides it.
func TestMain(m *testing.M) {
	if os.Getenv(config.EngineEnv) == "" {
		os.Setenv(config.EngineEnv, config.EngineGo)
	}
	os.Exit(m.Run())
}
