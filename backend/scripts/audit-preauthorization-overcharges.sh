#!/usr/bin/env sh
set -eu

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "usage: DATABASE_URL=... $0 START_RFC3339 END_RFC3339 [OUTPUT_DIR]" >&2
  exit 2
fi
if [ -z "${DATABASE_URL:-}" ]; then
  echo "DATABASE_URL is required" >&2
  exit 2
fi
if ! command -v psql >/dev/null 2>&1; then
  echo "psql is required" >&2
  exit 2
fi

START_AT="$1"
END_AT="$2"
OUTPUT_DIR="${3:-./data/diagnostics/preauthorization-overcharge-audit-$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$OUTPUT_DIR"

run_report() {
  destination="$1"
  psql "$DATABASE_URL" \
    --no-psqlrc \
    --quiet \
    --csv \
    --set ON_ERROR_STOP=1 \
    --set start_at="$START_AT" \
    --set end_at="$END_AT" \
    --file - > "$destination"
}

# A candidate must match the exact v0.1.221 regression signature: balance
# billing in token mode, no billable unit and zero calculated total, but a
# positive actual charge. The time window is mandatory so unrelated historical
# pricing behavior cannot silently enter a compensation decision.
#
# This report is deliberately conservative. A positive charge is only marked
# as proven excess when the correlated provider response is a deterministic
# 4xx rejection that could not have produced billable model usage. Transport
# failures, 5xx responses, and requests without a correlated error remain
# unverified: they may have produced partial output whose usage was lost.
run_report "$OUTPUT_DIR/requests.csv" <<'SQL'
SET statement_timeout = '60s';
SET default_transaction_read_only = on;
WITH candidates AS (
  SELECT
    l.*,
    regexp_replace(COALESCE(l.request_id, ''), '^client:', '') AS normalized_client_request_id
  FROM usage_logs AS l
  WHERE l.created_at >= :'start_at'::timestamptz
    AND l.created_at < :'end_at'::timestamptz
    AND l.billing_type = 0
    AND l.billing_mode = 'token'
    AND l.actual_cost > 0
    AND l.total_cost = 0
    AND COALESCE(l.input_tokens, 0) = 0
    AND COALESCE(l.output_tokens, 0) = 0
    AND COALESCE(l.cache_creation_tokens, 0) = 0
    AND COALESCE(l.cache_read_tokens, 0) = 0
    AND COALESCE(l.cache_creation_5m_tokens, 0) = 0
    AND COALESCE(l.cache_creation_1h_tokens, 0) = 0
    AND COALESCE(l.image_input_tokens, 0) = 0
    AND COALESCE(l.image_output_tokens, 0) = 0
    AND COALESCE(l.image_count, 0) = 0
    AND COALESCE(l.video_count, 0) = 0
), correlated AS (
  SELECT
    c.*,
    matched.id AS ops_error_log_id,
    matched.status_code AS downstream_status_code,
    matched.upstream_status_code,
    matched.error_phase,
    matched.error_type,
    matched.error_owner,
    CASE
      WHEN matched.error_phase IN ('upstream', 'account_auth')
        AND matched.upstream_status_code IN (400, 401, 403, 404, 409, 413, 422, 429)
        THEN 'definite_nonbillable'
      ELSE 'actual_cost_unknown'
    END AS audit_classification
  FROM candidates AS c
  LEFT JOIN LATERAL (
    SELECT
      o.id,
      o.status_code,
      o.upstream_status_code,
      o.error_phase,
      o.error_type,
      o.error_owner,
      o.created_at
    FROM ops_error_logs AS o
    WHERE o.created_at >= c.created_at - INTERVAL '2 seconds'
      AND o.created_at <= c.created_at + INTERVAL '2 seconds'
      AND (
        (c.normalized_client_request_id <> '' AND o.client_request_id = c.normalized_client_request_id)
        OR (COALESCE(c.request_id, '') <> '' AND o.request_id = c.request_id)
      )
    ORDER BY
      CASE WHEN o.client_request_id = c.normalized_client_request_id THEN 0 ELSE 1 END,
      ABS(EXTRACT(EPOCH FROM (o.created_at - c.created_at))),
      o.id DESC
    LIMIT 1
  ) AS matched ON TRUE
)
SELECT
  c.id AS usage_log_id,
  c.created_at,
  c.user_id,
  u.email,
  c.api_key_id,
  c.account_id,
  c.request_id,
  c.model,
  c.inbound_endpoint,
  c.upstream_endpoint,
  c.ops_error_log_id,
  c.downstream_status_code,
  c.upstream_status_code,
  c.error_phase,
  c.error_type,
  c.error_owner,
  c.audit_classification,
  ROUND(c.actual_cost::numeric, 8) AS suspicious_charge_usd,
  CASE
    WHEN c.audit_classification = 'definite_nonbillable' THEN 0::numeric
    ELSE NULL
  END AS proven_actual_cost_usd,
  CASE
    WHEN c.audit_classification = 'definite_nonbillable' THEN ROUND(c.actual_cost::numeric, 8)
    ELSE NULL
  END AS proven_excess_refund_usd
