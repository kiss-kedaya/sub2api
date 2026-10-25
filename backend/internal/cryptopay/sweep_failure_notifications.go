package cryptopay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

const (
	sweepAlertPrefix   = "crypto.sweep.alert.v1."
	sweepAlertCooldown = 30 * time.Minute
	sweepAlertRetry    = time.Minute
	sweepAlertBatch    = 10
	sweepAlertBudget   = 10 * time.Second
	sweepAlertAckTime  = 3 * time.Second
)

// One row per task keeps independent failures from replacing each other.
// The outbox contains fixed, sanitized metadata, never signed bytes, keys,
// RPC endpoints, addresses or arbitrary upstream/driver error strings.
type sweepFailureAlert struct {
	Version       int    `json:"version"`
	Fingerprint   string `json:"fingerprint"`
	Body          string `json:"body"`
	Pending       bool   `json:"pending"`
	LastSentAt    int64  `json:"last_sent_at"`
	NextAttemptAt int64  `json:"next_attempt_at"`
	DeliveryToken string `json:"delivery_token,omitempty"`
}

type sweepFailureEvent struct{ key, fingerprint, body string }

func sweepFailureEvents(summary *sweeper.Summary, runErr error) []sweepFailureEvent {
	if summary != nil && summary.DryRun {
		return nil
	}
	var events []sweepFailureEvent
	if summary != nil {
		for _, entry := range summary.Entries {
			// Ordinary confirmation waits are not failures. Pending jobs with
			// an RPC/signing/persistence error still require an operator alert.
			reason := rpc.SafeRecordedError(entry.Error)
			if entry.Status != "failed" && entry.Status != "manual_review" &&
				(entry.Status != "pending" || reason == "" || reason == "awaiting canonical receipt and required confirmations" || reason == "awaiting gas payer transaction confirmation") {
				continue
			}
			if reason == "" {
				reason = "Payment operation failed; reconcile transaction status before retrying"
			}
			chain := "未知链"
			for _, known := range evmChains {
				if entry.Chain == known {
					chain = chainDisplayName(known)
					break
				}
			}
			identity := fmt.Sprintf("job:%d", entry.JobID)
			task := "未创建持久任务"
			if entry.JobID > 0 {
				task = fmt.Sprintf("#%d", entry.JobID)
			} else {
				// Hash local identity only; none of these fields enter Bark.
				identity = "unreserved:" + entry.Chain + ":" + strings.ToLower(entry.Address) + ":" + strings.ToLower(entry.Token)
			}
			fingerprint := alertDigest(entry.Status + ":" + reason)
			body := fmt.Sprintf("链：%s\n归集任务：%s\n原因：%s\n请检查后台 Gas 钱包余额和归集记录；有交易哈希的任务须先核对链上结果，不要重复付款。", chain, task, reason)
			events = append(events, sweepFailureEvent{key: sweepAlertPrefix + alertDigest(identity), fingerprint: fingerprint, body: body})
		}
	}
	if len(events) == 0 && runErr != nil {
		reason := rpc.SafeError(runErr)
		events = append(events, sweepFailureEvent{
			key: sweepAlertPrefix + alertDigest("run:"+reason), fingerprint: alertDigest(reason),
			body: "归集运行未完成\n原因：" + reason + "\n请检查后台 Gas 钱包余额和归集记录；本通知不代表资金已转出，请先核对交易结果。",
		})
	}
	return events
}

func alertDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// reportSweepFailure survives a canceled HTTP request but remains bounded.
// Read-only review instances cannot write an alert or send a real push.
func (s *Service) reportSweepFailure(ctx context.Context, summary *sweeper.Summary, runErr error) {
	if s == nil || s.db == nil || mode.CheckWritable() != nil {
		return
	}
	events := sweepFailureEvents(summary, runErr)
	if len(events) == 0 {
		return
	}
	alertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sweepAlertBudget)
	defer cancel()
	if err := s.queueSweepFailureAlerts(alertCtx, events, time.Now()); err != nil {
		slog.Warn("[cryptopay] could not persist sweep failure notification")
		return
	}
	if err := s.deliverSweepFailureAlerts(alertCtx); err != nil {
		slog.Warn("[cryptopay] sweep failure notification pending")
	}
}

