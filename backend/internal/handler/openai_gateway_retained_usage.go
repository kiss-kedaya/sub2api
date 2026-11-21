package handler

import "github.com/Wei-Shaw/sub2api/internal/service"

// retainedOpenAIUsage keeps the billing closure of a replayable OpenAI-family
// attempt until the failover loop knows the final outcome:
//
//   - a retry that succeeds bills its own result, and the retained attempt is
//     dropped, so one logical request never produces two bills;
//   - a loop that gives up without a billable result of its own (no more accounts,
//     account slot exhausted, client gone, aborted) settles the retained attempt, so
//     the upstream cost observed before the stream broke is not lost (issue #456).
//
// The closure, not the plain result, is retained because it captures the account,
// api key and pricing snapshot of the attempt that produced the usage. The latest
// attempt wins: earlier attempts of the same logical request are superseded, never
// added, so a failover chain cannot stack bills. settle is idempotent, so every exit
// path can call it safely.
type retainedOpenAIUsage struct {
	pending func()
	settled bool
}

// retain remembers the billing closure of a replayable attempt the loop is about to
// retry. Nil closures and calls after settlement are ignored.
func (r *retainedOpenAIUsage) retain(bill func()) {
	if r == nil || r.settled || bill == nil {
		return
	}
	r.pending = bill
}

// settle bills the terminal attempt when bill is non-nil, otherwise the retained
// attempt. Either way the retained closure is cleared and later calls are no-ops, so
// a request is billed at most once.
func (r *retainedOpenAIUsage) settle(bill func()) {
	if r == nil {
		if bill != nil {
			bill()
		}
		return
	}
	if r.settled {
		return
	}
	r.settled = true
	if bill != nil {
		r.pending = nil
		bill()
		return
	}
	if r.pending != nil {
		pending := r.pending
		r.pending = nil
		pending()
	}
}

// discard forgets the retained attempt when the request is billed through another
// channel (for example a moderation refusal with a fixed price), so the same
// logical request is never charged twice.
func (r *retainedOpenAIUsage) discard() {
	if r == nil || r.settled {
		return
	}
	r.settled = true
	r.pending = nil
}

// openAISettleUsage turns a (possibly nil) forward result plus the attempt's submit
// closure into the closure retainedOpenAIUsage expects, or nil when there is nothing
// to bill.
func openAISettleUsage(result *service.OpenAIForwardResult, submit func(*service.OpenAIForwardResult)) func() {
	if result == nil {
		return nil
	}
	return func() { submit(result) }
}
