package curated

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

func v2Store(t *testing.T) Store {
	t.Helper()
	s := testStore(t)
	s.V2 = true
	return s
}

// One validator on every write and load under memory v2: the fields a memory takes, a body that
// fits one recall, and pinned only from the user's own edit of the file. Under v1 the same files
// load as before.
func TestCheckOnWriteAndLoad(t *testing.T) {
	s := v2Store(t)
	long := strings.Repeat("x", serialize.RecallPageChars)
	if _, err := s.Save(Memory{Name: "too-long", Type: TypeLesson, Description: "Long.", Body: long}, "s1"); err == nil || !strings.Contains(err.Error(), "one recall shows at most") {
		t.Fatalf("a body past one recall must be refused, got %v", err)
	}
	fits := strings.Repeat("y", serialize.RecallPageChars-200)
	if _, err := s.Save(Memory{Name: "fits", Type: TypeLesson, Description: "Fits.", Body: fits}, "s1"); err != nil {
		t.Fatalf("a body within one recall: %v", err)
	}
	if _, err := s.Update("fits", "", Edit{Body: &long}, "s1"); err == nil {
		t.Fatal("an edit past one recall must be refused")
	}
	saved, err := s.Save(Memory{Name: "pinned-try", Type: TypeUser, Description: "Tries to pin.", Pinned: true}, "s1")
	if err != nil || saved.Pinned {
		t.Fatalf("a write never sets pinned: %+v, %v", saved, err)
	}
	// The user pins a memory by editing its file; an edit keeps it.
	path := filepath.Join(s.Dir, "pinned-try.md")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "---\n", "---\npinned: true\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	body := "Kept."
	if m, err := s.Update("pinned-try", "", Edit{Body: &body}, "s1"); err != nil || !m.Pinned {
		t.Fatalf("an edit must keep the user's pin: %+v, %v", m, err)
	}
	if m, err := s.Get("pinned-try"); err != nil || !m.Pinned {
		t.Fatalf("pin lost: %+v, %v", m, err)
	}
	// A field no memory takes is refused on load under v2, read as before under v1.
	if err := os.WriteFile(filepath.Join(s.Dir, "odd.md"), []byte("---\ntype: user\ndescription: Odd.\ncolour: blue\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("odd"); err == nil || !strings.Contains(err.Error(), "colour") {
		t.Fatalf("an unknown field must be refused under v2, got %v", err)
	}
	v1 := s
	v1.V2 = false
	if m, err := v1.Get("odd"); err != nil || m.Description != "Odd." {
		t.Fatalf("v1 reads the file as before: %+v, %v", m, err)
	}
}

// Under v2 forget retires: the file stays, the memory is found nowhere, and its name may be saved
// again. Under v1 forget removes the file, as before.
func TestForgetRetiresUnderV2(t *testing.T) {
	s := v2Store(t)
	mustSave(t, s, Memory{Name: "old-fact", Type: TypeProject, Description: "The api runs on port 8080."})
	if err := s.Forget("old-fact", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "old-fact.md")); err != nil {
		t.Fatalf("a retired memory keeps its file: %v", err)
	}
	if m, err := s.Get("old-fact"); err != nil || m.Retired.IsZero() {
		t.Fatalf("retired %+v, %v", m, err)
	}
	if _, err := s.GetIn("old-fact", ""); err != ErrNotFound {
		t.Fatalf("a retired memory must not be found, got %v", err)
	}
	if list, _ := s.List(""); len(list) != 0 {
		t.Fatalf("a retired memory must not be listed: %s", names(list))
	}
	if err := s.Forget("old-fact", ""); err != ErrNotFound {
		t.Fatalf("forgetting twice: %v", err)
	}
	if m, err := s.Save(Memory{Name: "old-fact", Type: TypeProject, Description: "The api runs on port 9090."}, "s2"); err != nil || !m.Retired.IsZero() {
		t.Fatalf("a retired name may be saved again: %+v, %v", m, err)
	}
}

