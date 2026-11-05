package observationalmemory

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/tokens"
)

// The memory engine is harness-neutral; under PiG it asks PiG for its directories and uses
// the SDK's token estimate, the one PiG's own compaction counts with.
func init() {
	hostinfo.AgentDir = sdk.AgentDir
	hostinfo.ConfigDirName = sdk.ConfigDirName
	tokens.EstimateMessageTokens = sdk.EstimateTokens
}
