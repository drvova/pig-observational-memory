// Package clipboard copies text to the system clipboard through the platform's
// command-line tools.
package clipboard

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
	"unicode/utf16"
)

// Command is one clipboard program invocation.
type Command struct {
	Command string
	Args    []string
	// UTF16 sends the text as UTF-16LE with a byte order mark.
	UTF16 bool
	// Env, when set, names the environment variable that must be non-empty
	// for the command to reach a clipboard (the Wayland or X11 display).
	Env string
	// Socket, when set, names a file in $XDG_RUNTIME_DIR that lets the
	// command reach a clipboard with Env unset: libwayland-client falls back
	// to the wayland-0 socket there when WAYLAND_DISPLAY is unset (a tmux
	// server started before the compositor exported it).
	Socket string
}

// Runner runs one command with text on stdin and reports success.
type Runner func(command Command, text string) bool

// Commands lists the clipboard programs to try on a platform (GOOS value).
func Commands(platform string) []Command {
	switch platform {
	case "darwin":
		return []Command{{Command: "pbcopy"}}
	case "windows":
		// clip.exe decodes piped input in the console code page unless it
		// starts with a UTF-16 byte order mark.
		return []Command{{Command: "clip", UTF16: true}}
	default:
		return []Command{
			{Command: "wl-copy", Env: "WAYLAND_DISPLAY", Socket: "wayland-0"},
			{Command: "xclip", Args: []string{"-selection", "clipboard"}, Env: "DISPLAY"},
			{Command: "xsel", Args: []string{"--clipboard", "--input"}, Env: "DISPLAY"},
			{Command: "termux-clipboard-set"},
		}
	}
}

// Copy tries each command in order until one succeeds.
func Copy(text string, runner Runner, commands []Command) bool {
	if runner == nil {
		runner = Run
	}
	if commands == nil {
		commands = Commands(runtime.GOOS)
	}
	for _, command := range commands {
		if runner(command, text) {
			return true
		}
	}
	return false
}

// Outcome is what a copy attempt did.
type Outcome int

// The copy outcomes.
const (
	// Failed: a usable clipboard command ran and none succeeded.
	Failed Outcome = iota
	// Copied: a clipboard command took the text.
	Copied
	// Unavailable: no clipboard command can reach a clipboard here (none is
	// installed, or the display it needs is unset and has no default socket),
	// so none was run.
	Unavailable
)

// Usable keeps the commands that can reach a clipboard: found by lookPath and,
// for one that needs a display, with its Env set by getenv or its Socket
// present (exists) in getenv's XDG_RUNTIME_DIR.
func Usable(commands []Command, lookPath func(string) (string, error), getenv func(string) string, exists func(string) bool) []Command {
	usable := []Command{}
	for _, command := range commands {
		if command.Env != "" && getenv(command.Env) == "" && !hasSocket(command, getenv, exists) {
			continue
		}
		if _, err := lookPath(command.Command); err != nil {
			continue
		}
		usable = append(usable, command)
	}
	return usable
}

// hasSocket reports whether command's Socket exists in XDG_RUNTIME_DIR.
func hasSocket(command Command, getenv func(string) string, exists func(string) bool) bool {
	dir := getenv("XDG_RUNTIME_DIR")
	if command.Socket == "" || dir == "" || exists == nil {
		return false
	}
	return exists(filepath.Join(dir, command.Socket))
}

// fileExists reports whether path names an existing file of any kind.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// CopyIfAvailable copies text with this platform's usable commands, and runs
// none when there is no usable one (a headless session).
func CopyIfAvailable(text string) Outcome {
	usable := Usable(Commands(runtime.GOOS), exec.LookPath, os.Getenv, fileExists)
	if len(usable) == 0 {
		return Unavailable
	}
	if Copy(text, nil, usable) {
		return Copied
	}
	return Failed
}

// Input is the bytes written to command's stdin for text.
func Input(command Command, text string) []byte {
	if !command.UTF16 {
		return []byte(text)
	}
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2+2*len(units))
	out = append(out, 0xff, 0xfe)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

// Timeout bounds one clipboard command.
const Timeout = 2 * time.Second

// Run executes one clipboard command with text on stdin.
func Run(command Command, text string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Command, command.Args...)
	cmd.Stdin = bytes.NewReader(Input(command, text))
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
