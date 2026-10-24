package agents_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/declare"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// schemaRun runs the observer over one JSON item and one rows table with schema, and returns
// what it recorded and the request it sent.
func schemaRun(t *testing.T, schema *ledger.NoteSchema, rows string, item map[string]any) ([]ledger.Observation, map[string]any) {
	t.Helper()
	rec := &tf.StreamRecorder{Replies: []map[string]any{
		tf.AssistantToolCall("tool-1", "record_observations", map[string]any{"rows": rows, "observations": []any{item}}),
		tf.AssistantText("done"),
	}}
	args := detailedArgs(rec)
	args.Schema = schema
	observations, err := agents.RunObserver(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	return observations, rec.Requests[0]
}

// The seed schema is exactly the observer's built-in fields: the same tool parameters, the
// same system prompt and the same records as a run with no schema at all.
func TestObserverSeedSchemaIsIdenticalToNoSchema(t *testing.T) {
	seed := declare.SeedsOnly("p/m").Schema()
	if !reflect.DeepEqual(seed.Fields, agents.DefaultSchemaFields()) {
		t.Fatalf("seed schema %+v differs from the built-in fields %+v", seed.Fields, agents.DefaultSchemaFields())
	}
	rows := "observations[1]{timestamp,relevance,kind,subject,scope,supersedes,sourceEntryIds,content}:\n  2026-05-02 10:30,high,fact,Baking,at home,,raw-1,User bakes sourdough weekly."
	item := map[string]any{"timestamp": "2026-05-02 10:31", "content": "User owns a convection oven.", "relevance": "medium", "sourceEntryIds": []any{"raw-1"}, "kind": "rumor", "scope": " oven "}
	without, reqWithout := schemaRun(t, nil, rows, item)
	with, reqWith := schemaRun(t, &seed, rows, item)
	if !reflect.DeepEqual(without, with) || len(with) != 2 {
		t.Fatalf("records differ:\n%+v\n%+v", without, with)
	}
	if !reflect.DeepEqual(reqWithout["tools"], reqWith["tools"]) || reqWithout["systemPrompt"] != reqWith["systemPrompt"] {
		t.Fatal("the seed schema changed the request")
	}
}

// A model's schema adds fields (validated and stored with the note), narrows kind, and leaves
// out a field it does not declare, which the rows table then refuses.
func TestObserverSchemaAddsNarrowsAndOmitsFields(t *testing.T) {
	schema, err := ledger.ParseNoteSchema("schema[4]{field,type,values,max,description}:\n" +
		"  kind,enum,fact plan,,\n" +
		"  subject,text,,,\n" +
		"  mood,enum,happy sad,,How the user felt\n" +
		"  amount,number,,,")
	if err != nil {
		t.Fatal(err)
	}
	rows := "observations[1]{timestamp,relevance,kind,subject,mood,amount,sourceEntryIds,content}:\n  2026-05-02 10:30,high,plan,Trips,Happy,12.5,raw-1,User plans a trip to Lisbon."
	item := map[string]any{"timestamp": "2026-05-02 10:31", "content": "User corrected the trip date.", "relevance": "medium", "sourceEntryIds": []any{"raw-1"},
		"kind": "correction", "scope": "may", "mood": "angry", "amount": "a lot"}
	got, req := schemaRun(t, &schema, rows, item)
	if len(got) != 2 {
		t.Fatalf("records %+v", got)
	}
	second, first := got[0], got[1]
	if first.Kind != "plan" || first.Subject != "trips" || !reflect.DeepEqual(first.Fields, map[string]string{"mood": "happy", "amount": "12.5"}) {
		t.Fatalf("first %+v", first)
	}
	if second.Kind != "" || second.Scope != "" || second.Fields != nil {
		t.Fatalf("second: an undeclared kind, an omitted field or an invalid value was kept: %+v", second)
	}
	encoded := fmt.Sprint(req["tools"])
	if !strings.Contains(encoded, "timestamp,relevance,kind,subject,mood,amount,sourceEntryIds,content") || !strings.Contains(encoded, "How the user felt") || strings.Contains(encoded, "Qualifier that tells") {
		t.Fatalf("tool schema %s", encoded)
	}
	// The omitted scope column is refused in rows.
	bad := "observations[1]{timestamp,relevance,scope,sourceEntryIds,content}:\n  2026-05-02 10:30,high,may,raw-1,Anything."
	rec := &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantToolCall("tool-1", "record_observations", map[string]any{"rows": bad}), tf.AssistantText("done")}}
	args := detailedArgs(rec)
	args.Schema = &schema
	if observations, _ := agents.RunObserver(context.Background(), args); observations != nil {
		t.Fatalf("recorded %+v", observations)
	}
	if text := lastToolResultText(t, rec); !strings.Contains(text, `unknown column "scope"`) {
		t.Fatalf("ack %q", text)
	}
}
