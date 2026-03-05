package bridge

import "github.com/justinpbarnett/virgil/internal/envelope"

// Usage is an alias for envelope.Usage, the canonical token usage type.
type Usage = envelope.Usage

// ModelPricing holds per-million-token prices for a model.
type ModelPricing struct {
	InputPerMToken  float64
	OutputPerMToken float64
}

// CostTable maps model IDs to their pricing. Models not in the table yield zero cost.
// Update this table when provider pricing changes.
var CostTable = map[string]ModelPricing{
	// Anthropic
	"claude-sonnet-4-20250514":  {InputPerMToken: 3.00, OutputPerMToken: 15.00},
	"claude-haiku-4-5-20251001": {InputPerMToken: 0.80, OutputPerMToken: 4.00},
	"claude-opus-4-6":           {InputPerMToken: 15.00, OutputPerMToken: 75.00},
	// OpenAI
	"gpt-4o":       {InputPerMToken: 2.50, OutputPerMToken: 10.00},
	"gpt-4o-mini":  {InputPerMToken: 0.15, OutputPerMToken: 0.60},
	"gpt-4.1":      {InputPerMToken: 2.00, OutputPerMToken: 8.00},
	"gpt-4.1-mini": {InputPerMToken: 0.40, OutputPerMToken: 1.60},
	"gpt-4.1-nano": {InputPerMToken: 0.10, OutputPerMToken: 0.40},
	// Google Gemini
	"gemini-2.0-flash": {InputPerMToken: 0.10, OutputPerMToken: 0.40},
	"gemini-2.5-pro":   {InputPerMToken: 1.25, OutputPerMToken: 10.00},
	// xAI Grok
	"grok-3":      {InputPerMToken: 3.00, OutputPerMToken: 15.00},
	"grok-3-mini": {InputPerMToken: 0.30, OutputPerMToken: 0.50},
}

// CostFor returns the USD cost for the given model and token counts.
// Returns 0.0 if the model is not in the cost table.
func CostFor(model string, inputTokens, outputTokens int) float64 {
	p, ok := CostTable[model]
	if !ok {
		return 0.0
	}
	return (float64(inputTokens)/1_000_000)*p.InputPerMToken +
		(float64(outputTokens)/1_000_000)*p.OutputPerMToken
}
