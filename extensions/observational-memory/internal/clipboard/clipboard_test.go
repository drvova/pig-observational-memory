package clipboard_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/clipboard"
)

func names(commands []clipboard.Command) []string {
	out := []string{}
	for _, c := range commands {
		out = append(out, c.Command)
	}
	return out
}

func TestCommands(t *testing.T) {
	if !reflect.DeepEqual(names(clipboard.Commands("darwin")), []string{"pbcopy"}) || !reflect.DeepEqual(names(clipboard.Commands("windows")), []string{"clip"}) {
		t.Fatal("platform commands wrong")
	}
	if !reflect.DeepEqual(names(clipboard.Commands("linux")), []string{"wl-copy", "xclip", "xsel", "termux-clipboard-set"}) {
		t.Fatal("linux commands wrong")
	}
}

func TestCopyStopsAtFirstSuccess(t *testing.T) {
	commands := []clipboard.Command{{Command: "first"}, {Command: "second"}, {Command: "third"}}
	calls := []string{}
	runner := func(command clipboard.Command, _ string) bool {
		calls = append(calls, command.Command)
		return command.Command == "second"
	}
	if !clipboard.Copy("text", runner, commands) || !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("calls %v", calls)
	}
	calls = nil
	if clipboard.Copy("text", func(clipboard.Command, string) bool { calls = append(calls, "x"); return false }, commands[:2]) || len(calls) != 2 {
		t.Fatal("all-fail copy wrong")
	}
}
