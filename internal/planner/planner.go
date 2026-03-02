package planner

import (
	"strings"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

type Planner struct {
	templates []config.TemplateEntry
	sources   map[string]string // source name → pipe name
}

func New(templates config.TemplatesConfig, sources map[string]string) *Planner {
	return &Planner{
		templates: templates.Templates,
		sources:   sources,
	}
}

func (p *Planner) Plan(route router.RouteResult, parsed parser.ParsedSignal) runtime.Plan {
	// Try template matching (component-presence-based)
	for _, tmpl := range p.templates {
		if p.matchesRequirements(tmpl.Requires, parsed) {
			return p.resolveTemplate(tmpl, parsed)
		}
	}

	// No template match — single step plan for the routed pipe
	flags := make(map[string]string)
	if parsed.Action != "" {
		flags["action"] = parsed.Action
	}
	if parsed.Topic != "" {
		flags["topic"] = parsed.Topic
	}

	return runtime.Plan{
		Steps: []runtime.Step{
			{Pipe: route.Pipe, Flags: flags},
		},
	}
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
		flags := make(map[string]string)

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
	if p.sources != nil {
		if mapped, ok := p.sources[parsed.Source]; ok {
			sourcePipe = mapped
		}
	}
	s = strings.ReplaceAll(s, "{source}", sourcePipe)

	return s
}
