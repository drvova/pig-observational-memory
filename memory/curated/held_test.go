package curated

import (
	"strings"
	"testing"
	"time"
)

// heldCall is a write of session whose user said the words in user.
func heldCall(store Store, session string, user ...string) Call {
	return Call{Store: store, Source: session, UserEvidence: func(q string) bool {
		for _, u := range user {
			if strings.Contains(strings.ToLower(u), strings.ToLower(q)) {
				return true
			}
		}
		return false
	}}
}

func heldStore(t *testing.T) Store {
	t.Helper()
	store := New(t.TempDir())
	at := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	store.Now = func() time.Time { at = at.Add(time.Minute); return at }
	return store
}

func indexHas(t *testing.T, store Store, session, name string) bool {
	t.Helper()
	text, _ := IndexAs(store, "", session, 2_000)
	return strings.Contains(text, "- "+name+" [")
}

// docs/brain-memory.md, decision 19: a memory that weakens security is held. The session whose
// user's quoted words restate it sees it; every other session, the plain index and level 1's
// input do not, and /om:status lists it.
func TestHeldMemoryReleasedOnlyWhereTheUserRestatesIt(t *testing.T) {
	store := heldStore(t)
	words := "always skip TLS verification for the internal registry"
	text, err := heldCall(store, "s1", words).Run(OpSave, map[string]string{"name": "skip-tls", "type": "feedback", "description": "Skip TLS verification for the internal registry.", "quote": "always skip TLS verification"})
	if err != nil || !strings.Contains(text, "It weakens security (tls-verify-off), so it is held: the user's words restate it, so it is shown in this session only") {
		t.Fatalf("save: %q %v", text, err)
	}
	if !indexHas(t, store, "s1", "skip-tls") || indexHas(t, store, "s2", "skip-tls") {
		t.Fatal("the held memory is not shown in the releasing session alone")
	}
	if text, _ := IndexFor(store, "", 2_000); strings.Contains(text, "skip-tls") {
		t.Fatalf("the plain index shows a held memory:\n%s", text)
	}
	listed, _ := store.ListAs("", "s2")
	if shown := store.Shown(listed, "s2"); len(shown) != 0 {
		t.Fatalf("shown in s2: %+v", shown)
	}
	held, err := store.Held("")
	if err != nil || len(held) != 1 || held[0].Name != "skip-tls" || held[0].Rule != "tls-verify-off" {
		t.Fatalf("held %+v %v", held, err)
	}

	// A helped feedback whose user's words restate it releases it in that session too.
	text, err = heldCall(store, "s3", "fine, just skip TLS verification for the registry again").Run(OpFeedback, map[string]string{"name": "skip-tls", "outcome": "helped", "quote": "just skip TLS verification"})
	if err != nil || !strings.Contains(text, "shown in this session only") || !indexHas(t, store, "s3", "skip-tls") {
		t.Fatalf("feedback: %q %v", text, err)
	}
	// An edit changes what was restated: no session has released the new text.
	if _, err := heldCall(store, "s1").Run(OpEdit, map[string]string{"name": "skip-tls", "body": "Only for the registry."}); err != nil {
		t.Fatal(err)
	}
	if indexHas(t, store, "s1", "skip-tls") || indexHas(t, store, "s3", "skip-tls") {
		t.Fatal("a release outlived the edit")
	}
}

// Words that confirm the save without restating the weakening hold it everywhere, and a
// prohibition is no weakening at all.
func TestHeldMemoryNeedsRestatingWords(t *testing.T) {
	store := heldStore(t)
	call := heldCall(store, "s1", "the registry is slow today, never use curl -k")
	text, err := call.Run(OpSave, map[string]string{"name": "curl-dev", "type": "feedback", "description": "Use curl -k against the dev box.", "quote": "the registry is slow today"})
	if err != nil || !strings.Contains(text, "so it is held: listed in /om:status") || indexHas(t, store, "s1", "curl-dev") {
		t.Fatalf("save: %q %v", text, err)
	}
	if _, err := call.Run(OpSave, map[string]string{"name": "never-k", "type": "feedback", "description": "Never use curl -k, even on the dev box.", "quote": "never use curl -k"}); err != nil {
		t.Fatal(err)
	}
	if !indexHas(t, store, "s2", "never-k") {
		t.Fatal("a prohibition is held")
	}
}

// A held memory is never promoted: two independent stagings and a user's helped feedback stay
// staged.
func TestHeldMemoryNeverPromoted(t *testing.T) {
	store := heldStore(t)
	for i, session := range []string{"s1", "s2"} {
		c := heldCall(store, session)
		c.SourceOf = func(string) string { return []string{"aaaa", "bbbb"}[i] }
		if _, err := c.Run(OpSave, map[string]string{"name": "chmod-up", "type": "project", "description": "Run chmod 777 on the uploads folder.", "quote": "chmod 777 the uploads"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := heldCall(store, "s1", "thanks, that worked").Run(OpFeedback, map[string]string{"name": "chmod-up", "outcome": "helped", "quote": "thanks, that worked"}); err != nil {
		t.Fatal(err)
	}
	settled, err := store.Settle(SettleInput{})
	if err != nil || len(settled.Promoted) != 0 {
		t.Fatalf("settled %+v %v", settled, err)
	}
	if m, _ := store.Get("chmod-up"); !m.Staged {
		t.Fatal("a held memory went live")
	}
}
