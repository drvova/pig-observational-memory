package agents

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// Table is one table of records with named fields (docs/memory-interface.md,
// section 1), so the observer works on tables, never on text.
type Table = tables.Table

// Codec turns tables into text and back, so the text format the model reads
// and writes can be switched per model without touching the workers
// (docs/memory-interface.md, sections 1 and 6).
type Codec = tables.Codec

// RowsCodec is the default codec: `name[N]{f1,f2,...}:` followed by N rows.
var RowsCodec = tables.Rows