FROM correlated AS c
JOIN users AS u ON u.id = c.user_id
ORDER BY c.created_at, c.id;
SQL

run_report "$OUTPUT_DIR/users.csv" <<'SQL'
SET statement_timeout = '60s';
SET default_transaction_read_only = on;
WITH candidates AS (
  SELECT
    l.*,
    regexp_replace(COALESCE(l.request_id, ''), '^client:', '') AS normalized_client_request_id
  FROM usage_logs AS l
  WHERE l.created_at >= :'start_at'::timestamptz
    AND l.created_at < :'end_at'::timestamptz
    AND l.billing_type = 0
    AND l.billing_mode = 'token'
    AND l.actual_cost > 0
    AND l.total_cost = 0
    AND COALESCE(l.input_tokens, 0) = 0
    AND COALESCE(l.output_tokens, 0) = 0
    AND COALESCE(l.cache_creation_tokens, 0) = 0
    AND COALESCE(l.cache_read_tokens, 0) = 0
    AND COALESCE(l.cache_creation_5m_tokens, 0) = 0
    AND COALESCE(l.cache_creation_1h_tokens, 0) = 0
    AND COALESCE(l.image_input_tokens, 0) = 0
    AND COALESCE(l.image_output_tokens, 0) = 0
    AND COALESCE(l.image_count, 0) = 0
    AND COALESCE(l.video_count, 0) = 0
), correlated AS (
  SELECT
    c.*,
    CASE
      WHEN matched.error_phase IN ('upstream', 'account_auth')
        AND matched.upstream_status_code IN (400, 401, 403, 404, 409, 413, 422, 429)
        THEN 'definite_nonbillable'
      ELSE 'actual_cost_unknown'
    END AS audit_classification
  FROM candidates AS c
  LEFT JOIN LATERAL (
    SELECT
      o.id,
      o.upstream_status_code,
      o.error_phase,
      o.created_at
    FROM ops_error_logs AS o
    WHERE o.created_at >= c.created_at - INTERVAL '2 seconds'
      AND o.created_at <= c.created_at + INTERVAL '2 seconds'
      AND (
        (c.normalized_client_request_id <> '' AND o.client_request_id = c.normalized_client_request_id)
        OR (COALESCE(c.request_id, '') <> '' AND o.request_id = c.request_id)
      )
    ORDER BY
      CASE WHEN o.client_request_id = c.normalized_client_request_id THEN 0 ELSE 1 END,
      ABS(EXTRACT(EPOCH FROM (o.created_at - c.created_at))),
      o.id DESC
    LIMIT 1
  ) AS matched ON TRUE
)
SELECT
  c.user_id,
  u.email,
  COUNT(*) AS suspicious_requests,
  COUNT(*) FILTER (WHERE c.audit_classification = 'definite_nonbillable') AS definite_nonbillable_requests,
  COUNT(*) FILTER (WHERE c.audit_classification = 'actual_cost_unknown') AS unverified_requests,
  MIN(c.created_at) AS first_candidate_at,
  MAX(c.created_at) AS last_candidate_at,
  ROUND(SUM(c.actual_cost)::numeric, 8) AS gross_suspicious_charge_usd,
  ROUND(COALESCE(SUM(c.actual_cost) FILTER (
    WHERE c.audit_classification = 'definite_nonbillable'
  ), 0)::numeric, 8) AS proven_excess_refund_usd,
  ROUND(COALESCE(SUM(c.actual_cost) FILTER (
    WHERE c.audit_classification = 'actual_cost_unknown'
  ), 0)::numeric, 8) AS unverified_charge_usd,
  ROUND(MAX(c.actual_cost)::numeric, 8) AS largest_single_charge_usd
