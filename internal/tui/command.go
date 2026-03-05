package tui

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// CommandResult holds the outcome of a colon command.
type CommandResult struct {
	Output string
	Quit   bool
}

// CommandHandler processes a colon command and returns its result.
type CommandHandler func(args string) CommandResult

// CommandRegistry maps command names to handlers.
type CommandRegistry struct {
	commands map[string]CommandHandler
}

// NewCommandRegistry creates a registry with the default commands registered.
func NewCommandRegistry() *CommandRegistry {
	r := &CommandRegistry{
		commands: make(map[string]CommandHandler),
	}

	r.Register("clear", func(args string) CommandResult {
		return CommandResult{Output: ""}
	})

	r.Register("quit", func(args string) CommandResult {
		return CommandResult{Quit: true}
	})

	r.Register("q", func(args string) CommandResult {
		return CommandResult{Quit: true}
	})

	r.Register("panel", func(args string) CommandResult {
		return CommandResult{Output: "panel"}
	})

	r.Register("log", func(args string) CommandResult {
		n := 20
		if args != "" {
			if parsed, err := strconv.Atoi(args); err == nil && parsed > 0 {
				n = parsed
			}
		}
		path := ServerLogPath()
		lines, err := tailFile(path, n)
		if err != nil {
			return CommandResult{Output: fmt.Sprintf("no log file: %v", err)}
		}
		display := path
		if home, err := os.UserHomeDir(); err == nil {
			display = strings.Replace(path, home, "~", 1)
		}
		return CommandResult{Output: display + "\n" + strings.Join(lines, "\n")}
	})

	r.Register("help", func(args string) CommandResult {
		names := r.List()
		var b strings.Builder
		b.WriteString("available commands:\n")
		for _, name := range names {
			b.WriteString("  :")
			b.WriteString(name)
			b.WriteString("\n")
		}
		return CommandResult{Output: b.String()}
	})

	return r
}

// Register adds a command handler under the given name.
func (r *CommandRegistry) Register(name string, handler CommandHandler) {
	r.commands[name] = handler
}

// Execute parses the input, looks up the command, and calls its handler.
// It returns the result and true if the command was found, or a zero
// CommandResult and false if not found.
func (r *CommandRegistry) Execute(input string) (CommandResult, bool) {
	name, args := ParseCommand(input)
	if name == "" {
		return CommandResult{}, false
	}
	handler, ok := r.commands[name]
	if !ok {
		return CommandResult{}, false
	}
	return handler(args), true
}

// List returns a sorted list of all registered command names.
func (r *CommandRegistry) List() []string {
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// tailFile reads the last n lines from a file by seeking from the end.
func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}

	// Read up to 64KB from the end — enough for any reasonable tail
	readSize := int64(64 * 1024)
	if readSize > size {
		readSize = size
	}
	buf := make([]byte, readSize)
	if _, err := f.ReadAt(buf, size-readSize); err != nil {
		return nil, err
	}

	text := strings.TrimRight(string(buf), "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// ParseCommand trims the leading ":" from input and splits on the first
// space to produce a command name and remaining args. Both are trimmed of
// surrounding whitespace. If input is just ":" the name and args are empty.
func ParseCommand(input string) (name string, args string) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, ":") {
		return "", ""
	}
	input = input[1:] // strip the colon
	input = strings.TrimSpace(input)
	if input == "" {
		return "", ""
	}
	parts := strings.SplitN(input, " ", 2)
	name = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return name, args
}
