package trust

import "context"

type approvalKey struct{}

// AutoApproval records an outbound action that was auto-approved during a skill run.
// Fields are unexported; construct only via RecordAutoApproval.
type AutoApproval struct {
	actionType string
	channel    string
	contact    string
}

func (a AutoApproval) ActionType() string { return a.actionType }
func (a AutoApproval) Channel() string    { return a.channel }
func (a AutoApproval) Contact() string    { return a.contact }

// WithAutoApprovals attaches an auto-approval tracker to the context.
func WithAutoApprovals(ctx context.Context) context.Context {
	approvals := make([]AutoApproval, 0)
	return context.WithValue(ctx, approvalKey{}, &approvals)
}

// RecordAutoApproval appends an approval record to the tracker in ctx.
// No-ops if the context has no tracker.
func RecordAutoApproval(ctx context.Context, actionType, channel, contact string) {
	if p, ok := ctx.Value(approvalKey{}).(*[]AutoApproval); ok && p != nil {
		*p = append(*p, AutoApproval{actionType: actionType, channel: channel, contact: contact})
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
