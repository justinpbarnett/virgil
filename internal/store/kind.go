package store

const (
	KindExplicit     = "explicit"
	KindWorkingState = "working_state"
	KindInvocation   = "invocation"
	KindTodo         = "todo"
	KindReminder     = "reminder"
	KindGoal         = "goal"
	KindSummary      = "summary"
)

func DefaultConfidence(kind string) float64 {
	switch kind {
	case KindExplicit:
		return ConfidenceExplicit
	case KindWorkingState:
		return ConfidenceWorkingState
	case KindInvocation:
		return ConfidenceInvocation
	case KindTodo:
		return ConfidenceTodo
	case KindReminder:
		return ConfidenceReminder
	case KindGoal:
		return ConfidenceGoal
	case KindSummary:
		return ConfidenceSummary
	default:
		return 0.5
	}
}
