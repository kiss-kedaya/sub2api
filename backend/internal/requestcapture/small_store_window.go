package requestcapture

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const smallRetentionWindow = 24 * time.Hour

var errSmallRetentionExpired = errors.New("small request capture expired")

func smallCutoff(now time.Time) time.Time { return now.UTC().Add(-smallRetentionWindow) }

// Complete groups use the existing totals. Only groups that cross the exact
// cutoff need to sum occurrences. Old writers left no per-request timestamps;
// at the boundary expose only occurrences we can independently date.
const smallActiveRecords = `WITH live_records AS (
 SELECT id,bucket,user_id,api_key_id,model,status,first_seen,last_seen,repeat_count,
        input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens,actual_cost,raw_bytes,
        omitted_responses,request_ids,request_headers_hash,request_body_hash,data,false AS window_trimmed
 FROM small_request_records
 WHERE bucket >= $1 AND first_seen > $2
 UNION ALL
 SELECT r.id,r.bucket,r.user_id,r.api_key_id,r.model,
        (SELECT q.status FROM small_request_occurrences q WHERE q.record_id=r.id AND q.seen_at>$2 ORDER BY q.seen_at DESC,q.ordinal DESC LIMIT 1),
        o.first_seen,o.last_seen,o.repeats,o.input_tokens,o.output_tokens,o.cache_read_tokens,o.cache_creation_tokens,o.actual_cost,o.raw_bytes,
        o.repeats-o.with_sample,
        (SELECT COALESCE(jsonb_agg(q.request_id ORDER BY q.seen_at,q.ordinal),'[]'::jsonb) FROM
           (SELECT q.request_id,q.seen_at,q.ordinal FROM small_request_occurrences q WHERE q.record_id=r.id AND q.seen_at>$2 ORDER BY q.seen_at DESC,q.ordinal DESC LIMIT 20) q),
        r.request_headers_hash,r.request_body_hash,r.data - 'first_usage' - 'request_volatile_headers' - 'group_id',true
 FROM small_request_records r
 CROSS JOIN LATERAL (
   SELECT min(o.seen_at) first_seen,max(o.seen_at) last_seen,count(*) repeats,
          sum(o.input_tokens)::bigint input_tokens,sum(o.output_tokens)::bigint output_tokens,
          sum(o.cache_read_tokens)::bigint cache_read_tokens,sum(o.cache_creation_tokens)::bigint cache_creation_tokens,
          sum(o.actual_cost) actual_cost,sum(o.raw_bytes)::bigint raw_bytes,count(s.record_id) with_sample
   FROM small_request_occurrences o
   LEFT JOIN small_request_responses s ON s.record_id=o.record_id AND s.fingerprint=o.response_fingerprint
   WHERE o.record_id=r.id AND o.seen_at>$2
 ) o
 WHERE r.bucket=$1 AND r.first_seen <= $2 AND r.last_seen > $2 AND o.repeats>0
) `

func insertSmallOccurrence(ctx context.Context, tx *sql.Tx, id, ordinal int64, p preparedSmallExchange) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO small_request_occurrences
 (record_id,ordinal,seen_at,request_id,response_fingerprint,status,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens,actual_cost,raw_bytes)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		id, ordinal, p.StartedAt, p.Usage.RequestID, p.ResponseFingerprint, p.Status,
		p.Usage.InputTokens, p.Usage.OutputTokens, p.Usage.CacheReadTokens, p.Usage.CacheCreationTokens,
		smallCostOrZero(p.Usage.ActualCost), p.RawBytes)
	return err
}

func smallResponseRows(ctx context.Context, tx *sql.Tx, r SmallRecord, cutoff time.Time) (*sql.Rows, error) {
	if !r.windowTrimmed {
		return tx.QueryContext(ctx, `SELECT fingerprint,first_seen,last_seen,repeat_count,data,headers_hash,body_hash
 FROM small_request_responses WHERE record_id=$1 ORDER BY first_seen,fingerprint`, r.ID)
	}
	// A response body may match a younger occurrence even after the original
	// sample's per-request headers and usage have expired.
	return tx.QueryContext(ctx, `SELECT s.fingerprint,o.first_seen,o.last_seen,o.repeats,
 CASE WHEN s.first_seen>$2 THEN s.data ELSE s.data - 'usage' - 'volatile_headers' END,
 s.headers_hash,s.body_hash
 FROM small_request_responses s JOIN (
   SELECT response_fingerprint,count(*) repeats,min(seen_at) first_seen,max(seen_at) last_seen
   FROM small_request_occurrences WHERE record_id=$1 AND seen_at>$2 GROUP BY response_fingerprint
 ) o ON o.response_fingerprint=s.fingerprint
 WHERE s.record_id=$1 ORDER BY o.first_seen,s.fingerprint`, r.ID, cutoff)
}
