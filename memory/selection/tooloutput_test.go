package selection_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tool-output transcript (testdata/lispparity/tooloutput.jsonl) through level 0 and the
// summary: its value and evidence records, printed for review with OM_DUMP_TOOLOUTPUT=1.
func TestToolOutputDump(t *testing.T) {
	if os.Getenv("OM_DUMP_TOOLOUTPUT") != "1" {
		t.Skip("set OM_DUMP_TOOLOUTPUT=1 to print the records")
	}
	pinClock(t)
	path := filepath.Join("testdata", "lispparity", "tooloutput.jsonl")
	for _, line := range goPipeline(t, parityCase{"tooloutput", "generic", path, "/srv/app", "whole", []int{4000}}) {
		if strings.HasPrefix(line, "record ") || strings.HasPrefix(line, "summary ") {
			t.Log(line)
		}
	}
}
