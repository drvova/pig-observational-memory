package clipboard_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/clipboard"
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

// clip.exe gets UTF-16LE with a byte order mark; other commands get UTF-8.
func TestWindowsClipboardInputIsUTF16(t *testing.T) {
	clip := clipboard.Commands("windows")[0]
	got := clipboard.Input(clip, "── a")
	want := []byte{0xff, 0xfe, 0x00, 0x25, 0x00, 0x25, 0x20, 0x00, 0x61, 0x00}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("clip input % x", got)
	}
	if got := clipboard.Input(clipboard.Commands("darwin")[0], "── a"); string(got) != "── a" {
		t.Fatalf("pbcopy input % x", got)
	}
}
