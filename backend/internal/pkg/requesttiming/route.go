package requesttiming

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
)

// RouteObservation measures a gateway boundary, not model latency. In
// particular, websocket_write ends at Write, not at the response terminal.
type RouteObservation struct {
	Route     transportdiag.Route `json:"route"`
	AccountID int64               `json:"account_id"`
	Phase     string              `json:"phase"`
	StartMS   float64             `json:"start_ms"`
	EndMS     *float64            `json:"end_ms,omitempty"`
	Status    int                 `json:"status,omitempty"`
	Error     string              `json:"error,omitempty"`
}

func StartRoute(ctx context.Context, accountID int64, phase string, route transportdiag.Route) func(int, error) {
	c := From(ctx)
	if c == nil {
		return func(int, error) {}
	}
	c.mu.Lock()
	if c.finished || len(c.data.Routes) >= 32 {
		c.data.Truncated = true
		c.mu.Unlock()
		return func(int, error) {}
	}
	i := len(c.data.Routes)
	c.data.Routes = append(c.data.Routes, RouteObservation{Route: route, AccountID: accountID, Phase: phase, StartMS: c.offset(time.Now())})
	c.mu.Unlock()
	return func(status int, err error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finished || c.data.Routes[i].EndMS != nil {
			return
		}
		v := c.offset(time.Now())
		a := &c.data.Routes[i]
		a.EndMS, a.Status = &v, status
		if err != nil {
			a.Error = errorKind(err)
		}
	}
}

// SetRoute annotates the confirmed dispatch boundary, e.g. a plugin owns the
// request and its internal network transport cannot be observed by the host.
func SetRoute(ctx context.Context, route transportdiag.Route) {
	if ctx == nil {
		return
	}
	if t, ok := ctx.Value(attemptKey{}).(*Trace); ok {
		t.update(func(a *Attempt) { a.Route = route })
	}
}
