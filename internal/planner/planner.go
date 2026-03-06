package planner

import (
	"log/slog"
	"strings"
	"text/template"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

type Planner struct {
	templates []config.TemplateEntry
	sources   map[string][]string // source name → []pipe names
	logger    *slog.Logger
}

func New(templates config.TemplatesConfig, sources map[string][]string, logger *slog.Logger) *Planner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Planner{
		templates: templates.Templates,
		sources:   sources,
		logger:    logger,
	}
}

func (p *Planner) Plan(route router.RouteResult, parsed parser.ParsedSignal) runtime.Plan {
	verb, action := reconcileAmbiguousVerb(parsed, route.Pipe)

	// Try template matching (component-presence-based, scoped to routed pipe)
	for _, tmpl := range p.templates {
		if tmpl.Pipe != "" && tmpl.Pipe != route.Pipe {
			continue
		}
		if matchesRequirements(tmpl.Requires, parsed, verb) {
			plan := p.resolveTemplate(tmpl, parsed, verb, action)
			p.logger.Info("planned", "steps", len(plan.Steps))
			p.logger.Debug("template matched", "requires", tmpl.Requires, "steps", len(plan.Steps))
			return plan
		}
	}

	// No template match — single step plan for the routed pipe
	return p.singleStepPlan(route.Pipe, parsed, action)
}

func reconcileAmbiguousVerb(parsed parser.ParsedSignal, routedPipe string) (string, string) {
	if parsed.Verb != "" {
		return parsed.Verb, parsed.Action
	}

	// Check if the routed pipe matches any of the ambiguous verbs
	for _, v := range parsed.Verbs {
		if v == routedPipe {
			return v, parsed.Actions[v]
		}
	}
	return "", ""
}

func matchesRequirements(requires []string, parsed parser.ParsedSignal, verb string) bool {
	for _, req := range requires {
		switch req {
		case "verb":
			if verb == "" {
				return false
			}
		case "type":
			if parsed.Type == "" {
				return false
			}
		case "source":
			if parsed.Source == "" {
				return false
			}
		case "modifier":
			if parsed.Modifier == "" {
				return false
			}
		case "topic":
			if parsed.Topic == "" {
				return false
			}
		}
	}
	return true
}

func (p *Planner) singleStepPlan(pipe string, parsed parser.ParsedSignal, action string) runtime.Plan {
	flags := make(map[string]string, 3)
	if action != "" {
		flags["action"] = action
	}
	if parsed.Topic != "" {
		flags["topic"] = parsed.Topic
	}
	if parsed.Modifier != "" {
		flags["modifier"] = parsed.Modifier
	}

	plan := runtime.Plan{
		Steps: []runtime.Step{{Pipe: pipe, Flags: flags}},
	}
	p.logger.Info("planned", "steps", len(plan.Steps))
	p.logger.Debug("no template, single step", "pipe", pipe)
	return plan
}

func (p *Planner) resolveTemplate(tmpl config.TemplateEntry, parsed parser.ParsedSignal, verb string, action string) runtime.Plan {
	var steps []runtime.Step

	for _, planStep := range tmpl.Plan {
		pipeName := p.resolveVar(planStep.Pipe, parsed)
		flags := make(map[string]string, len(planStep.Flags)+2)

		for k, v := range planStep.Flags {
			flags[k] = p.resolveVar(v, parsed)
		}

		// Add action and topic from parser if not already in flags
		if action != "" {
			flags["action"] = action
		}
		if parsed.Topic != "" {
			flags["topic"] = parsed.Topic
		}

		steps = append(steps, runtime.Step{Pipe: pipeName, Flags: flags})
	}

	return runtime.Plan{Steps: collapseConsecutiveSteps(steps)}
}

func collapseConsecutiveSteps(steps []runtime.Step) []runtime.Step {
	if len(steps) <= 1 {
		return steps
	}

	collapsed := []runtime.Step{steps[0]}
	for i := 1; i < len(steps); i++ {
		last := &collapsed[len(collapsed)-1]
		if steps[i].Pipe != last.Pipe {
			collapsed = append(collapsed, steps[i])
		} else {
			// Merge flags from later step (earlier wins on conflicts)
			for k, v := range steps[i].Flags {
				if _, exists := last.Flags[k]; !exists {
					last.Flags[k] = v
				}
			}
		}
	}
	return collapsed
}

// varSyntax converts pipe.yaml variable syntax ({verb}) to Go template syntax ({{.Verb}}).
var varSyntax = strings.NewReplacer(
	"{verb}", "{{.Verb}}",
	"{type}", "{{.Type}}",
	"{topic}", "{{.Topic}}",
	"{modifier}", "{{.Modifier}}",
	"{source}", "{{.Source}}",
)

type varData struct {
	Verb     string
	Type     string
	Topic    string
	Modifier string
	Source   string
}

func (p *Planner) resolveVar(s string, parsed parser.ParsedSignal) string {
	if !strings.Contains(s, "{") {
		return s
	}

	sourcePipe := parsed.Source
	if mapped, ok := p.sources[parsed.Source]; ok && len(mapped) > 0 {
		sourcePipe = mapped[0]
	}

	tmpl, err := template.New("").Option("missingkey=zero").Parse(varSyntax.Replace(s))
	if err != nil {
		return s
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, varData{
		Verb:     parsed.Verb,
		Type:     parsed.Type,
		Topic:    parsed.Topic,
		Modifier: parsed.Modifier,
		Source:   sourcePipe,
	}); err != nil {
		return s
	}
	return buf.String()
}
