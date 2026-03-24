package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal"
)

func (a *Agent) systemPrompt(signal internal.Signal) string {
	var b strings.Builder

	b.WriteString("You are Virgil, Justin's personal guide. You coordinate his work life: triage email, manage calendar, track tasks, prepare context, and handle follow-ups.\n\n")
	b.WriteString("You have access to tools for memory, tasks, and more. Use them to accomplish what's asked.\n\n")

	if len(a.Skills) > 0 {
		b.WriteString("You also have skills you can invoke with the run_skill tool. Available skills:\n")
		for _, s := range a.Skills {
			if s.Enabled {
				b.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("Be proactive. If you notice something relevant while doing a task, mention it.\n\n")
	b.WriteString(fmt.Sprintf("Current time: %s\n", time.Now().Format(time.RFC3339)))

	if signal.Bridge != "" {
		b.WriteString(fmt.Sprintf("Current bridge context: %s\n", signal.Bridge))
	} else {
		b.WriteString("Current bridge context: personal\n")
	}

	return b.String()
}
