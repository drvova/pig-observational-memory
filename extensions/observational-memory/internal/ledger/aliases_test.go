package ledger_test

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// alias is a fixture alias.
func alias(from, to string) ledger.SubjectAlias { return ledger.SubjectAlias{From: from, To: to} }

// aliasEntry is an om.subjects.aliased entry without an observation claim, as one written
// before the claim was folds by its position.
func aliasEntry(id string, aliases ...ledger.SubjectAlias) session.Entry {
	return claimedAliasEntry(id, "", aliases...)
}

// claimedAliasEntry is an om.subjects.aliased entry claiming the observations covered through
// through ("" for no claim).
func claimedAliasEntry(id, through string, aliases ...ledger.SubjectAlias) session.Entry {
	return tf.Entry(map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMSubjectsAliased,
		"data": tf.ToJSONValue(*ledger.BuildSubjectsAliasedData(aliases, through))})
}

// The real session with the alias the reflector supplies: half marathon training is about
// running for this user, so its notes join the running group with what it said as scope.
func TestCanonicalSubjectsRealSessionWithAlias(t *testing.T) {
	got := ledger.CanonicalSubjectsWithAliases(realSessionSubjects, []ledger.SubjectAlias{alias("half marathon training", "running")})
	want := map[string]ledger.Canonical{
		"running":                {Subject: "running"},
		"running training":       {Subject: "running", ExtraScope: "training"},
		"running gear":           {Subject: "running", ExtraScope: "gear"},
		"running shoes":          {Subject: "running", ExtraScope: "shoes"},
		"half marathon training": {Subject: "running", ExtraScope: "half marathon training"},
		"fitness routine":        {Subject: "fitness routine"},
		"health metrics":         {Subject: "health metrics"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalSubjectsWithAliases = %#v\nwant %#v", got, want)
	}
	// Without aliases, or with aliases the kernel refuses, nothing changes.
	plain := ledger.CanonicalSubjects(realSessionSubjects)
	for _, aliases := range [][]ledger.SubjectAlias{nil, {alias("half marathon training", "cycling")}, {alias("running", "running")}} {
		if got := ledger.CanonicalSubjectsWithAliases(realSessionSubjects, aliases); !reflect.DeepEqual(got, plain) {
			t.Errorf("aliases %v changed the grouping: %#v", aliases, got)
		}
	}
}

// A subject path groups under its root, its other segments becoming scope, whether or not the
// root is a subject of its own; a root that nests under a shorter subject goes on to it.
func TestCanonicalSubjectsPaths(t *testing.T) {
	got := ledger.CanonicalSubjects([]string{"running/half marathon", "running/half marathon/pace", "trail running/shoes", "running", "baking/sourdough"})
	want := map[string]ledger.Canonical{
		"running/half marathon":      {Subject: "running", ExtraScope: "half marathon"},
		"running/half marathon/pace": {Subject: "running", ExtraScope: "half marathon pace"},
		"trail running/shoes":        {Subject: "running", ExtraScope: "trail shoes"},
		"running":                    {Subject: "running"},
		"baking/sourdough":           {Subject: "baking", ExtraScope: "sourdough"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalSubjects = %#v\nwant %#v", got, want)
	}
	// Segments are trimmed and empty ones dropped; a single segment is a plain subject.
	for in, want := range map[string]string{" Running / half  marathon/ ": "running/half marathon", "//baking//": "baking", "running shoes": "running shoes"} {
		if got := ledger.NormalizeSubjectPath(in); got != want {
			t.Errorf("NormalizeSubjectPath(%q) = %q, want %q", in, got, want)
		}
	}
	if segments, ok := ledger.SubjectPath("running/half marathon/pace"); !ok || !reflect.DeepEqual(segments, []string{"running", "half marathon", "pace"}) {
		t.Fatalf("SubjectPath = %q, %v", segments, ok)
	}
}

// The kernel refuses a path deeper than MaxSubjectPathDepth or one whose leading segments
// spell a kernel-owned name; a refused path reads as one plain subject, and is recorded so.
func TestSubjectPathRefusals(t *testing.T) {
	for _, subject := range []string{"running/a/b/c", "om/folded", "raw/subject", "subject/scope", "canonical/subjects"} {
		if _, ok := ledger.SubjectPath(subject); ok {
			t.Errorf("SubjectPath(%q) accepted", subject)
		}
		stored, refused := ledger.RecordableSubject(subject)
		if !refused || strings.Contains(stored, "/") {
			t.Errorf("RecordableSubject(%q) = %q, %v", subject, stored, refused)
		}
	}
	if stored, refused := ledger.RecordableSubject("Running / Half Marathon"); refused || stored != "running/half marathon" {
		t.Fatalf("RecordableSubject = %q, %v", stored, refused)
	}
	// A plain subject that happens to be a kernel name is no path game and stays as written,
	// and so is a path whose root alone is one: only joined segments can spell a kernel name.
	if stored, refused := ledger.RecordableSubject("memory"); refused || stored != "memory" {
		t.Fatalf("plain subject %q, %v", stored, refused)
	}
	for _, subject := range []string{"search/indexing", "coverage/unit tests", "history/git rebase", "memory/ram", "notes/meeting"} {
		if stored, refused := ledger.RecordableSubject(subject); refused || stored != subject {
			t.Errorf("RecordableSubject(%q) = %q, %v", subject, stored, refused)
		}
	}
	if c := ledger.CanonicalSubjects([]string{"search/indexing", "search"})["search/indexing"]; c != (ledger.Canonical{Subject: "search", ExtraScope: "indexing"}) {
		t.Fatalf("owned root %#v", c)
	}
	// Read back, a refused path is a plain subject: containment still groups it.
	got := ledger.CanonicalSubjects([]string{"running", "running/a/b/c", "om/folded"})
	if c := got["running/a/b/c"]; c != (ledger.Canonical{Subject: "running", ExtraScope: "a b c"}) {
		t.Fatalf("deep path %#v", c)
	}
	if c := got["om/folded"]; c != (ledger.Canonical{Subject: "om/folded"}) {
		t.Fatalf("kernel path %#v", c)
	}
}

// Aliases are followed to their final target; the scope keeps the words of the subject the
// note's base wrote, and a path whose root is aliased keeps its other segments after them.
func TestCanonicalSubjectsTransitiveAliases(t *testing.T) {
	subjects := []string{"jogging", "jogging/shoes", "road running", "running", "running gear"}
	aliases := []ledger.SubjectAlias{alias("jogging", "road running"), alias("road running", "running")}
	got := ledger.CanonicalSubjectsWithAliases(subjects, aliases)
	want := map[string]ledger.Canonical{
		"jogging":       {Subject: "running", ExtraScope: "jogging"},
		"jogging/shoes": {Subject: "running", ExtraScope: "jogging shoes"},
		"road running":  {Subject: "running", ExtraScope: "road"},
		"running":       {Subject: "running"},
		"running gear":  {Subject: "running", ExtraScope: "gear"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalSubjectsWithAliases = %#v\nwant %#v", got, want)
	}
	// An alias matches by SubjectKey and lands on the target's present spelling.
	got = ledger.CanonicalSubjectsWithAliases([]string{"Half-Marathon Training", "running"}, []ledger.SubjectAlias{alias("half marathon training", "RUNNING")})
	if c := got["Half-Marathon Training"]; c != (ledger.Canonical{Subject: "running", ExtraScope: "half marathon training"}) {
		t.Fatalf("by key %#v", c)
	}
}

// Self-aliases and aliases that would close a cycle are ignored, earliest wins; so is a second
// alias of one subject.
func TestCheckSubjectAliasesCyclesAndSelf(t *testing.T) {
	subjects := []string{"a topic", "b topic", "c topic"}
	cases := []struct {
		aliases []ledger.SubjectAlias
		want    []ledger.SubjectAlias
		reasons []string
	}{
		{[]ledger.SubjectAlias{alias("a topic", "a topic")}, nil, []string{ledger.AliasRefusedSelf}},
		{[]ledger.SubjectAlias{alias("a topic", "b topic"), alias("b topic", "a topic")}, []ledger.SubjectAlias{alias("a topic", "b topic")}, []string{ledger.AliasRefusedCycle}},
		{[]ledger.SubjectAlias{alias("a topic", "b topic"), alias("b topic", "c topic"), alias("c topic", "a topic")}, []ledger.SubjectAlias{alias("a topic", "b topic"), alias("b topic", "c topic")}, []string{ledger.AliasRefusedCycle}},
		// The latest alias of a subject replaces the earlier one, and a self-alias retracts it.
		{[]ledger.SubjectAlias{alias("a topic", "b topic"), alias("a topic", "c topic")}, []ledger.SubjectAlias{alias("a topic", "c topic")}, []string{}},
		{[]ledger.SubjectAlias{alias("a topic", "b topic"), alias("b topic", "c topic"), alias("a topic", "a topic")}, []ledger.SubjectAlias{alias("b topic", "c topic")}, []string{}},
		{[]ledger.SubjectAlias{alias("a topic", "b topic"), alias("a topic", "b topic")}, []ledger.SubjectAlias{alias("a topic", "b topic")}, []string{ledger.AliasRefusedRepeat}},
		// A replacement that would close a cycle is refused, and the earlier alias stays.
		{[]ledger.SubjectAlias{alias("a topic", "b topic"), alias("c topic", "a topic"), alias("a topic", "c topic")}, []ledger.SubjectAlias{alias("a topic", "b topic"), alias("c topic", "a topic")}, []string{ledger.AliasRefusedCycle}},
	}
	for _, c := range cases {
		accepted, refused := ledger.CheckSubjectAliases(subjects, c.aliases)
		reasons := []string{}
		for _, r := range refused {
			reasons = append(reasons, r.Reason)
		}
		if len(accepted) == 0 {
			accepted = nil
		}
		if !reflect.DeepEqual(accepted, c.want) || !reflect.DeepEqual(reasons, c.reasons) {
			t.Errorf("%v: accepted %v refused %v", c.aliases, accepted, refused)
		}
	}
	// A cycle never loops canonicalization: the accepted aliases resolve, the rest are ignored.
	got := ledger.CanonicalSubjectsWithAliases(subjects, []ledger.SubjectAlias{alias("b topic", "a topic"), alias("a topic", "b topic")})
	if got["a topic"].Subject != "a topic" || got["b topic"] != (ledger.Canonical{Subject: "a topic", ExtraScope: "b"}) {
		t.Fatalf("cycle %#v", got)
	}
}

// An alias applies only while both subjects are present (a path subject by its root); one to
// or from an absent subject, a path, or a kernel-owned name is ignored.
func TestCheckSubjectAliasesRefusals(t *testing.T) {
	subjects := []string{"running", "jogging", "memory", "baking/sourdough", "raw_subject", "running shoes", "shoe"}
	cases := map[ledger.SubjectAlias]string{
		alias("jogging", "cycling"):          ledger.AliasRefusedAbsent,
		alias("cycling", "running"):          ledger.AliasRefusedAbsent,
		alias("sourdough", "running"):        ledger.AliasRefusedAbsent,
		alias("jogging", "baking/sourdough"): ledger.AliasRefusedPath,
		alias("raw_subject", "running"):      ledger.AliasRefusedKernel,
		alias("jogging", "raw subject"):      ledger.AliasRefusedKernel,
		alias("--", "running"):               ledger.AliasRefusedEmpty,
		alias("jogging", "jogging"):          ledger.AliasRefusedSelf,
		alias("running", "running shoes"):    ledger.AliasRefusedContained,
		alias("shoe", "running shoes"):       ledger.AliasRefusedContained,
	}
	for a, reason := range cases {
		accepted, refused := ledger.CheckSubjectAliases(subjects, []ledger.SubjectAlias{a})
		if len(accepted) != 0 || len(refused) != 1 || refused[0].Reason != reason {
			t.Errorf("%v: accepted %v refused %v; want %q", a, accepted, refused, reason)
		}
	}
	// A path subject is present by its root; a plain subject that is a kernel name other than
	// the notes source's fields may be aliased, as it may be written.
	for _, a := range []ledger.SubjectAlias{alias("baking", "running"), alias("memory", "running"), alias("jogging", "memory")} {
		if accepted, refused := ledger.CheckSubjectAliases(subjects, []ledger.SubjectAlias{a}); len(accepted) != 1 {
			t.Errorf("%v refused: %v", a, refused)
		}
	}
	// Once the target's notes are gone the alias stops applying.
	got := ledger.CanonicalSubjectsWithAliases([]string{"half marathon training"}, []ledger.SubjectAlias{alias("half marathon training", "running")})
	if c := got["half marathon training"]; c != (ledger.Canonical{Subject: "half marathon training"}) {
		t.Fatalf("absent target %#v", c)
	}
	// Record time: the recorded aliases come first, and a proposal repeating one is refused.
	recorded := []ledger.SubjectAlias{alias("jogging", "running")}
	accepted, refused := ledger.AcceptNewSubjectAliases(subjects, recorded, []ledger.SubjectAlias{alias("jogging", "running"), alias("baking", "running"), alias("running", "jogging")})
	if !reflect.DeepEqual(accepted, []ledger.SubjectAlias{alias("baking", "running")}) || len(refused) != 2 || refused[0].Reason != ledger.AliasRefusedRepeat || refused[1].Reason != ledger.AliasRefusedCycle {
		t.Fatalf("accepted %v refused %v", accepted, refused)
	}
	// A proposal a later one replaces or retracts is not recorded: the later one alone leaves
	// the same aliases in force.
	accepted, refused = ledger.AcceptNewSubjectAliases(subjects, recorded, []ledger.SubjectAlias{alias("jogging", "memory"), alias("jogging", "baking"), alias("baking", "running")})
	if !reflect.DeepEqual(accepted, []ledger.SubjectAlias{alias("jogging", "baking"), alias("baking", "running")}) || len(refused) != 0 {
		t.Fatalf("replaced: accepted %v refused %v", accepted, refused)
	}
	accepted, _ = ledger.AcceptNewSubjectAliases(subjects, recorded, []ledger.SubjectAlias{alias("jogging", "memory"), alias("jogging", "jogging")})
	if !reflect.DeepEqual(accepted, []ledger.SubjectAlias{alias("jogging", "jogging")}) {
		t.Fatalf("retracted: accepted %v", accepted)
	}
	if inForce, _ := ledger.CheckSubjectAliases(subjects, append(recorded, accepted...)); len(inForce) != 0 {
		t.Fatalf("retraction left %v in force", inForce)
	}
}

// The result depends on the subjects as a set and on aliases that do not conflict in any
// order; conflicting ones are settled by ledger order, never by the subjects' order.
func TestCanonicalSubjectsWithAliasesOrderIndependent(t *testing.T) {
	subjects := append(append([]string{}, realSessionSubjects...), "jogging", "running/pace", "workout plan", "gym")
	aliases := []ledger.SubjectAlias{alias("half marathon training", "running"), alias("jogging", "running"), alias("gym", "fitness routine"), alias("workout plan", "fitness routine")}
	want := ledger.CanonicalSubjectsWithAliases(subjects, aliases)
	r := rand.New(rand.NewSource(2))
	for range 50 {
		s := append(append([]string{}, subjects...), subjects[r.Intn(len(subjects))], "")
		r.Shuffle(len(s), func(i, j int) { s[i], s[j] = s[j], s[i] })
		a := append([]ledger.SubjectAlias{}, aliases...)
		r.Shuffle(len(a), func(i, j int) { a[i], a[j] = a[j], a[i] })
		if got := ledger.CanonicalSubjectsWithAliases(s, a); !reflect.DeepEqual(got, want) {
			t.Fatalf("order %q %v: %#v\nwant %#v", s, a, got, want)
		}
	}
	conflicting := append(append([]ledger.SubjectAlias{}, aliases...), alias("running", "jogging"))
	first := ledger.CanonicalSubjectsWithAliases(subjects, conflicting)
	for range 20 {
		s := append([]string{}, subjects...)
		r.Shuffle(len(s), func(i, j int) { s[i], s[j] = s[j], s[i] })
		if got := ledger.CanonicalSubjectsWithAliases(s, conflicting); !reflect.DeepEqual(got, first) || got["running"].Subject != "running" {
			t.Fatalf("conflict settled differently: %#v", got)
		}
	}
}

// Canonicalizing the canonical subjects again, with the same aliases, maps each to itself.
func TestCanonicalSubjectsWithAliasesIdempotent(t *testing.T) {
	subjects := append(append([]string{}, realSessionSubjects...), "jogging", "running/pace", "trail running/shoes", "gym")
	aliases := []ledger.SubjectAlias{alias("half marathon training", "running"), alias("jogging", "half marathon training"), alias("gym", "fitness routine")}
	canonical := map[string]bool{}
	for _, c := range ledger.CanonicalSubjectsWithAliases(subjects, aliases) {
		canonical[c.Subject] = true
	}
	again := []string{}
	for s := range canonical {
		again = append(again, s)
	}
	for s, c := range ledger.CanonicalSubjectsWithAliases(again, aliases) {
		if c.Subject != s || c.ExtraScope != "" {
			t.Errorf("canonical %q maps to %#v", s, c)
		}
	}
}

// Alias entries fold like the other records: through the boundary the observations fold
// through, into the compaction's details, and back out of them once the raw entries are
// compacted; they never move a coverage marker.
func TestAliasesFoldAcrossCompaction(t *testing.T) {
	run := tf.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.Subject = "running" })
	half := tf.Observation("bbbbbbbbbbbb", func(o *ledger.Observation) { o.Subject = "half marathon training" })
	gym := tf.Observation("cccccccccccc", func(o *ledger.Observation) { o.Subject = "gym" })
	fit := tf.Observation("dddddddddddd", func(o *ledger.Observation) { o.Subject = "fitness routine" })
	early, late := alias("half marathon training", "running"), alias("gym", "fitness routine")
	entries := []session.Entry{
		tf.RawMessage("raw-1", "one"),
		tf.ObservationsRecordedEntry("obs-1", []ledger.Observation{run, half}, "raw-1"),
		claimedAliasEntry("alias-1", "raw-1", early),
		tf.RawMessage("raw-2", "two"),
		tf.ObservationsRecordedEntry("obs-2", []ledger.Observation{gym, fit}, "raw-2"),
		claimedAliasEntry("alias-2", "raw-2", late),
		tf.RawMessage("raw-3", "three"),
	}
	before := latestMarker(entries, ledger.OMObservationsRecorded)
	if before != "raw-2" || latestMarker(entries[:6], ledger.OMReflectionsRecorded) != "" {
		t.Fatalf("coverage %q", before)
	}
	if folded := ledger.FoldLedger(entries, ""); !reflect.DeepEqual(folded.Aliases, []ledger.SubjectAlias{early, late}) {
		t.Fatalf("folded aliases %v", folded.Aliases)
	}
	if folded := ledger.FoldLedger(entries, "raw-2"); !reflect.DeepEqual(folded.Aliases, []ledger.SubjectAlias{early}) {
		t.Fatalf("folded through raw-2 %v", folded.Aliases)
	}
	// A compaction keeping raw-3 folds both aliases into its details.
	projection := ledger.BuildCompactionProjection(entries, "raw-3", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 10_000})
	if !reflect.DeepEqual(projection.Aliases, []ledger.SubjectAlias{early, late}) || !reflect.DeepEqual(projection.Details.Aliases, projection.Aliases) {
		t.Fatalf("projection aliases %v details %v", projection.Aliases, projection.Details.Aliases)
	}
	// One keeping raw-2 folds obs-2, whose notes cover raw-2, and so the alias about them,
	// though the reflector appended it after them; one keeping raw-1 folds neither.
	if p := ledger.BuildCompactionProjection(entries, "raw-2", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 10_000}); !reflect.DeepEqual(p.Aliases, []ledger.SubjectAlias{early, late}) {
		t.Fatalf("aliases through raw-2 %v", p.Aliases)
	} else if c := ledger.ObservationSubjectsWithAliases(p.Observations, p.Aliases); c["gym"].Subject != "fitness routine" {
		t.Fatalf("grouping through raw-2 %#v", c)
	}
	if p := ledger.BuildCompactionProjection(entries, "raw-1", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 10_000}); !reflect.DeepEqual(p.Aliases, []ledger.SubjectAlias{early}) {
		t.Fatalf("aliases through raw-1 %v", p.Aliases)
	}
	// An entry without the claim folds by its own position.
	legacy := append(append([]session.Entry{}, entries[:5]...), aliasEntry("alias-2", late), entries[6])
	if p := ledger.BuildCompactionProjection(legacy, "raw-2", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 10_000}); !reflect.DeepEqual(p.Aliases, []ledger.SubjectAlias{early}) {
		t.Fatalf("legacy aliases through raw-2 %v", p.Aliases)
	}
	// The details round-trip through JSON, and the compacted memory groups as the ledger did.
	details := tf.ToJSONValue(projection.Details)
	compacted := append(append([]session.Entry{}, entries...), tf.CompactionEntry("compaction-1", tf.CompactionArgs{FirstKeptEntryID: "raw-3", Details: details}))
	visible := ledger.VisibleProjection(compacted, "")
	if !reflect.DeepEqual(visible.Aliases, []ledger.SubjectAlias{early, late}) {
		t.Fatalf("visible aliases %v", visible.Aliases)
	}
	canonical := ledger.ObservationSubjectsWithAliases(visible.Observations, visible.Aliases)
	if canonical["half marathon training"].Subject != "running" || canonical["gym"].Subject != "fitness routine" {
		t.Fatalf("compacted grouping %#v", canonical)
	}
	if got := latestMarker(compacted, ledger.OMObservationsRecorded); got != before {
		t.Fatalf("coverage moved to %q", got)
	}
	// Details without aliases carry no aliases key, so they stay byte for byte as before.
	plain, _ := json.Marshal(ledger.BuildCompactionProjection(entries[:2], "raw-1", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 10_000}).Details)
	if strings.Contains(string(plain), "aliases") {
		t.Fatalf("details %s", plain)
	}
	// A malformed alias entry is no record.
	bad := tf.Entry(map[string]any{"type": "custom", "id": "bad", "customType": ledger.OMSubjectsAliased, "data": map[string]any{"aliases": []any{map[string]any{"from": "x"}}}})
	if _, ok := ledger.SubjectsAliasedEntry(bad); ok {
		t.Fatal("malformed alias entry read")
	}
}

