package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	// ipWhitelistAssistWindow is how far back "recent" client IPs are read.
	ipWhitelistAssistWindow = 30 * 24 * time.Hour
	// ipWhitelistAssistScanLimit caps the rows aggregated per request. The
	// (user_id, started_at DESC) / (actor_user_id, created_at DESC) indexes make
	// the inner ORDER BY + LIMIT an index scan instead of a full-table scan.
	ipWhitelistAssistScanLimit = 20000
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
	rows, err := r.db.QueryContext(ctx, `
		SELECT client_ip, COUNT(*) AS request_count
		FROM (
			SELECT client_ip
			FROM request_connection_traces
			WHERE user_id = $1
			  AND started_at >= $2
			  AND COALESCE(client_ip, '') <> ''
			ORDER BY started_at DESC
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

func (r *ipWhitelistAssistRepository) listFromAuditLogs(ctx context.Context, userID int64, since time.Time, limit int) ([]service.RecentClientIP, error) {
	rows, err := r.db.QueryContext(ctx, `
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
		if ip == "" {
			continue
		}
		out = append(out, service.RecentClientIP{IP: ip, Count: count})
	}
	return out, rows.Err()
}
