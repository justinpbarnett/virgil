package planner

import (
	"log/slog"
	"strings"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

type Planner struct {
	templates []config.TemplateEntry
	sources   map[string]string // source name → pipe name
	logger    *slog.Logger
}

func New(templates config.TemplatesConfig, sources map[string]string, logger *slog.Logger) *Planner {
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
	// Try template matching (component-presence-based)
	for _, tmpl := range p.templates {
		if p.matchesRequirements(tmpl.Requires, parsed) {
			plan := p.resolveTemplate(tmpl, parsed)
			p.logger.Info("planned", "steps", len(plan.Steps))
			p.logger.Debug("template matched", "requires", tmpl.Requires, "steps", len(plan.Steps))
			return plan
		}
	}

	// No template match — single step plan for the routed pipe
	// Capacity hint: up to 3 entries (action, topic, modifier).
	flags := make(map[string]string, 3)
	if parsed.Action != "" {
		flags["action"] = parsed.Action
	}
	if parsed.Topic != "" {
		flags["topic"] = parsed.Topic
	}
	if parsed.Modifier != "" {
		flags["modifier"] = parsed.Modifier
	}

	plan := runtime.Plan{
		Steps: []runtime.Step{
			{Pipe: route.Pipe, Flags: flags},
		},
	}
	p.logger.Info("planned", "steps", len(plan.Steps))
	p.logger.Debug("no template, single step", "pipe", route.Pipe)
	return plan
}

func (p *Planner) matchesRequirements(requires []string, parsed parser.ParsedSignal) bool {
	for _, req := range requires {
		switch req {
		case "verb":
			if parsed.Verb == "" {
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

func (p *Planner) resolveTemplate(tmpl config.TemplateEntry, parsed parser.ParsedSignal) runtime.Plan {
	var steps []runtime.Step

	for _, planStep := range tmpl.Plan {
		pipeName := p.resolveVar(planStep.Pipe, parsed)
		flags := make(map[string]string, len(planStep.Flags)+2)

		for k, v := range planStep.Flags {
			flags[k] = p.resolveVar(v, parsed)
		}

		// Add action from parser if available and not already in flags
		if _, hasAction := flags["action"]; !hasAction && parsed.Action != "" {
			flags["action"] = parsed.Action
		}

		// Add topic from parser if available and not already in flags
		if _, hasTopic := flags["topic"]; !hasTopic && parsed.Topic != "" {
			flags["topic"] = parsed.Topic
		}

		steps = append(steps, runtime.Step{Pipe: pipeName, Flags: flags})
	}

	// Collapse consecutive steps targeting the same pipe.
	// This prevents redundant execution when verb and source resolve
	// to the same pipe (e.g., "check my calendar" → calendar→calendar
	// collapses to a single calendar step). Flags from the later step
	// are merged in (earlier step wins on conflicts).
	if len(steps) > 1 {
		collapsed := []runtime.Step{steps[0]}
		for i := 1; i < len(steps); i++ {
			last := &collapsed[len(collapsed)-1]
			if steps[i].Pipe != last.Pipe {
				collapsed = append(collapsed, steps[i])
			} else {
				for k, v := range steps[i].Flags {
					if _, exists := last.Flags[k]; !exists {
						last.Flags[k] = v
					}
				}
			}
		}
		steps = collapsed
	}

	return runtime.Plan{Steps: steps}
}

func (p *Planner) resolveVar(s string, parsed parser.ParsedSignal) string {
	if !strings.Contains(s, "{") {
		return s
	}

	s = strings.ReplaceAll(s, "{verb}", parsed.Verb)
	s = strings.ReplaceAll(s, "{type}", parsed.Type)
	s = strings.ReplaceAll(s, "{topic}", parsed.Topic)
	s = strings.ReplaceAll(s, "{modifier}", parsed.Modifier)

	// Resolve source: map source name to pipe name
	sourcePipe := parsed.Source
	if mapped, ok := p.sources[parsed.Source]; ok {
		sourcePipe = mapped
	}
	s = strings.ReplaceAll(s, "{source}", sourcePipe)

	return s
}
