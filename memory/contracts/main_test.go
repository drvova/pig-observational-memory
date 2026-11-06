package contracts

import (
	"os"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/config"
)

// TestMain runs om on the Go engine: the corpus is Go's, and the image is the default only where
// sbcl can build it (docs/lisp-core.md, "The switch"), which no test may depend on.
func TestMain(m *testing.M) {
	if os.Getenv(config.EngineEnv) == "" {
		os.Setenv(config.EngineEnv, config.EngineGo)
	}
	os.Exit(m.Run())
}
