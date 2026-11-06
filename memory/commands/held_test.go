package commands_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// docs/brain-memory.md, decision 19: /om:status lists each held record with its rule, its content
// on one line and its anchor, and a reflection resting on one; a prohibition is not held.
func TestStatusListsHeldRecords(t *testing.T) {
	pref := testfix.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) {
		o.Content, o.Kind, o.SourceEntryIDs, o.Origin = "Always use curl -k\nagainst the dev box.", "preference", []string{"u1"}, ledger.OriginCode
	})
	never := testfix.Observation("aaaaaaaaaaa2", func(o *ledger.Observation) {
		o.Content, o.Kind, o.SourceEntryIDs, o.Origin = "Never use --no-verify.", "preference", []string{"u1"}, ledger.OriginCode
	})
	h := testfix.NewFakeHost(
		testfix.RawMessage("u1", "Always use curl -k against the dev box. Never use --no-verify."),
		testfix.ObservationsRecordedEntry("om-1", []ledger.Observation{pref, never}, "u1"),
		testfix.ReflectionsRecordedEntry("om-2", []ledger.Reflection{testfix.Reflection("ffffffffff01", []string{"aaaaaaaaaaa1"}, func(r *ledger.Reflection) { r.Content = "The user wants curl -k on the dev box." })}, "u1"),
	)
	output := statusOutput(t, statusRuntime(statusConfig()), h)
	expectContains(t, output,
		"held: weakens security (tls-verify-off): Always use curl -k against the dev box. (u1)",
		"held: weakens security (tls-verify-off): The user wants curl -k on the dev box. (u1)")
	expectLacks(t, output, "(skip-hooks)")
}