// A staged save stays out of the index and out of other sessions' view until evidence promotes
// it: a user's words quoted in feedback, a level-1 citation, or the same save staged in another
// session. Nothing deletes a staged memory.
func TestSettlePromotesStagedOnEvidence(t *testing.T) {
	s := v2Store(t)
	for _, name := range []string{"by-quote", "by-citation", "by-sessions", "no-evidence"} {
		if _, err := s.Save(Memory{Name: name, Type: TypeUser, Description: "Memory " + name + ".", Staged: true}, "s1"); err != nil {
			t.Fatal(err)
		}
	}
	if text, shown := IndexFor(s, "", 0); text != EmptyIndex || len(shown) != 0 {
		t.Fatalf("staged memories are not in the index:\n%s", text)
	}
	if _, err := s.GetAs("by-quote", "", "s2"); err != ErrNotFound {
		t.Fatalf("another session must not see a staged memory, got %v", err)
	}
	if _, err := s.GetAs("by-quote", "", "s1"); err != nil {
		t.Fatalf("the staging session sees it: %v", err)
	}
	// A helped feedback whose quote is not a user's words is no evidence.
	if err := s.appendUsage(usageLine{Name: "by-quote", Event: EventHelped, Quote: "assistant words here"}); err != nil {
		t.Fatal(err)
	}
	if settled, err := s.Settle(SettleInput{}); err != nil || len(settled.Promoted) != 0 {
		t.Fatalf("no evidence yet: %+v, %v", settled, err)
	}
	if err := s.appendUsage(usageLine{Name: "by-quote", Event: EventHelped, Quote: "yes, always tabs", User: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCited("by-citation", "aaaaaaaaaaa1"); err != nil {
		t.Fatal(err)
	}
	joined, err := s.Save(Memory{Name: "by-sessions", Type: TypeUser, Description: "memory   BY-SESSIONS.", Staged: true}, "s2")
	if err != nil || len(joined.StagedBy) != 2 {
		t.Fatalf("the same save in another session joins it: %+v, %v", joined, err)
	}
	if _, err := s.Save(Memory{Name: "no-evidence", Type: TypeUser, Description: "Something else.", Staged: true}, "s2"); err == nil || !strings.Contains(err.Error(), "staged by another session") {
		t.Fatalf("another description under a staged name: %v", err)
	}
	settled, err := s.Settle(SettleInput{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range settled.Promoted {
		got[p.Name] = p.Evidence
	}
	if got["by-quote"] != EvidenceUserQuote || got["by-citation"] != EvidenceCitation || got["by-sessions"] != EvidenceSessions || len(got) != 3 {
		t.Fatalf("promoted %+v", settled.Promoted)
	}
	if m, err := s.Get("no-evidence"); err != nil || !m.Staged {
		t.Fatalf("a staged memory without evidence stays staged, never deleted: %+v, %v", m, err)
	}
	text, shown := IndexFor(s, "", 0)
	if len(shown) != 3 || strings.Contains(text, "no-evidence") {
		t.Fatalf("index after promotion %v:\n%s", shown, text)
	}
	if again, _ := s.Settle(SettleInput{}); len(again.Promoted) != 0 {
		t.Fatalf("promotion happens once: %+v", again)
	}
}

// A user correction whose subject matches a memory flags it once per correction, only when it
// came after the memory's last save or edit, and never touches a pinned memory; a flagged memory
// ranks last until it is edited.
func TestSettleFlagsMatchingCorrections(t *testing.T) {
	s := v2Store(t)
	mustSave(t, s, Memory{Name: "deploy-port", Type: TypeUser, Description: "Deploys use port 8080."})
	mustSave(t, s, Memory{Name: "lesson", Type: TypeLesson, Description: "Run the tests."})
	mustSave(t, s, Memory{Name: "pinned-port", Type: TypeUser, Description: "Pinned port."})
	path := filepath.Join(s.Dir, "pinned-port.md")
	data, _ := os.ReadFile(path)
	_ = os.WriteFile(path, []byte(strings.Replace(string(data), "---\n", "---\npinned: true\n", 1)), 0o600)
	later := s.Now().Add(time.Hour)
	before := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	in := SettleInput{Corrections: []Correction{
		{ID: "c1", Subject: "deploy port", Time: later},
		{ID: "c2", Subject: "port", Time: later},
		{ID: "c0", Subject: "deploy port", Time: before},
		{ID: "c3", Subject: "", Time: later},
	}}
	settled, err := s.Settle(in)
	if err != nil || strings.Join(settled.Flagged, ",") != "deploy-port" {
		t.Fatalf("flagged %+v, %v", settled, err)
	}
	usage, _ := s.Usage()
	if usage["deploy-port"].Corrected != 2 || usage["pinned-port"].Corrected != 0 {
		t.Fatalf("corrected counts %+v", usage)
	}
	if again, _ := s.Settle(in); len(again.Flagged) != 0 {
		t.Fatalf("a correction flags once: %+v", again)
	}
	all, _ := s.List("")
	if ranked := names(Rank(all, usage)); !strings.HasSuffix(ranked, "deploy-port") {
		t.Fatalf("a flagged memory ranks last: %s", ranked)
	}
	desc := "Deploys use port 9090."
	if _, err := s.Update("deploy-port", "", Edit{Description: &desc}, ""); err != nil {
		t.Fatal(err)
	}
	if usage, _ := s.Usage(); usage["deploy-port"].Corrected != 0 {
		t.Fatalf("an edit clears the flag: %+v", usage["deploy-port"])
	}
	if flags, _ := s.Flags(); len(flags) != 0 {
		t.Fatalf("flags after the edit %v", flags)
	}
}

func TestSubjectMatches(t *testing.T) {
	for _, c := range []struct {
		subject, name string
		want          bool
	}{
		{"tabs", "prefers-tabs", true},
		{"deploy port", "deploy-port", true},
		{"deploy/port", "staging-deploy-port-rule", true},
		{"indentation", "prefers-tabs", false},
		{"port", "export-rules", false},
		{"", "anything", false},
		{"the staging deploy port", "deploy-port", true},
	} {
		if got := SubjectMatches(c.subject, c.name); got != c.want {
			t.Errorf("SubjectMatches(%q, %q) = %v", c.subject, c.name, got)
		}
	}
}

// A v1 store loaded under memory v2: a memory past the bound, or with a field no memory takes,
// is refused on load but never silently (Refused names it and why), and it can still be edited
// within the validator or retired with forget; a save of its name says how.
func TestV1StoreUnderV2(t *testing.T) {
	v1 := testStore(t)
	big := strings.Repeat("x", serialize.RecallPageChars+2000)
	mustSave(t, v1, Memory{Name: "big", Type: TypeLesson, Description: "A long lesson.", Body: big})
	mustSave(t, v1, Memory{Name: "tagged", Type: TypeUser, Description: "Tabs."})
	mustSave(t, v1, Memory{Name: "old-big", Type: TypeLesson, Description: "Another long one.", Body: big})
	path := filepath.Join(v1.Dir, "tagged.md")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "---\n", "---\ntags: style\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	s := v1
	s.V2 = true
	if listed, _ := s.List(""); len(listed) != 0 {
		t.Fatalf("refused memories are not loaded: %s", names(listed))
	}
	refused, err := s.Refused("")
	if err != nil || len(refused) != 3 || refused[0].Name != "big" || !strings.Contains(refused[0].Reason, "shorten the body") || refused[2].Name != "tagged" || !strings.Contains(refused[2].Reason, "tags") {
		t.Fatalf("refused %+v, %v", refused, err)
	}
	if v1Refused, _ := v1.Refused(""); len(v1Refused) != 0 {
		t.Fatalf("v1 refuses nothing it reads: %+v", v1Refused)
	}
	if _, err := s.Save(Memory{Name: "big", Type: TypeLesson, Description: "New."}, "s1"); err == nil || !strings.Contains(err.Error(), "edit name=big") {
		t.Fatalf("a save of a refused memory's name says how to fix it, got %v", err)
	}
	short := "Short now."
	if m, err := s.Update("big", "", Edit{Body: &short}, "s1"); err != nil || m.Body != short {
		t.Fatalf("an edit within the bound brings it back: %+v, %v", m, err)
	}
	desc := "Tabs, always."
	if _, err := s.Update("tagged", "", Edit{Description: &desc}, "s1"); err != nil {
		t.Fatalf("an edit rewrites the file with the fields a memory takes: %v", err)
	}
	if _, err := s.Update("old-big", "", Edit{Description: &desc}, "s1"); err == nil {
		t.Fatal("an edit that leaves the memory past the bound is refused")
	}
	if err := s.Forget("old-big", ""); err != nil {
		t.Fatalf("forget retires a refused memory: %v", err)
	}
	if listed, _ := s.List(""); names(listed) != "big,tagged" {
		t.Fatalf("after the fixes: %s", names(listed))
	}
	if refused, _ := s.Refused(""); len(refused) != 0 {
		t.Fatalf("nothing left refused (a retired one is not reported): %+v", refused)
	}
}

// Evidence counts only from a staged memory's last save or edit: a user's quote for one text
// never promotes the text an edit wrote after it.
func TestSettleEvidenceSinceLastEdit(t *testing.T) {
	s := v2Store(t)
	if _, err := s.Save(Memory{Name: "deploy-target", Type: TypeProject, Description: "Where prod runs.", Body: "prod is us-east-1", Staged: true}, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := s.appendUsage(usageLine{Name: "deploy-target", Event: EventHelped, Quote: "yes that is right", User: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCited("deploy-target", "aaaaaaaaaaa1"); err != nil {
		t.Fatal(err)
	}
	body := "prod is eu-west-1"
	if _, err := s.Update("deploy-target", "", Edit{Body: &body}, "s1"); err != nil {
		t.Fatal(err)
	}
	if settled, err := s.Settle(SettleInput{}); err != nil || len(settled.Promoted) != 0 {
		t.Fatalf("evidence before the edit must not promote the edited text: %+v, %v", settled, err)
	}
	if err := s.appendUsage(usageLine{Name: "deploy-target", Event: EventHelped, Quote: "yes, eu-west-1", User: true}); err != nil {
		t.Fatal(err)
	}
	if settled, err := s.Settle(SettleInput{}); err != nil || len(settled.Promoted) != 1 || settled.Promoted[0].Evidence != EvidenceUserQuote {
		t.Fatalf("evidence after the edit promotes it: %+v, %v", settled, err)
	}
}

// A pinned memory ranks as unflagged even when a correction flagged it before the pin, and a v1
// store's ranking leaves out the corrected events a memory v2 session recorded in the shared
// store.
func TestRankIgnoresFlagsOnPinnedAndUnderV1(t *testing.T) {
	s := v2Store(t)
	mustSave(t, s, Memory{Name: "other", Type: TypeUser, Description: "Other."})
	// Saved last, so it ranks first among equals.
	mustSave(t, s, Memory{Name: "deploy-target", Type: TypeUser, Description: "Prod runs in us-east-1."})
	in := SettleInput{Corrections: []Correction{{ID: "c1", Subject: "deploy target", Time: s.Now().Add(time.Hour)}}}
	if settled, err := s.Settle(in); err != nil || len(settled.Flagged) != 1 {
		t.Fatalf("flagged %+v, %v", settled, err)
	}
	all, _ := s.List("")
	if ranked := names(Rank(all, s.RankUsage())); ranked != "other,deploy-target" {
		t.Fatalf("a flagged memory ranks last under v2: %s", ranked)
	}
	v1 := s
	v1.V2 = false
	if ranked := names(Rank(all, v1.RankUsage())); ranked != "deploy-target,other" {
		t.Fatalf("v1 ranks without v2's corrections: %s", ranked)
	}
	path := filepath.Join(s.Dir, "deploy-target.md")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "---\n", "---\npinned: true\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	all, _ = s.List("")
	if ranked := names(Rank(all, s.RankUsage())); ranked != "deploy-target,other" {
		t.Fatalf("a pinned memory is never ranked as flagged: %s", ranked)
	}
}
