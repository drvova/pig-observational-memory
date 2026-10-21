// Package clipboard copies text to the system clipboard through the platform's
// command-line tools.
package clipboard

import (
	"bytes"
	"context"
	"os/exec"
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
			{Command: "wl-copy"},
			{Command: "xclip", Args: []string{"-selection", "clipboard"}},
			{Command: "xsel", Args: []string{"--clipboard", "--input"}},
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
