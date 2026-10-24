package memquery

import (
	"errors"
	"strings"
	"testing"
)

// fakeLayer records the layer writes a query makes.
type fakeLayer struct {
	calls []string
	rows  []HarnessRow
}

func (f *fakeLayer) Define(kind, name, text string) (string, error) {
	f.calls = append(f.calls, "define "+kind+"/"+name+" "+text)
	return "held", nil
}

func (f *fakeLayer) Forget(kind, name string) (string, error) {
	f.calls = append(f.calls, "forget "+kind+"/"+name)
	return "forgot", nil
}

func (f *fakeLayer) Rollback(n int, model string) (string, error) {
	if n > 5 {
		return "", errors.New("generation does not exist")
	}
	f.calls = append(f.calls, "rollback "+string(rune('0'+n))+" "+model)
	return "rolled back", nil
}

func (f *fakeLayer) Listing() ([]HarnessRow, error) { return f.rows, nil }

// The harness and rollback writes reach the layer with their arguments; harness alone or with
// stages reads the layer's listing; without a layer every one of them is refused.
func TestHarnessQueries(t *testing.T) {
	layer := &fakeLayer{rows: []HarnessRow{
		{Model: "p/m", Kind: "instructions", Name: "observer", State: "held", Origin: "override"},
		{Model: "p/m", Kind: "format", Name: "rows", State: "seed", Origin: "seed"},
	}}
	env := Env{Layer: layer}
	for query, want := range map[string]string{
		`harness kind=instructions name=observer text="Keep figures."`: "define instructions/observer Keep figures.",
		`harness forget kind=instructions name=observer`:               "forget instructions/observer",
		`rollback 3`:             "rollback 3 ",
		`rollback 0 model="p/m"`: "rollback 0 p/m",
	} {
		layer.calls = nil
		if r := Run(env, query); r.IsError || len(layer.calls) != 1 || layer.calls[0] != want || r.Details.Kind == "" {
			t.Errorf("%s: %+v calls %v", query, r, layer.calls)
		}
	}
	if r := Run(env, `harness | where state=held`); r.IsError || !strings.Contains(r.Text, "observer") || strings.Contains(r.Text, ",rows,") || r.Details.Kind != SourceHarness {
		t.Fatalf("listing: %+v", r)
	}
	if r := Run(env, `harness`); r.IsError || !strings.Contains(r.Text, "harness[2]") {
		t.Fatalf("harness alone: %s", r.Text)
	}
	for query, want := range map[string]string{
		`rollback`:                            "generation number",
		`rollback x`:                          "generation number",
		`rollback 2 kind=x`:                   "only model=",
		`harness kind=instructions`:           "needs name=",
		`harness forget kind=format`:          "needs name=",
		`harness forget kind=a name=b text=c`: "unknown key",
		`rollback 9`:                          "does not exist",
	} {
		if r := Run(env, query); !r.IsError || !strings.Contains(r.Text, want) {
			t.Errorf("%s: %s (want %q)", query, r.Text, want)
		}
	}
	for _, query := range []string{`harness`, `harness kind=format name=x text=y`, `rollback 1`} {
		if r := Run(Env{}, query); !r.IsError || !strings.Contains(r.Text, "not available") {
			t.Errorf("without a layer %s: %s", query, r.Text)
		}
	}
	if q, err := Parse(`define layered = harness | limit 3 "my layer"`); err == nil {
		t.Fatalf("a defined query reads the layer: %+v", q)
	}
}
