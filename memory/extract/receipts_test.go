package extract

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The working-memory cases of contracts/encoding.jsonl that are level 0's grammar (memory/ledger's
// TestEncodingContractCases reads the others; lisp/tests/encoding.lisp reads them all).
func TestWorkingContractCases(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "contracts", "encoding.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var c struct{ Name, Kind, Input, Want string }
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			continue
		}
		got := ""
		switch c.Kind {
		case "scopeExclusion":
			got = fmt.Sprint(IsScopeExclusion(c.Input))
		case "replacedRule":
			got = replacedRulePhrase(c.Input)
		default:
			continue
		}
		n++
		if got != c.Want {
			t.Errorf("%s (%s): got %q, want %q", c.Name, c.Kind, got, c.Want)
		}
	}
	if n < 8 {
		t.Fatalf("read %d cases", n)
	}
}
