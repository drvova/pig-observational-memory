package omhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// A read answering a call that is cancelled while it scans stops between rows: the scan returns
// the context's error well before its last row, and its answer is never built.
func TestSlowReadStopsOnCancel(t *testing.T) {
	rows := Rows{Fields: []string{"id"}}
	for i := 0; i < 10000; i++ {
		rows.Rows = append(rows.Rows, []string{fmt.Sprint(i)})
	}
	tb := newTable("history", rows, nil)
	ctx, cancel := context.WithCancel(context.Background())
	seen := 0
	err := tb.keep(ctx, func(i int) bool {
		seen++
		if i == 10 {
			cancel() // the user's abort arrives while the read scans
		}
		return true
	})
	if !errors.Is(err, context.Canceled) || seen >= 10000 || seen > 300 {
		t.Fatalf("err %v after %d rows", err, seen)
	}
	// A call answered after the cancel stops before it reads anything.
	var entries []session.Entry
	for i := 0; i < 50; i++ {
		entries = append(entries, tf.RawMessage(fmt.Sprintf("u%d", i), "x"))
	}
	raw, _ := json.Marshal([]any{omwire.Options{}})
	if _, err := (&Call{Entries: entries}).Call(ctx, omwire.MHistory, raw); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
