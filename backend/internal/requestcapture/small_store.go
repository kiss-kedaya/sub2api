package requestcapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const smallMaintenanceLock int64 = 0x534d414c4c524551

type SmallStore struct{ DB *sql.DB }

type SmallRecordData struct {
	Method                 string      `json:"method"`
	Path                   string      `json:"path"`
	GroupID                int64       `json:"group_id"`
	RequestVolatileHeaders http.Header `json:"request_volatile_headers,omitempty"`
	ReplayReasons          []string    `json:"replay_reasons"`
	FirstUsage             *SmallUsage `json:"first_usage,omitempty"`
}
type SmallRecord struct {
	ID                  int64           `json:"id"`
	Bucket              time.Time       `json:"bucket"`
	UserID              int64           `json:"user_id"`
	APIKeyID            int64           `json:"api_key_id"`
	Model               string          `json:"model"`
	Status              int             `json:"status"`
	FirstSeen           time.Time       `json:"first_seen"`
	LastSeen            time.Time       `json:"last_seen"`
	ExpiresAt           time.Time       `json:"expires_at"`
	RepeatCount         int64           `json:"repeat_count"`
	InputTokens         int64           `json:"input_tokens"`
	OutputTokens        int64           `json:"output_tokens"`
	CacheReadTokens     int64           `json:"cache_read_tokens"`
	CacheCreationTokens int64           `json:"cache_creation_tokens"`
	ActualCost          string          `json:"actual_cost"`
	RawBytes            int64           `json:"raw_bytes"`
	OmittedResponses    int64           `json:"omitted_responses"`
	RequestIDs          []string        `json:"request_ids"`
	RequestHeadersHash  string          `json:"-"`
	RequestBodyHash     string          `json:"-"`
	Data                SmallRecordData `json:"data"`
	windowTrimmed       bool
}
type SmallResponseData struct {
	Status          int         `json:"status"`
	Usage           *SmallUsage `json:"usage,omitempty"`
	VolatileHeaders http.Header `json:"volatile_headers,omitempty"`
	Partial         bool        `json:"partial"`
	ReplayReasons   []string    `json:"replay_reasons"`
}
type SmallResponse struct {
	Fingerprint string            `json:"fingerprint"`
	FirstSeen   time.Time         `json:"first_seen"`
	LastSeen    time.Time         `json:"last_seen"`
	RepeatCount int64             `json:"repeat_count"`
	Data        SmallResponseData `json:"data"`
	Headers     http.Header       `json:"headers"`
	Body        string            `json:"body"`
}
type SmallDetail struct {
	Record         SmallRecord     `json:"record"`
	RequestHeaders http.Header     `json:"request_headers"`
	RequestBody    string          `json:"request_body"`
	Responses      []SmallResponse `json:"responses"`
	Curl           string          `json:"curl"`
}
type SmallFilter struct {
	UserID, APIKeyID, MinRepeats int64
	Model, RequestID             string
	Status                       int
	From, To                     time.Time
	Limit, Offset                int
}
type SmallStats struct {
	Records            int64            `json:"records"`
	Requests           int64            `json:"requests"`
	Duplicates         int64            `json:"duplicates"`
	RawBytes           int64            `json:"raw_bytes"`
	StoredBytes        int64            `json:"stored_bytes"`
	StoredLimitBytes   int64            `json:"stored_limit_bytes"`
	QuotaFull          bool             `json:"quota_full"`
	SavedBytes         int64            `json:"saved_bytes"`
	Oldest             *time.Time       `json:"oldest"`
	Newest             *time.Time       `json:"newest"`
	RepeatDistribution map[string]int64 `json:"repeat_distribution"`
	Skipped            map[string]int64 `json:"skipped"`
	Ready              bool             `json:"ready"`
}

