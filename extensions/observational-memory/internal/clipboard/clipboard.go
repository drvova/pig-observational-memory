// Package clipboard copies text to the system clipboard through the platform's
// command-line tools.
package clipboard

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Command is one clipboard program invocation.
type Command struct {
	Command string
	Args    []string
}

// Runner runs one command with text on stdin and reports success.
type Runner func(command Command, text string) bool

// Commands lists the clipboard programs to try on a platform (GOOS value).
func Commands(platform string) []Command {
	switch platform {
	case "darwin":
		return []Command{{Command: "pbcopy"}}
	case "windows":
		return []Command{{Command: "clip"}}
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

// Timeout bounds one clipboard command.
const Timeout = 2 * time.Second

// Run executes one clipboard command with text on stdin.
func Run(command Command, text string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Command, command.Args...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
