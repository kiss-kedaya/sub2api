package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	// ipWhitelistAssistWindow is how far back "recent" client IPs are read.
	ipWhitelistAssistWindow = 30 * 24 * time.Hour
	// ipWhitelistAssistScanLimit caps the rows aggregated per request for the
	// audit_logs fallback. The (actor_user_id, created_at DESC) index makes the
	// inner ORDER BY + LIMIT an index scan instead of a full-table scan.
	ipWhitelistAssistScanLimit = 20000
)

// Per-source budgets. Each source gets its own deadline that is independent of
// the caller's deadline: one slow query must never burn the other source's
// budget. Before this split a 4.5s request_connection_traces scan consumed the
// whole shared budget, the follow-up audit_logs query started with an already
// cancelled context, and the "常用 IP" card rendered empty even though the user
// had thousands of recorded addresses.
var (
	ipWhitelistAssistTraceBudget = 900 * time.Millisecond
	ipWhitelistAssistAuditBudget = 900 * time.Millisecond
)

type ipWhitelistAssistRepository struct {
	db *sql.DB
}

// NewIPWhitelistAssistRepository creates the bounded recent-client-IP reader
// used by the IP whitelist assist endpoint.
func NewIPWhitelistAssistRepository(db *sql.DB) service.IPWhitelistAssistRepository {
	return &ipWhitelistAssistRepository{db: db}
}

// ListRecentClientIPs returns the user's most frequent client addresses within
// the recent window, most frequent first.
//
// request_connection_traces is preferred because it records every connection;
// audit_logs is the fallback when traces are unavailable or empty (for example
// when trace retention has already pruned a quiet account). Both paths bound
// the scanned rows, and callers add a short context timeout on top.
func (r *ipWhitelistAssistRepository) ListRecentClientIPs(ctx context.Context, userID int64, limit int) ([]service.RecentClientIP, error) {
	if r == nil || r.db == nil || userID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	since := time.Now().Add(-ipWhitelistAssistWindow)

	fromTraces, tracesErr := r.listFromTraces(ctx, userID, since, limit)
	if tracesErr == nil && len(fromTraces) > 0 {
		return fromTraces, nil
	}
	fromAudit, auditErr := r.listFromAuditLogs(ctx, userID, since, limit)
	if auditErr != nil {
		if tracesErr != nil {
			return nil, fmt.Errorf("list recent client ips: traces: %v; audit logs: %w", tracesErr, auditErr)
		}
		return nil, fmt.Errorf("list recent client ips: %w", auditErr)
	}
	return fromAudit, nil
}

func (r *ipWhitelistAssistRepository) listFromTraces(ctx context.Context, userID int64, since time.Time, limit int) ([]service.RecentClientIP, error) {
	// WithoutCancel keeps the per-source deadline authoritative: the caller's
	// budget may already be exhausted by an earlier source, and the fallback
	// must still run. Both queries are bounded reads on an indexed path.
	traceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ipWhitelistAssistTraceBudget)
	defer cancel()
	rows, err := r.db.QueryContext(traceCtx, `
		SELECT client_ip, COUNT(*) AS request_count
		FROM request_connection_traces
		WHERE user_id = $1
		  AND started_at >= $2
		  AND COALESCE(client_ip, '') <> ''
		GROUP BY client_ip
		ORDER BY request_count DESC, client_ip ASC
		LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRecentClientIPs(rows)
}

func (r *ipWhitelistAssistRepository) listFromAuditLogs(ctx context.Context, userID int64, since time.Time, limit int) ([]service.RecentClientIP, error) {
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ipWhitelistAssistAuditBudget)
	defer cancel()
	rows, err := r.db.QueryContext(auditCtx, `
		SELECT client_ip, COUNT(*) AS request_count
		FROM (
			SELECT client_ip
			FROM audit_logs
			WHERE actor_user_id = $1
			  AND created_at >= $2
			  AND COALESCE(client_ip, '') <> ''
			ORDER BY created_at DESC
			LIMIT $3
		) recent
		GROUP BY client_ip
		ORDER BY request_count DESC, client_ip ASC
		LIMIT $4`, userID, since, ipWhitelistAssistScanLimit, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRecentClientIPs(rows)
}

func scanRecentClientIPs(rows *sql.Rows) ([]service.RecentClientIP, error) {
	out := make([]service.RecentClientIP, 0, 5)
	for rows.Next() {
		var ip string
		var count int
		if err := rows.Scan(&ip, &count); err != nil {
			return nil, err
		}
		ip = strings.TrimSpace(ip)
		// The UI offers these values for one-click insertion into the IP
		// whitelist, so only real addresses may pass: historical rows written by
		// older/misconfigured proxies can contain host:port strings, hostnames or
		// empty values, and inserting one of those would only make the save fail.
		if ip == "" || net.ParseIP(ip) == nil {
			continue
		}
		out = append(out, service.RecentClientIP{IP: ip, Count: count})
	}
	return out, rows.Err()
}