// One entry carries at most MaxSubjectAliasesPerEntry aliases.
func TestSubjectsAliasedDataBounded(t *testing.T) {
	many := []ledger.SubjectAlias{}
	items := []any{}
	for i := range ledger.MaxSubjectAliasesPerEntry + 4 {
		a := alias("from "+padID(string(rune('a'+i)), 'a'), "to")
		many = append(many, a)
		items = append(items, map[string]any{"from": a.From, "to": a.To})
	}
	if data := ledger.BuildSubjectsAliasedData(many, "raw-1"); len(data.Aliases) != ledger.MaxSubjectAliasesPerEntry {
		t.Fatalf("built %d", len(data.Aliases))
	}
	if data, ok := ledger.SubjectsAliasedDataFrom(map[string]any{"aliases": items}); !ok || len(data.Aliases) != ledger.MaxSubjectAliasesPerEntry {
		t.Fatalf("read %d", len(data.Aliases))
	}
	if ledger.BuildSubjectsAliasedData(nil, "raw-1") != nil {
		t.Fatal("empty payload built")
	}
}

// An alias moves the subjects containment grouped under its from along with it: they keep
// their containment words as scope after the from's, so one alias never regroups subjects it
// did not name.
func TestAliasCarriesContainmentChildren(t *testing.T) {
	subjects := []string{"running", "running shoes", "fitness", "trail running/pace"}
	plain := ledger.CanonicalSubjects(subjects)
	if plain["running shoes"] != (ledger.Canonical{Subject: "running", ExtraScope: "shoes"}) {
		t.Fatalf("plain %#v", plain)
	}
	got := ledger.CanonicalSubjectsWithAliases(subjects, []ledger.SubjectAlias{alias("running", "fitness")})
	want := map[string]ledger.Canonical{
		"running":            {Subject: "fitness", ExtraScope: "running"},
		"running shoes":      {Subject: "fitness", ExtraScope: "running shoes"},
		"fitness":            {Subject: "fitness"},
		"trail running/pace": {Subject: "fitness", ExtraScope: "running trail pace"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalSubjectsWithAliases = %#v\nwant %#v", got, want)
	}
	// An alias to a subject that nests under a shorter one goes on to it, and on through that
	// subject's own alias; an alias that would loop through containment is refused.
	subjects = []string{"jogging", "trail run", "run", "cardio"}
	got = ledger.CanonicalSubjectsWithAliases(subjects, []ledger.SubjectAlias{alias("jogging", "trail run"), alias("run", "cardio")})
	if got["jogging"] != (ledger.Canonical{Subject: "cardio", ExtraScope: "jogging"}) || got["trail run"] != (ledger.Canonical{Subject: "cardio", ExtraScope: "run trail"}) {
		t.Fatalf("through containment %#v", got)
	}
	_, refused := ledger.CheckSubjectAliases(subjects, []ledger.SubjectAlias{alias("jogging", "trail run"), alias("run", "jogging")})
	if len(refused) != 1 || refused[0].Reason != ledger.AliasRefusedCycle {
		t.Fatalf("containment cycle refused %v", refused)
	}
	// Canonicalizing the canonical subjects again maps each to itself.
	canonical := []string{}
	for _, c := range ledger.CanonicalSubjectsWithAliases(subjects, []ledger.SubjectAlias{alias("jogging", "trail run"), alias("run", "cardio")}) {
		canonical = append(canonical, c.Subject)
	}
	for s, c := range ledger.CanonicalSubjectsWithAliases(canonical, []ledger.SubjectAlias{alias("jogging", "trail run"), alias("run", "cardio")}) {
		if c.Subject != s || c.ExtraScope != "" {
			t.Errorf("canonical %q maps to %#v", s, c)
		}
	}
}

// The latest alias of a subject that applies wins: an earlier alias that stopped applying
// (its target's notes were dropped) does not come back over a later one when its target
// reappears, and a wrong alias is corrected by a later one.
func TestLatestAliasWins(t *testing.T) {
	ab, ac := alias("a topic", "b topic"), alias("a topic", "c topic")
	// a -> b recorded; every b note dropped, so it stops applying and the reflector records
	// a -> c.
	withoutB := []string{"a topic", "c topic"}
	if inForce, _ := ledger.CheckSubjectAliases(withoutB, []ledger.SubjectAlias{ab}); len(inForce) != 0 {
		t.Fatalf("a -> b applies without b: %v", inForce)
	}
	accepted, _ := ledger.AcceptNewSubjectAliases(withoutB, []ledger.SubjectAlias{ab}, []ledger.SubjectAlias{ac})
	if !reflect.DeepEqual(accepted, []ledger.SubjectAlias{ac}) {
		t.Fatalf("accepted %v", accepted)
	}
	// A new b note: a stays under c.
	withB := []string{"a topic", "b topic", "c topic"}
	recorded := []ledger.SubjectAlias{ab, ac}
	if inForce, _ := ledger.CheckSubjectAliases(withB, recorded); !reflect.DeepEqual(inForce, []ledger.SubjectAlias{ac}) {
		t.Fatalf("in force %v", inForce)
	}
	if c := ledger.CanonicalSubjectsWithAliases(withB, recorded)["a topic"]; c.Subject != "c topic" {
		t.Fatalf("a revived under %#v", c)
	}
	// A wrong alias is corrected by a later one, and undone by a self-alias.
	if c := ledger.CanonicalSubjectsWithAliases(withB, []ledger.SubjectAlias{ac, ab})["a topic"]; c.Subject != "b topic" {
		t.Fatalf("corrected to %#v", c)
	}
	if c := ledger.CanonicalSubjectsWithAliases(withB, []ledger.SubjectAlias{ac, alias("a topic", "a topic")})["a topic"]; c != (ledger.Canonical{Subject: "a topic"}) {
		t.Fatalf("undone to %#v", c)
	}
}

// A subject stored before subject paths reads back as written: NormalizeSubject keeps its
// spelling, and only the observer's record normalizes a written path.
func TestLegacySlashSubjectsReadAsWritten(t *testing.T) {
	for in, want := range map[string]string{"CI / CD": "ci / cd", "/": "/", "tcp/ip": "tcp/ip"} {
		if got := ledger.NormalizeSubject(in); got != want {
			t.Errorf("NormalizeSubject(%q) = %q, want %q", in, got, want)
		}
	}
	got := ledger.CanonicalSubjects([]string{"/", "ci / cd"})
	if got["/"] != (ledger.Canonical{Subject: "/"}) || got["ci / cd"] != (ledger.Canonical{Subject: "ci", ExtraScope: "cd"}) {
		t.Fatalf("legacy subjects %#v", got)
	}
}

// padID repeats fill after prefix up to an observation id's 12 characters.
func padID(prefix string, fill rune) string {
	return prefix + strings.Repeat(string(fill), 12-len(prefix))
}
