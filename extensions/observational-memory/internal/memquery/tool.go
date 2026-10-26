package memquery

import "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"

// ToolName is the memory tool's name. The tool's surface (description, schema, prompt lines,
// details and renderers) moved to internal/omtool with the code tool (docs/code-tool.md, "Order
// of work", step 7); the query language keeps the name only to skip the memory tool's calls
// until it is deleted.
const ToolName = omtool.ToolName