func (s *SmallStore) Ready(ctx context.Context) bool {
	if s == nil || s.DB == nil {
		return false
	}
	var ready bool
	err := s.DB.QueryRowContext(ctx, `SELECT to_regclass('small_request_records') IS NOT NULL AND to_regclass('small_request_blobs') IS NOT NULL AND to_regclass('small_request_responses') IS NOT NULL AND to_regclass('small_request_skips') IS NOT NULL AND to_regclass('small_request_state') IS NOT NULL AND to_regclass('small_request_occurrences') IS NOT NULL`).Scan(&ready)
	return err == nil && ready
}
func volatileSmallHeaders(headers http.Header) http.Header {
	out := http.Header{}
	for k, values := range headers {
		if smallVolatileHeaders[k] {
			out[k] = values
		}
	}
	return out
}
func smallBlobRef(ctx context.Context, tx *sql.Tx, bucket time.Time, user int64, blob smallBlob, delta int) error {
	if delta > 0 {
		_, err := tx.ExecContext(ctx, `INSERT INTO small_request_blobs(bucket,user_id,hash,codec,raw_size,payload,stored_size,ref_count) VALUES($1,$2,$3,$4,$5,$6,$7,1) ON CONFLICT(bucket,user_id,hash) DO UPDATE SET ref_count=small_request_blobs.ref_count+1`, bucket, user, blob.Hash, blob.Codec, blob.RawSize, blob.Payload, len(blob.Payload))
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE small_request_blobs SET ref_count=ref_count-1 WHERE bucket=$1 AND user_id=$2 AND hash=$3`, bucket, user, blob.Hash)
	return err
}

// Save takes a shared maintenance lock and serializes writes only within a user.
// ponytail: per-user transaction lock bounds one user's write throughput; switch
// to sorted blob row locks if measured worker throughput ever requires it.
func (s *SmallStore) Save(ctx context.Context, p preparedSmallExchange) error {
	if s == nil || s.DB == nil {
		return ErrDisabled
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, smallMaintenanceLock); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('small-user:' || $1::text,0))`, p.UserID); err != nil {
		return err
	}
	var fresh, afterClear bool
	if err = tx.QueryRowContext(ctx, `SELECT $1::timestamptz > clock_timestamp()-interval '24 hours',
 COALESCE($1::timestamptz >= (SELECT cleared_at FROM small_request_state WHERE id=1), false)`, p.StartedAt).Scan(&fresh, &afterClear); err != nil {
		return err
	}
	if !fresh {
		return errSmallRetentionExpired
	}
	if !afterClear {
		return nil
	}
	data, err := json.Marshal(SmallRecordData{Method: p.Method, Path: p.Path, GroupID: p.GroupID, RequestVolatileHeaders: volatileSmallHeaders(p.RequestHeaders), ReplayReasons: p.ReplayReasons, FirstUsage: &p.Usage})
	if err != nil {
		return err
	}
	ids, err := json.Marshal([]string{p.Usage.RequestID})
	if err != nil {
		return err
	}
	var id, repeats int64
	err = tx.QueryRowContext(ctx, `INSERT INTO small_request_records(bucket,fingerprint,user_id,api_key_id,model,status,first_seen,last_seen,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens,actual_cost,raw_bytes,request_ids,request_headers_hash,request_body_hash,data)
VALUES($1,$2,$3,$4,$5,$6,$7,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT(bucket,fingerprint) DO UPDATE SET
 first_seen=LEAST(small_request_records.first_seen,EXCLUDED.first_seen),
 last_seen=GREATEST(small_request_records.last_seen,EXCLUDED.last_seen), status=EXCLUDED.status,
 repeat_count=small_request_records.repeat_count+1,
 input_tokens=small_request_records.input_tokens+EXCLUDED.input_tokens,
 output_tokens=small_request_records.output_tokens+EXCLUDED.output_tokens,
 cache_read_tokens=small_request_records.cache_read_tokens+EXCLUDED.cache_read_tokens,
 cache_creation_tokens=small_request_records.cache_creation_tokens+EXCLUDED.cache_creation_tokens,
 actual_cost=small_request_records.actual_cost+EXCLUDED.actual_cost,
 raw_bytes=small_request_records.raw_bytes+EXCLUDED.raw_bytes,
 request_ids=(SELECT COALESCE(jsonb_agg(value ORDER BY n),'[]'::jsonb) FROM
 (SELECT value,n FROM jsonb_array_elements(small_request_records.request_ids || EXCLUDED.request_ids) WITH ORDINALITY e(value,n) ORDER BY n DESC LIMIT 20) recent)
RETURNING id,repeat_count`, p.Bucket, p.Fingerprint, p.UserID, p.APIKeyID, p.Usage.Model, p.Status, p.StartedAt, p.Usage.InputTokens, p.Usage.OutputTokens, p.Usage.CacheReadTokens, p.Usage.CacheCreationTokens, smallCostOrZero(p.Usage.ActualCost), p.RawBytes, string(ids), p.RequestHeaderBlob.Hash, p.RequestBodyBlob.Hash, string(data)).Scan(&id, &repeats)
	if err != nil {
		return err
	}
	if err = insertSmallOccurrence(ctx, tx, id, repeats, p); err != nil {
		return err
	}
	if repeats == 1 {
		for _, b := range []smallBlob{p.RequestHeaderBlob, p.RequestBodyBlob} {
			if err = smallBlobRef(ctx, tx, p.Bucket, p.UserID, b, 1); err != nil {
				return err
			}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE small_request_responses SET repeat_count=repeat_count+1,last_seen=GREATEST(last_seen,$3) WHERE record_id=$1 AND fingerprint=$2`, id, p.ResponseFingerprint, p.StartedAt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM small_request_responses WHERE record_id=$1`, id).Scan(&count); err != nil {
			return err
		}
		if count >= SmallResponseSamples {
			var old, hh, bh string
			var omitted int64
			err = tx.QueryRowContext(ctx, `SELECT fingerprint,headers_hash,body_hash,repeat_count FROM small_request_responses WHERE record_id=$1 AND fingerprint<>(SELECT fingerprint FROM small_request_responses WHERE record_id=$1 ORDER BY first_seen,fingerprint LIMIT 1) ORDER BY last_seen,fingerprint LIMIT 1`, id).Scan(&old, &hh, &bh, &omitted)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM small_request_responses WHERE record_id=$1 AND fingerprint=$2`, id, old); err != nil {
				return err
			}
			for _, hash := range []string{hh, bh} {
				if err = smallBlobRef(ctx, tx, p.Bucket, p.UserID, smallBlob{Hash: hash}, -1); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE small_request_records SET omitted_responses=omitted_responses+$2 WHERE id=$1`, id, omitted); err != nil {
				return err
			}
		}
		responseData, marshalErr := json.Marshal(SmallResponseData{Status: p.Status, Usage: &p.Usage, VolatileHeaders: volatileSmallHeaders(p.ResponseHeaders), Partial: p.Partial, ReplayReasons: p.ReplayReasons})
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO small_request_responses(record_id,bucket,fingerprint,headers_hash,body_hash,first_seen,last_seen,data) VALUES($1,$2,$3,$4,$5,$6,$6,$7)`, id, p.Bucket, p.ResponseFingerprint, p.ResponseHeaderBlob.Hash, p.ResponseBodyBlob.Hash, p.StartedAt, string(responseData)); err != nil {
			return err
		}
		for _, b := range []smallBlob{p.ResponseHeaderBlob, p.ResponseBodyBlob} {
			if err = smallBlobRef(ctx, tx, p.Bucket, p.UserID, b, 1); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM small_request_blobs WHERE bucket=$1 AND user_id=$2 AND ref_count=0`, p.Bucket, p.UserID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func smallCostOrZero(cost string) string {
	if cost == "" {
		return "0"
	}
	return cost
}
func (s *SmallStore) AddSkipped(ctx context.Context, bucket time.Time, reason string, count int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO small_request_skips(bucket,reason,count) VALUES($1,$2,$3) ON CONFLICT(bucket,reason) DO UPDATE SET count=small_request_skips.count+EXCLUDED.count`, bucket, reason, count)
	return err
}

// Cleanup also runs with capture disabled. Batches are bounded; an exclusive
// transaction lock elects one instance and prevents a writer recreating old blobs.
func (s *SmallStore) Cleanup(ctx context.Context, now time.Time) error {
	cutoff := smallCutoff(now)
	bucket := cutoff.Truncate(time.Hour)
	for _, target := range []struct {
		table, condition string
		cutoff           time.Time
	}{
		{"small_request_occurrences", "seen_at<=$1", cutoff},
		{"small_request_responses", "bucket<$1", bucket},
		{"small_request_records", "bucket<$1", bucket},
		{"small_request_blobs", "bucket<$1", bucket},
		{"small_request_skips", "bucket<$1", bucket},
	} {
		for i := 0; i < 8; i++ {
			n, locked, err := s.cleanupBatch(ctx, target.table, target.condition, target.cutoff)
			if err != nil || !locked {
				return err
			}
			if n < 5000 {
				break
			}
		}
	}
	return nil
}
func (s *SmallStore) cleanupBatch(ctx context.Context, table, condition string, cutoff time.Time) (int64, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, smallMaintenanceLock).Scan(&locked); err != nil || !locked {
		return 0, locked, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE ctid IN (SELECT ctid FROM `+table+` WHERE `+condition+` LIMIT 5000)`, cutoff)
	if err != nil {
		return 0, true, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, true, err
	}
	return n, true, tx.Commit()
}
func (s *SmallStore) Clear(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, smallMaintenanceLock); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE small_request_state SET cleared_at=clock_timestamp() WHERE id=1`); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `TRUNCATE small_request_occurrences,small_request_responses,small_request_records,small_request_blobs,small_request_skips`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const smallRecordColumns = `id,bucket,user_id,api_key_id,model,status,first_seen,last_seen,repeat_count,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens,actual_cost::text,raw_bytes,omitted_responses,request_ids,request_headers_hash,request_body_hash,data,window_trimmed`

type smallRowScanner interface{ Scan(...any) error }

func scanSmallRecord(row smallRowScanner) (SmallRecord, error) {
	var r SmallRecord
	var ids, data []byte
	err := row.Scan(&r.ID, &r.Bucket, &r.UserID, &r.APIKeyID, &r.Model, &r.Status, &r.FirstSeen, &r.LastSeen, &r.RepeatCount, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheCreationTokens, &r.ActualCost, &r.RawBytes, &r.OmittedResponses, &ids, &r.RequestHeadersHash, &r.RequestBodyHash, &data, &r.windowTrimmed)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(ids, &r.RequestIDs); err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r.Data)
	r.ExpiresAt = r.LastSeen.Add(smallRetentionWindow)
	return r, err
}
func (s *SmallStore) List(ctx context.Context, f SmallFilter) ([]SmallRecord, error) {
	return s.listAt(ctx, f, smallCutoff(time.Now()))
}

func (s *SmallStore) listAt(ctx context.Context, f SmallFilter, cutoff time.Time) ([]SmallRecord, error) {
	var conditions []string
	args := []any{cutoff.Truncate(time.Hour), cutoff}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if f.UserID > 0 {
		add("user_id=$%d", f.UserID)
	}
	if f.APIKeyID > 0 {
		add("api_key_id=$%d", f.APIKeyID)
	}
	if f.MinRepeats > 0 {
		add("repeat_count>=$%d", f.MinRepeats)
	}
	if f.Model != "" {
		add("model=$%d", f.Model)
	}
	if f.RequestID != "" {
		b, _ := json.Marshal([]string{f.RequestID})
		add("request_ids @> $%d::jsonb", string(b))
	}
	if f.Status > 0 {
		add("status=$%d", f.Status)
	}
	if !f.From.IsZero() {
		add("last_seen>=$%d", f.From)
	}
	if !f.To.IsZero() {
		add("first_seen<=$%d", f.To)
	}
	limit := f.Limit
	if limit < 1 || limit > 100 {
		limit = 20
	}
	args = append(args, limit, max(0, f.Offset))
	where := ""
	if len(conditions) != 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	rows, err := s.DB.QueryContext(ctx, smallActiveRecords+`SELECT `+smallRecordColumns+` FROM live_records`+where+` ORDER BY last_seen DESC,id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SmallRecord{}
	for rows.Next() {
		r, e := scanSmallRecord(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func smallBlobPayload(ctx context.Context, tx *sql.Tx, bucket time.Time, user int64, hash string) ([]byte, error) {
	var codec string
	var payload []byte
	err := tx.QueryRowContext(ctx, `SELECT codec,payload FROM small_request_blobs WHERE bucket=$1 AND user_id=$2 AND hash=$3`, bucket, user, hash).Scan(&codec, &payload)
	if err != nil {
		return nil, err
	}
	return decodeSmallBlob(codec, payload)
}
func (s *SmallStore) Detail(ctx context.Context, id int64) (*SmallDetail, error) {
	return s.detailAt(ctx, id, smallCutoff(time.Now()))
}

func (s *SmallStore) detailAt(ctx context.Context, id int64, cutoff time.Time) (*SmallDetail, error) {
	// A sample can be evicted while the operator opens it. Read a single MVCC
	// snapshot so headers and bodies cannot disappear between these queries.
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := scanSmallRecord(tx.QueryRowContext(ctx, smallActiveRecords+`SELECT `+smallRecordColumns+` FROM live_records WHERE id=$3`, cutoff.Truncate(time.Hour), cutoff, id))
	if err != nil {
		return nil, err
	}
	d := &SmallDetail{Record: r, Responses: []SmallResponse{}}
	h, err := smallBlobPayload(ctx, tx, r.Bucket, r.UserID, r.RequestHeadersHash)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(h, &d.RequestHeaders); err != nil {
		return nil, err
	}
	for k, v := range r.Data.RequestVolatileHeaders {
		d.RequestHeaders[k] = v
	}
	body, err := smallBlobPayload(ctx, tx, r.Bucket, r.UserID, r.RequestBodyHash)
	if err != nil {
		return nil, err
	}
	d.RequestBody = string(body)
	d.Curl = SmallCurl(r.Data.Method, r.Data.Path, d.RequestHeaders, body)
	rows, err := smallResponseRows(ctx, tx, r, cutoff)
	if err != nil {
		return nil, err
	}
	type sample struct {
		response SmallResponse
		hh, bh   string
	}
	samples := []sample{}
	for rows.Next() {
		var v sample
		var data []byte
		if err = rows.Scan(&v.response.Fingerprint, &v.response.FirstSeen, &v.response.LastSeen, &v.response.RepeatCount, &data, &v.hh, &v.bh); err != nil {
			break
		}
		if err = json.Unmarshal(data, &v.response.Data); err != nil {
			break
		}
		samples = append(samples, v)
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	for _, v := range samples {
		h, err = smallBlobPayload(ctx, tx, r.Bucket, r.UserID, v.hh)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(h, &v.response.Headers); err != nil {
			return nil, err
		}
		for k, values := range v.response.Data.VolatileHeaders {
			v.response.Headers[k] = values
		}
		body, err = smallBlobPayload(ctx, tx, r.Bucket, r.UserID, v.bh)
		if err != nil {
			return nil, err
		}
		v.response.Body = string(body)
		d.Responses = append(d.Responses, v.response)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}
func (s *SmallStore) StoredBytes(ctx context.Context) (int64, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var size int64
	err := s.DB.QueryRowContext(queryCtx, `SELECT COALESCE(sum(stored_size),0) FROM small_request_blobs WHERE bucket>=$1`, smallCutoff(time.Now()).Truncate(time.Hour)).Scan(&size)
	return size, err
}
func (s *SmallStore) Stats(ctx context.Context) (SmallStats, error) {
	return s.statsAt(ctx, smallCutoff(time.Now()))
}

func (s *SmallStore) statsAt(ctx context.Context, cutoff time.Time) (SmallStats, error) {
	out := SmallStats{StoredLimitBytes: SmallStoredBytesLimit, Skipped: map[string]int64{}, RepeatDistribution: map[string]int64{}, Ready: s.Ready(ctx)}
	for _, reason := range smallSkipReasons {
		out.Skipped[reason] = 0
	}
	if !out.Ready {
		return out, nil
	}
	bucket := cutoff.Truncate(time.Hour)
	var once, few, many, hundred int64
	err := s.DB.QueryRowContext(ctx, smallActiveRecords+`SELECT count(*),COALESCE(sum(repeat_count),0),COALESCE(sum(raw_bytes),0),min(first_seen),max(last_seen),
 COUNT(*) FILTER(WHERE repeat_count=1),COUNT(*) FILTER(WHERE repeat_count BETWEEN 2 AND 9),
 COUNT(*) FILTER(WHERE repeat_count BETWEEN 10 AND 99),COUNT(*) FILTER(WHERE repeat_count>=100) FROM live_records`, bucket, cutoff).
		Scan(&out.Records, &out.Requests, &out.RawBytes, &out.Oldest, &out.Newest, &once, &few, &many, &hundred)
	if err != nil {
		return out, err
	}
	out.Duplicates = out.Requests - out.Records
	for key, count := range map[string]int64{"1": once, "2-9": few, "10-99": many, "100+": hundred} {
		if count != 0 {
			out.RepeatDistribution[key] = count
		}
	}
	if out.StoredBytes, err = s.StoredBytes(ctx); err != nil {
		return out, err
	}
	out.QuotaFull = out.StoredBytes >= SmallStoredBytesLimit
	out.SavedBytes = max(0, out.RawBytes-out.StoredBytes)
	rows, err := s.DB.QueryContext(ctx, `SELECT reason,sum(count) FROM small_request_skips WHERE bucket>=$1 GROUP BY reason`, bucket)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var reason string
		var count int64
		if err = rows.Scan(&reason, &count); err != nil {
			break
		}
		out.Skipped[reason] = count
	}
	e := rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	if e != nil {
		return out, e
	}
	return out, nil
}
