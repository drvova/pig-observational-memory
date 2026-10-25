package clipboard_test

import (
	"errors"
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

// A command is usable when it is installed and, for the Linux display tools,
// its display is set: a headless session has none, so nothing is run.
func TestUsableNeedsCommandAndDisplay(t *testing.T) {
	installed := map[string]bool{"wl-copy": true, "xclip": true}
	lookPath := func(name string) (string, error) {
		if installed[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	env := map[string]string{}
	getenv := func(key string) string { return env[key] }
	sockets := map[string]bool{}
	exists := func(path string) bool { return sockets[path] }
	linux := clipboard.Commands("linux")

	if got := clipboard.Usable(linux, lookPath, getenv, exists); len(got) != 0 {
		t.Fatalf("headless usable %v", names(got))
	}
	env["DISPLAY"] = ":0"
	if got := names(clipboard.Usable(linux, lookPath, getenv, exists)); !reflect.DeepEqual(got, []string{"xclip"}) {
		t.Fatalf("X11 usable %v", got)
	}
	env["WAYLAND_DISPLAY"] = "wayland-0"
	if got := names(clipboard.Usable(linux, lookPath, getenv, exists)); !reflect.DeepEqual(got, []string{"wl-copy", "xclip"}) {
		t.Fatalf("Wayland usable %v", got)
	}
	// libwayland's default socket reaches the compositor with WAYLAND_DISPLAY
	// unset; it is looked up only under XDG_RUNTIME_DIR.
	delete(env, "WAYLAND_DISPLAY")
	sockets["/run/user/1000/wayland-0"] = true
	if got := names(clipboard.Usable(linux, lookPath, getenv, exists)); !reflect.DeepEqual(got, []string{"xclip"}) {
		t.Fatalf("socket without XDG_RUNTIME_DIR usable %v", got)
	}
	env["XDG_RUNTIME_DIR"] = "/run/user/1000"
	if got := names(clipboard.Usable(linux, lookPath, getenv, exists)); !reflect.DeepEqual(got, []string{"wl-copy", "xclip"}) {
		t.Fatalf("default Wayland socket usable %v", got)
	}
	installed["termux-clipboard-set"] = true
	env = map[string]string{}
	if got := names(clipboard.Usable(linux, lookPath, getenv, exists)); !reflect.DeepEqual(got, []string{"termux-clipboard-set"}) {
		t.Fatalf("termux usable %v", got)
	}
	// pbcopy and clip need no display variable.
	installed["pbcopy"] = true
	if got := names(clipboard.Usable(clipboard.Commands("darwin"), lookPath, getenv, exists)); !reflect.DeepEqual(got, []string{"pbcopy"}) {
		t.Fatalf("darwin usable %v", got)
	}
	if got := clipboard.Usable(clipboard.Commands("windows"), lookPath, getenv, exists); len(got) != 0 {
		t.Fatalf("missing clip usable %v", names(got))
	}
}
