package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

// compileFormats compiles raw format template strings (pipe name → content type → template string)
// into executable templates. Returns an error if any template is invalid.
func compileFormats(raw map[string]map[string]string) (map[string]map[string]*template.Template, error) {
	result := make(map[string]map[string]*template.Template, len(raw))
	for pipe, byType := range raw {
		compiled := make(map[string]*template.Template, len(byType))
		for ct, tmplStr := range byType {
			name := fmt.Sprintf("%s/%s", pipe, ct)
			t, err := template.New(name).Option("missingkey=zero").Parse(tmplStr)
			if err != nil {
				return nil, fmt.Errorf("pipe %s, content_type %s: %w", pipe, ct, err)
			}
			compiled[ct] = t
		}
		result[pipe] = compiled
	}
	return result, nil
}

// formatTerminal applies a format template to the envelope if it is a terminal envelope
// (last in a pipeline) and its content_type has a matching template. If the content_type
// is already "text" or no matching template exists, the envelope is returned unchanged.
func formatTerminal(env envelope.Envelope, pipe string, formats map[string]map[string]*template.Template) envelope.Envelope {
	if env.ContentType == envelope.ContentText {
		return env
	}
	if formats == nil {
		return env
	}
	byType, ok := formats[pipe]
	if !ok {
		return env
	}
	tmpl, ok := byType[env.ContentType]
	if !ok {
		return env
	}

	data := prepareTemplateData(env)

	// For calendar pipe, provide range context for template messaging
	if pipe == "calendar" && env.Args != nil {
		if range_ := env.Args["range"]; range_ != "" {
			data["Range"] = range_
		} else if modifier := env.Args["modifier"]; modifier != "" {
			data["Range"] = modifier
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return env
	}

	env.Content = buf.String()
	env.ContentType = envelope.ContentText
	return env
}

// prepareTemplateData converts envelope content into a map suitable for template execution.
// For list content: provides .Items (the slice), .Count (length), and .Signal.
// For structured content: provides map fields directly plus .Signal.
func prepareTemplateData(env envelope.Envelope) map[string]any {
	data := make(map[string]any, 4)
	if env.Args != nil {
		data["Signal"] = env.Args["signal"]
	}

	switch env.ContentType {
	case envelope.ContentList:
		items := normalizeToSlice(env.Content)
		data["Items"] = items
		data["Count"] = len(items)

	case envelope.ContentStructured:
		m := normalizeToMap(env.Content)
		for k, v := range m {
			data[k] = v
		}
	}

	return data
}

// normalizeToSlice converts env.Content into a []map[string]any.
// Handles both []any (from JSON unmarshaling) and Go structs/slices (from in-process handlers).
func normalizeToSlice(content any) []map[string]any {
	if content == nil {
		return nil
	}

	// Direct type assertion for []map[string]any (in-process handlers)
	if maps, ok := content.([]map[string]any); ok {
		return maps
	}

	// Try direct type assertion for []any (subprocess JSON path)
	if slice, ok := content.([]any); ok {
		result := make([]map[string]any, 0, len(slice))
		for _, item := range slice {
			if m, ok := item.(map[string]any); ok {
				result = append(result, m)
			} else {
				result = append(result, structToMap(item))
			}
		}
		return result
	}

	// For Go structs/slices, marshal then unmarshal to normalize
	b, err := json.Marshal(content)
	if err != nil {
		return nil
	}
	var result []map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		return nil
	}
	return result
}

// normalizeToMap converts env.Content into a map[string]any.
func normalizeToMap(content any) map[string]any {
	if m, ok := content.(map[string]any); ok {
		return m
	}
	return structToMap(content)
}

// structToMap converts a Go struct to map[string]any via JSON round-trip.
func structToMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}