func (s *Service) queueSweepFailureAlerts(ctx context.Context, events []sweepFailureEvent, now time.Time) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, sweepAlertBudget)
	defer cancel()
	// A single autocommit statement durably queues the entire batch or none
	// of it. Deduplication avoids ON CONFLICT touching a row twice; ordering
	// makes concurrent batches acquire existing rows in the same order.
	type item struct {
		Key   string            `json:"key"`
		Value sweepFailureAlert `json:"value"`
	}
	byKey := make(map[string]sweepFailureEvent, len(events))
	for _, event := range events {
		byKey[event.key] = event
	}
	items := make([]item, 0, len(byKey))
	for _, event := range byKey {
		items = append(items, item{Key: event.key, Value: sweepFailureAlert{Version: 1,
			Fingerprint: event.fingerprint, Body: event.body, Pending: true}})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	encoded, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at)
		SELECT incoming.key,incoming.value::text,NOW()
		FROM jsonb_to_recordset($1::jsonb) AS incoming(key text,value jsonb)
		ORDER BY incoming.key
		ON CONFLICT (key) DO UPDATE SET
		value=jsonb_set(EXCLUDED.value::jsonb,'{last_sent_at}',COALESCE(settings.value::jsonb->'last_sent_at','0'::jsonb))::text,
		updated_at=NOW()
		WHERE COALESCE((settings.value::jsonb->>'version')::int,0)<>1
		OR settings.value::jsonb->>'fingerprint' IS DISTINCT FROM EXCLUDED.value::jsonb->>'fingerprint'
		OR (COALESCE((settings.value::jsonb->>'pending')::boolean,false)=false
		AND COALESCE((settings.value::jsonb->>'last_sent_at')::bigint,0)<=$2)`,
		string(encoded), now.Add(-sweepAlertCooldown).Unix())
	return err
}

// deliverSweepFailureAlerts is called both immediately and by the existing
// wallet security monitor. Short atomic claims serialize delivery across app
// instances; network work never holds a database transaction or row lock.
func (s *Service) deliverSweepFailureAlerts(ctx context.Context) error {
	if s == nil || s.db == nil || s.getPushNotifier() == nil {
		return nil
	}
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sweepAlertBudget)
	defer cancel()
	// Ordinary settings can contain plain text. CASE guards JSON casts even
	// when PostgreSQL reorders WHERE predicates; the outer range uses key's
	// existing index and avoids reading unrelated configuration values.
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM settings
		WHERE key >= $1 AND key < $2 AND CASE WHEN key >= $1 AND key < $2 THEN
		value::jsonb->>'pending'='true' AND COALESCE((value::jsonb->>'next_attempt_at')::bigint,0)<=$3
		ELSE false END
		ORDER BY updated_at LIMIT $4`, sweepAlertPrefix, "crypto.sweep.alert.v1/", time.Now().Unix(), sweepAlertBatch)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	rowErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(rowErr, closeErr); err != nil {
		return err
	}
	var deliveryErr error
	for _, key := range keys {
		if err := s.deliverSweepFailureAlert(ctx, key, time.Now()); err != nil {
			deliveryErr = errors.Join(deliveryErr, err)
		}
	}
	return deliveryErr
}

func (s *Service) deliverSweepFailureAlert(ctx context.Context, key string, now time.Time) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	push := s.getPushNotifier()
	if push == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, sweepAlertBudget)
	defer cancel()
	deadline, _ := ctx.Deadline()
	// The claim and send honor cancellation and end before the shared deadline,
	// reserving time to acknowledge success or retain a retry after cancellation.
	workTime := min(5*time.Second, time.Until(deadline)-sweepAlertAckTime)
	if workTime <= 0 {
		return context.DeadlineExceeded
	}
	workCtx, cancelWork := context.WithTimeout(ctx, workTime)
	defer cancelWork()
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return errors.New("sweep notification claim is unavailable")
	}
	token := hex.EncodeToString(tokenBytes[:])
	var raw string
	if err := s.db.QueryRowContext(workCtx, `UPDATE settings SET
		value=(value::jsonb || jsonb_build_object('delivery_token',$2::text,'next_attempt_at',$3::bigint))::text,
		updated_at=NOW() WHERE key=$1 AND value::jsonb->>'pending'='true'
		AND COALESCE((value::jsonb->>'next_attempt_at')::bigint,0)<=$4 RETURNING value`,
		key, token, deadline.Unix()+1, now.Unix()).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var state sweepFailureAlert
	if json.Unmarshal([]byte(raw), &state) != nil || state.Version != 1 {
		return errors.New("invalid sweep notification state")
	}
	sendErr := push.PushToAdmins(workCtx, "链上归集失败，请检查", state.Body, "crypto-sweep")
	state.DeliveryToken = ""
	if sendErr == nil {
		state.Pending, state.LastSentAt, state.NextAttemptAt = false, now.Unix(), 0
	} else {
		state.NextAttemptAt = now.Add(sweepAlertRetry).Unix()
	}
	ackCtx, cancelAck := context.WithTimeout(context.WithoutCancel(ctx), min(sweepAlertAckTime, max(time.Until(deadline), 0)))
	defer cancelAck()
	if err := s.saveSweepAlert(ackCtx, key, token, state); err != nil {
		return err
	}
	if sendErr != nil {
		// A sender can include its device key or endpoint in errors. Never
		// propagate it into the payment response, persisted alert or logs.
		return errors.New("sweep failure notification delivery pending")
	}
	return nil
}

func (s *Service) saveSweepAlert(ctx context.Context, key, token string, state sweepFailureAlert) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	// A failed task may acquire a new fingerprint while a previous push is in
	// flight. An old claim may never clear that newer alert or a renewed lease.
	_, err = s.db.ExecContext(ctx, `UPDATE settings SET value=$2,updated_at=NOW()
		WHERE key=$1 AND value::jsonb->>'delivery_token'=$3`, key, string(encoded), token)
	return err
}
