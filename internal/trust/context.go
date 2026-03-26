package trust

import "context"

type approvalKey struct{}

// AutoApproval records an outbound action that was auto-approved.
type AutoApproval struct {
	ActionType string
	Channel    string
	Contact    string
}

// WithAutoApprovals attaches an auto-approval tracker to the context.
func WithAutoApprovals(ctx context.Context) context.Context {
	approvals := make([]AutoApproval, 0)
	return context.WithValue(ctx, approvalKey{}, &approvals)
}

// RecordAutoApproval appends an approval record to the tracker in ctx.
// No-ops if the context has no tracker.
func RecordAutoApproval(ctx context.Context, actionType, channel, contact string) {
	if p, ok := ctx.Value(approvalKey{}).(*[]AutoApproval); ok && p != nil {
		*p = append(*p, AutoApproval{ActionType: actionType, Channel: channel, Contact: contact})
	}
}

// DrainAutoApprovals returns and clears all recorded auto-approvals.
// Returns nil if no tracker is present.
func DrainAutoApprovals(ctx context.Context) []AutoApproval {
	p, ok := ctx.Value(approvalKey{}).(*[]AutoApproval)
	if !ok || p == nil {
		return nil
	}
	result := *p
	*p = (*p)[:0]
	return result
}
