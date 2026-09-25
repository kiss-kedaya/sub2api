package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const ticketEmailOutboxPrefix = "ticket_reply_email:"

type ticketEmailJob struct {
	TicketID  int64 `json:"ticket_id"`
	MessageID int64 `json:"message_id"`
	Attempts  int   `json:"attempts"`
}

func queueTicketReplyEmail(ctx context.Context, tx *sql.Tx, ticketID, messageID int64) error {
	payload, err := json.Marshal(ticketEmailJob{TicketID: ticketID, MessageID: messageID})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO NOTHING`, ticketEmailOutboxPrefix+strconv.FormatInt(messageID, 10), string(payload))
	return err
}

func (r *ticketRepository) ProcessPendingEmail(ctx context.Context, deliver func(context.Context, int64, int64) error) (bool, error) {
	if deliver == nil {
		return false, fmt.Errorf("missing ticket email sender")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var key, payload string
	err = tx.QueryRowContext(ctx, `SELECT key, value FROM settings
		WHERE key >= $1 AND key < $2 AND updated_at <= NOW()
		ORDER BY updated_at, key LIMIT 1 FOR UPDATE SKIP LOCKED`, ticketEmailOutboxPrefix, "ticket_reply_email;").Scan(&key, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var job ticketEmailJob
	if err := json.Unmarshal([]byte(payload), &job); err != nil {
		return false, fmt.Errorf("decode ticket email job: %w", err)
	}
	if job.TicketID <= 0 || job.MessageID <= 0 || key != ticketEmailOutboxPrefix+strconv.FormatInt(job.MessageID, 10) {
		return false, fmt.Errorf("invalid ticket email job")
	}
	_, err = tx.ExecContext(ctx, `UPDATE settings SET updated_at = NOW() + INTERVAL '60 seconds' WHERE key = $1`, key)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	deliveryErr := deliver(ctx, job.TicketID, job.MessageID)
	finish, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = finish.Rollback() }()
	if deliveryErr == nil || errors.Is(deliveryErr, service.ErrTicketNotFound) {
		_, err = finish.ExecContext(ctx, "DELETE FROM settings WHERE key = $1 AND value = $2", key, payload)
	} else {
		job.Attempts = min(max(job.Attempts, 0)+1, 10)
		delay := min(30*(1<<min(job.Attempts, 7)), 3600)
		updated, encodeErr := json.Marshal(job)
		if encodeErr != nil {
			return false, encodeErr
		}
		_, err = finish.ExecContext(ctx, `UPDATE settings SET value = $2, updated_at = NOW() + ($3 * INTERVAL '1 second') WHERE key = $1 AND value = $4`, key, string(updated), delay, payload)
	}
	if err != nil {
		return false, err
	}
	if err := finish.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
