package ledger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// encodingCase is one line of contracts/encoding.jsonl, which lisp/tests/encoding.lisp reads too.
type encodingCase struct {
	Name  string          `json:"name"`
	Kind  string          `json:"kind"`
	Input string          `json:"input"`
	Want  json.RawMessage `json:"want"`
}

func TestEncodingContractCases(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "contracts", "encoding.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var c encodingCase
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		n++
		var want string
		var wantList []string
		if c.Kind == "versions" {
			if err := json.Unmarshal(c.Want, &wantList); err != nil {
				t.Fatal(err)
			}
		} else if err := json.Unmarshal(c.Want, &want); err != nil {
			t.Fatal(err)
		}
		got := ""
		switch c.Kind {
		case "hedge":
			got = HedgeOf(c.Input)
		case "versions":
			if gotList := VersionsIn(c.Input); !slices.Equal(gotList, wantList) {
				t.Errorf("%s: VersionsIn = %q, want %q", c.Name, gotList, wantList)
			}
			continue
		case "timeHint":
			var m map[string]any
			if err := json.Unmarshal([]byte(c.Input), &m); err != nil {
				t.Fatal(err)
			}
			o := Observation{}
			o.Content, _ = m["content"].(string)
			o.Timestamp, _ = m["timestamp"].(string)
			kind, _ := m["kind"].(string)
			o.Kind = ObservationKind(kind)
			o.Status, _ = m["status"].(string)
			o.EventDate, _ = m["eventDate"].(string)
			got = TimeHintOf(o)
		case "authority":
			got = AuthorityOf(c.Input)
		case "customSource":
			got = SourceOfCustomType(c.Input)
		case "pushback":
			got = fmt.Sprint(IsPushback(c.Input))
		case "concedes":
			got = fmt.Sprint(Concedes(c.Input))
		case "restates":
			var m map[string]string
			if err := json.Unmarshal([]byte(c.Input), &m); err != nil {
				t.Fatal(err)
			}
			got = fmt.Sprint(RestatesClaim(Observation{Content: m["restatement"]}, Observation{Content: m["claim"]}))
		case "episode":
			var m map[string]string
			if err := json.Unmarshal([]byte(c.Input), &m); err != nil {
				t.Fatal(err)
			}
			got = EpisodeID(m["session"], m["entry"])
		case "keyMatches":
			var m map[string]string
			if err := json.Unmarshal([]byte(c.Input), &m); err != nil {
				t.Fatal(err)
			}
			got = fmt.Sprint(KeyMatches(m["key"], m["receipt"]))
		case "staleTerms":
			got = strings.Join(StaleTerms(Observation{Content: c.Input}), " | ")
		case "bearsOn":
			var m struct {
				Text  string   `json:"text"`
				Terms []string `json:"terms"`
			}
			if err := json.Unmarshal([]byte(c.Input), &m); err != nil {
				t.Fatal(err)
			}
			got = fmt.Sprint(BearsOn(m.Text, m.Terms))
		case "readPlan":
			got = fmt.Sprint(IsReadPlan(Observation{Content: c.Input}))
		case "scopeExclusion", "replacedRule":
			// Level 0's grammar: memory/extract's TestWorkingContractCases.
			continue
		default:
			t.Fatalf("%s: unknown kind %q", c.Name, c.Kind)
		}
		if got != want {
			t.Errorf("%s (%s): got %q, want %q", c.Name, c.Kind, got, want)
		}
	}
	if n < 50 {
		t.Fatalf("read %d cases", n)
	}
}

func TestKeysAddCopiesOnGrow(t *testing.T) {
	var a Keys
	a.Add(KeyPaths, " /a ")
	a.Add(KeyPaths, "/a")
	a.Add(KeyPaths, "")
	a.Add("nope", "x")
	b := a
	b.Add(KeyPaths, "/b")
	if !slices.Equal(a.Paths, []string{"/a"}) || !slices.Equal(b.Paths, []string{"/a", "/b"}) {
		t.Fatalf("a %v b %v", a.Paths, b.Paths)
	}
	if a.IsZero() || (Keys{}).IsZero() == false {
		t.Fatal("IsZero")
	}
}

// The encoding fields round-trip, are omitted when empty, and a malformed one is cleared.
func TestEncodingFieldsRoundTrip(t *testing.T) {
	o := Observation{ID: "0123456789ab", Content: "x", Timestamp: "2026-11-02 10:00", Relevance: "medium", SourceEntryIDs: []string{"e1"}, TokenCount: 3,
		Source: SourceUserSaid, Authority: AuthorityUser, Hedge: HedgeHedged, TimeHint: "after:2026-11-02", Episode: "ep1",
		Frame: Frame{RepoID: "r", Head: "h"}, Writer: Writer{Harness: "pig", Engine: EngineGo}}
	o.Keys.Add(KeyCommands, "go test ./...")
	encoded, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"0123456789ab","content":"x","timestamp":"2026-11-02 10:00","relevance":"medium","sourceEntryIds":["e1"],"tokenCount":3,"source":"user-said","authority":"user","hedge":"hedged","timeHint":"after:2026-11-02","episode":"ep1","keys":{"commands":["go test ./..."]},"frame":{"repoId":"r","head":"h"},"writer":{"harness":"pig","engine":"go"}}`
	if string(encoded) != want {
		t.Fatalf("encoded\n%s\nwant\n%s", encoded, want)
	}
	var decoded any
	_ = json.Unmarshal(encoded, &decoded)
	back, ok := ObservationFrom(decoded)
	if !ok {
		t.Fatal("not read back")
	}
	again, _ := json.Marshal(back)
	if string(again) != want {
		t.Fatalf("round trip\n%s", again)
	}
	plain, _ := json.Marshal(Observation{ID: "0123456789ab", Content: "x", Timestamp: "t", Relevance: "medium", SourceEntryIDs: []string{"e1"}})
	if string(plain) != `{"id":"0123456789ab","content":"x","timestamp":"t","relevance":"medium","sourceEntryIds":["e1"],"tokenCount":0}` {
		t.Fatalf("an old record's bytes changed: %s", plain)
	}
	var bad any
	_ = json.Unmarshal([]byte(`{"id":"0123456789ab","content":"x","timestamp":"t","relevance":"medium","sourceEntryIds":["e1"],"tokenCount":0,"source":"nope","authority":"root","hedge":1,"timeHint":"after:soon","keys":{"paths":["", 3, "/a"],"other":["x"]},"frame":"x"}`), &bad)
	read, ok := ObservationFrom(bad)
	if !ok || read.Source != "" || read.Authority != "" || read.Hedge != "" || read.TimeHint != "" || !slices.Equal(read.Keys.Paths, []string{"/a"}) || read.Frame != (Frame{}) {
		t.Fatalf("lenient read: %+v", read)
	}
}

func TestLeastAuthorityAndStrongestHedge(t *testing.T) {
	if LeastAuthority(AuthorityUser, AuthorityTool) != AuthorityTool || LeastAuthority(AuthorityModel, "") != "" || LeastAuthority(AuthorityTool, AuthorityRepoFile) != AuthorityTool {
		t.Fatal("LeastAuthority")
	}
	if StrongestHedge(HedgeStated, HedgeUnverified) != HedgeUnverified || StrongestHedge("", HedgeStated) != HedgeStated || StrongestHedge(HedgeHedged, "") != HedgeHedged {
		t.Fatal("StrongestHedge")
	}
}