FROM correlated AS c
JOIN users AS u ON u.id = c.user_id
GROUP BY c.user_id, u.email
ORDER BY proven_excess_refund_usd DESC, gross_suspicious_charge_usd DESC, c.user_id;
SQL

run_report "$OUTPUT_DIR/summary.csv" <<'SQL'
SET statement_timeout = '60s';
SET default_transaction_read_only = on;
WITH candidates AS (
  SELECT
    l.*,
    regexp_replace(COALESCE(l.request_id, ''), '^client:', '') AS normalized_client_request_id
  FROM usage_logs AS l
  WHERE l.created_at >= :'start_at'::timestamptz
    AND l.created_at < :'end_at'::timestamptz
    AND l.billing_type = 0
    AND l.billing_mode = 'token'
    AND l.actual_cost > 0
    AND l.total_cost = 0
    AND COALESCE(l.input_tokens, 0) = 0
    AND COALESCE(l.output_tokens, 0) = 0
    AND COALESCE(l.cache_creation_tokens, 0) = 0
    AND COALESCE(l.cache_read_tokens, 0) = 0
    AND COALESCE(l.cache_creation_5m_tokens, 0) = 0
    AND COALESCE(l.cache_creation_1h_tokens, 0) = 0
    AND COALESCE(l.image_input_tokens, 0) = 0
    AND COALESCE(l.image_output_tokens, 0) = 0
    AND COALESCE(l.image_count, 0) = 0
    AND COALESCE(l.video_count, 0) = 0
), correlated AS (
  SELECT
    c.*,
    CASE
      WHEN matched.error_phase IN ('upstream', 'account_auth')
        AND matched.upstream_status_code IN (400, 401, 403, 404, 409, 413, 422, 429)
        THEN 'definite_nonbillable'
      ELSE 'actual_cost_unknown'
    END AS audit_classification
  FROM candidates AS c
  LEFT JOIN LATERAL (
    SELECT
      o.id,
      o.upstream_status_code,
      o.error_phase,
      o.created_at
    FROM ops_error_logs AS o
    WHERE o.created_at >= c.created_at - INTERVAL '2 seconds'
      AND o.created_at <= c.created_at + INTERVAL '2 seconds'
      AND (
        (c.normalized_client_request_id <> '' AND o.client_request_id = c.normalized_client_request_id)
        OR (COALESCE(c.request_id, '') <> '' AND o.request_id = c.request_id)
      )
    ORDER BY
      CASE WHEN o.client_request_id = c.normalized_client_request_id THEN 0 ELSE 1 END,
      ABS(EXTRACT(EPOCH FROM (o.created_at - c.created_at))),
      o.id DESC
    LIMIT 1
  ) AS matched ON TRUE
)
SELECT
  :'start_at' AS window_start,
  :'end_at' AS window_end,
  COUNT(*) AS suspicious_requests,
  COUNT(DISTINCT user_id) AS affected_users,
  COUNT(*) FILTER (WHERE audit_classification = 'definite_nonbillable') AS definite_nonbillable_requests,
  COUNT(*) FILTER (WHERE audit_classification = 'actual_cost_unknown') AS unverified_requests,
  MIN(created_at) AS first_candidate_at,
  MAX(created_at) AS last_candidate_at,
  ROUND(COALESCE(SUM(actual_cost), 0)::numeric, 8) AS gross_suspicious_charge_usd,
  ROUND(COALESCE(SUM(actual_cost) FILTER (
    WHERE audit_classification = 'definite_nonbillable'
  ), 0)::numeric, 8) AS proven_excess_refund_usd,
  ROUND(COALESCE(SUM(actual_cost) FILTER (
    WHERE audit_classification = 'actual_cost_unknown'
  ), 0)::numeric, 8) AS unverified_charge_usd
FROM correlated;
SQL

(
  cd "$OUTPUT_DIR"
  sha256sum requests.csv users.csv summary.csv > SHA256SUMS
)

echo "$OUTPUT_DIR"
