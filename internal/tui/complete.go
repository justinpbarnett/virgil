package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/justinpbarnett/virgil/internal/pipe"
)

// Completer provides tab completion for pipe names, flags, and commands.
type Completer struct {
	commands []string
	pipes    []pipe.Definition
	index    int
	matches  []string
	prefix   string
}

// NewCompleter creates a completer with command names.
func NewCompleter(cmdNames []string) *Completer {
	return &Completer{commands: cmdNames}
}

// LoadPipes fetches pipe definitions from the server.
func (c *Completer) LoadPipes(serverAddr string) {
	resp, err := signalClient.Get(fmt.Sprintf("http://%s/pipes", serverAddr))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var defs []pipe.Definition
	if err := json.NewDecoder(resp.Body).Decode(&defs); err != nil {
		return
	}
	c.pipes = defs
}

// Complete returns the next completion for the given input text.
// Returns the full completed text and a ghost suffix to display.
func (c *Completer) Complete(text string) (completed string, ghost string) {
	if c.prefix != text {
		c.prefix = text
		c.matches = c.findMatches(text)
		c.index = 0
	} else {
		c.index = (c.index + 1) % max(len(c.matches), 1)
	}

	if len(c.matches) == 0 {
		return text, ""
	}

	match := c.matches[c.index]
	return match, match[len(text):]
}

// Reset clears the current completion state.
func (c *Completer) Reset() {
	c.prefix = ""
	c.matches = nil
	c.index = 0
}

func (c *Completer) findMatches(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// Command completion: starts with ":"
	if strings.HasPrefix(text, ":") {
		prefix := text[1:]
		var matches []string
		for _, name := range c.commands {
			if strings.HasPrefix(name, prefix) && name != prefix {
				matches = append(matches, ":"+name)
			}
		}
		sort.Strings(matches)
		return matches
	}

	// Flag completion: contains "--"
	parts := strings.Fields(text)
	if len(parts) >= 2 {
		last := parts[len(parts)-1]
		if strings.HasPrefix(last, "--") {
			pipeName := parts[0]
			flagPrefix := strings.TrimPrefix(last, "--")
			return c.completeFlags(pipeName, flagPrefix, text, last)
		}
	}

	// Pipe name completion
	var matches []string
	for _, p := range c.pipes {
		if strings.HasPrefix(p.Name, text) && p.Name != text {
			matches = append(matches, p.Name)
		}
	}
	sort.Strings(matches)
	return matches
}

func (c *Completer) completeFlags(pipeName, flagPrefix, fullText, lastWord string) []string {
	for _, p := range c.pipes {
		if p.Name != pipeName {
			continue
		}
		var matches []string
		for flagName := range p.Flags {
			if strings.HasPrefix(flagName, flagPrefix) {
				completed := strings.TrimSuffix(fullText, lastWord) + "--" + flagName
				matches = append(matches, completed)
			}
		}
		sort.Strings(matches)
		return matches
	}
	return nil
}
