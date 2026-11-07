package erase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSpans(t *testing.T) {
	got := Spans("My home address is 14 Larkspur Close, Hove BN3 7AQ.")
	if want := []string{"14 Larkspur Close", "Hove BN3 7AQ"}; !slices.Equal(got, want) {
		t.Fatalf("spans %q, want %q", got, want)
	}
	if got := Spans("Run the tests before you push."); len(got) != 0 {
		t.Fatalf("no specifics, spans %q", got)
	}
}

// Every byte form a payload takes in a file is purged: as written, and inside a JSON string with
// and without HTML escaping; a payload shorter than MinPayload never is.
func TestPurgeTextForms(t *testing.T) {
	payload := `Gate <A&B> "14 Larkspur Close"`
	plain, _ := json.Marshal(payload)
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
	text := payload + "\n" + string(plain) + "\n" + b.String() + "short word"
	out, n := PurgeText(text, []string{payload, "short word"})
	if n != 3 || strings.Contains(out, "Larkspur") || !strings.Contains(out, "short word") {
		t.Fatalf("%d replaced: %q", n, out)
	}
	if again, m := PurgeText(out, []string{payload}); m != 0 || again != out {
		t.Fatal("the purge is not idempotent")
	}
}

// A purge counts and replaces per surface, and a file claimed by an earlier surface is not
// counted twice; the scan after it finds nothing.
func TestPurgeSurfaces(t *testing.T) {
	home := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(home, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("observational-memory/memories/core/log.jsonl", `{"text":"14 Larkspur Close"}`+"\n")
	write("observational-memory/memories/home.md", "Home: 14 Larkspur Close\n")
	write("sessions/mine/s.jsonl", `{"data":{"content":"14 Larkspur Close"}}`+"\n")
	write("transcripts/mine/s.jsonl", `{"text":"14 Larkspur Close"}`+"\n")
	ss := Surfaces(home, true, "")
	before := Scan(ss, []string{"14 Larkspur Close"})
	if Total(before) != 4 {
		t.Fatalf("before %+v", before)
	}
	rs, err := Purge(ss, []string{"14 Larkspur Close"})
	if err != nil || Total(rs) != 4 {
		t.Fatalf("purged %+v (%v)", rs, err)
	}
	if left := Total(Scan(ss, []string{"14 Larkspur Close"})); left != 0 {
		t.Fatalf("%d left", left)
	}
	data, _ := os.ReadFile(filepath.Join(home, "sessions/mine/s.jsonl"))
	if !strings.Contains(string(data), Marker) || !json.Valid([]byte(strings.TrimSpace(string(data)))) {
		t.Fatalf("store line %q", data)
	}
}
