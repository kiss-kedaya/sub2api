package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const mediaBillingJobColumns = `
	id, kind, user_id, api_key_id, account_id, group_id, subscription_id,
	upstream_task_id, status, reserved_amount, snapshot, intent,
	created_at, updated_at, available_at, attempts`

const leasedMediaBillingJobColumns = `
	jobs.id, jobs.kind, jobs.user_id, jobs.api_key_id, jobs.account_id, jobs.group_id, jobs.subscription_id,
	jobs.upstream_task_id, jobs.status, jobs.reserved_amount, jobs.snapshot, jobs.intent,
	jobs.created_at, jobs.updated_at, jobs.available_at, jobs.attempts`

const mediaBillingLeaseDuration = 60 * time.Second

var _ service.MediaBillingJobRepository = (*usageBillingRepository)(nil)

type mediaBillingJobScanner interface {
	Scan(dest ...any) error
}

func scanMediaBillingJob(scanner mediaBillingJobScanner) (*service.MediaBillingJob, error) {
	var (
		job            service.MediaBillingJob
		subscriptionID sql.NullInt64
		snapshot       []byte
		intentJSON     []byte
	)
	if err := scanner.Scan(
		&job.ID,
		&job.Kind,
		&job.UserID,
		&job.APIKeyID,
		&job.AccountID,
		&job.GroupID,
		&subscriptionID,
		&job.UpstreamTaskID,
		&job.Status,
		&job.ReservedAmount,
		&snapshot,
		&intentJSON,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.AvailableAt,
		&job.Attempts,
	); err != nil {
		return nil, err
	}
	if subscriptionID.Valid {
		job.SubscriptionID = &subscriptionID.Int64
	}
	if len(snapshot) > 0 && string(snapshot) != "null" {
		job.Snapshot = append(json.RawMessage(nil), snapshot...)
	}
	if len(intentJSON) > 0 && string(intentJSON) != "null" {
		var intent service.MediaBillingIntent
		if err := json.Unmarshal(intentJSON, &intent); err != nil {
			return nil, fmt.Errorf("decode media billing intent: %w", err)
		}
		job.Intent = &intent
	}
	return &job, nil
}

func (r *usageBillingRepository) CreateMediaBillingJob(ctx context.Context, input *service.MediaBillingJob) (_ *service.MediaBillingJob, err error) {
	if err := r.validateMediaBillingRepository(); err != nil {
		return nil, err
	}
	job, snapshot, intentJSON, err := normalizeNewMediaBillingJob(input)
	if err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	created, err := scanMediaBillingJob(tx.QueryRowContext(ctx, `
		INSERT INTO media_billing_jobs (
			id, kind, user_id, api_key_id, account_id, group_id, subscription_id,
			upstream_task_id, status, reserved_amount, snapshot, intent,
			available_at, attempts
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+mediaBillingJobColumns,
		job.ID,
		job.Kind,
		job.UserID,
		job.APIKeyID,
		job.AccountID,
		job.GroupID,
		job.SubscriptionID,
		job.UpstreamTaskID,
		job.Status,
		job.ReservedAmount,
		nullableJSON(snapshot),
		nullableJSON(intentJSON),
		job.AvailableAt,
		job.Attempts,
	))
	if errors.Is(err, sql.ErrNoRows) {
		existing, getErr := r.getMediaBillingJobByIDTx(ctx, tx, job.ID, true)
		if getErr != nil {
			return nil, getErr
		}
		if !sameMediaBillingCreate(existing, job) {
			return nil, service.ErrUsageBillingRequestConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return existing, nil
	}
	if err != nil {
		return nil, err
	}
	if created.Kind == service.MediaBillingKindImage {
		if err := validateMediaBillingIntentOwner(created, created.Intent); err != nil {
			return nil, err
		}
	}

	if created.Kind == service.MediaBillingKindGrokVideo {
		if created.SubscriptionID != nil {
			if err := r.reserveMediaBillingSubscription(ctx, tx, created); err != nil {
				return nil, err
			}
		} else if created.ReservedAmount > 0 {
			_, reserveErr := reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{
				UserID:     created.UserID,
				APIKeyID:   created.APIKeyID,
				BatchID:    created.ID,
				HoldAmount: created.ReservedAmount,
			})
			if reserveErr != nil {
				if errors.Is(reserveErr, service.ErrBatchImageInsufficientBalance) {
					return nil, service.ErrBalanceWithholdingFailed.WithCause(reserveErr)
				}
				return nil, reserveErr
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return created, nil
}

func normalizeNewMediaBillingJob(input *service.MediaBillingJob) (*service.MediaBillingJob, json.RawMessage, json.RawMessage, error) {
	if input == nil {
		return nil, nil, nil, errors.New("media billing job is nil")
	}
	job := *input
	job.ID = strings.TrimSpace(job.ID)
	job.Kind = strings.TrimSpace(job.Kind)
	job.UpstreamTaskID = strings.TrimSpace(job.UpstreamTaskID)
	job.Status = strings.TrimSpace(job.Status)
	job.ReservedAmount = service.QuantizeUsageBillingAmount(job.ReservedAmount)
	if job.ID == "" || job.UserID <= 0 || job.APIKeyID <= 0 || job.AccountID <= 0 || job.GroupID <= 0 ||
		job.ReservedAmount < 0 || math.IsNaN(job.ReservedAmount) || math.IsInf(job.ReservedAmount, 0) || job.Attempts < 0 {
		return nil, nil, nil, errors.New("invalid media billing job")
	}
	if job.AvailableAt.IsZero() {
		job.AvailableAt = time.Now().UTC()
	}
	if len(job.Snapshot) > 0 && !json.Valid(job.Snapshot) {
		return nil, nil, nil, errors.New("invalid media billing snapshot")
	}

	var intentJSON json.RawMessage
	switch job.Kind {
	case service.MediaBillingKindGrokVideo:
		if job.Status != service.MediaBillingCreating || job.Intent != nil || job.UpstreamTaskID != "" {
			return nil, nil, nil, errors.New("video media billing job must start in creating state")
		}
	case service.MediaBillingKindImage:
		if job.Status != service.MediaBillingReady || job.Intent == nil || job.ReservedAmount != 0 {
			return nil, nil, nil, errors.New("image media billing job must start ready with zero repository hold")
		}
		intent := *job.Intent
		encoded, err := json.Marshal(&intent)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("encode media billing intent: %w", err)
		}
		job.Intent = &intent
		intentJSON = encoded
	default:
		return nil, nil, nil, errors.New("unsupported media billing job kind")
	}

	snapshot := append(json.RawMessage(nil), job.Snapshot...)
	return &job, snapshot, intentJSON, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}

func sameMediaBillingCreate(existing, requested *service.MediaBillingJob) bool {
	if existing == nil || requested == nil {
		return false
	}
	return existing.Kind == requested.Kind &&
		existing.UserID == requested.UserID &&
		existing.APIKeyID == requested.APIKeyID &&
		service.QuantizeUsageBillingAmount(existing.ReservedAmount) == service.QuantizeUsageBillingAmount(requested.ReservedAmount)
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (r *usageBillingRepository) reserveMediaBillingSubscription(ctx context.Context, tx *sql.Tx, job *service.MediaBillingJob) error {
	if job == nil || job.SubscriptionID == nil {
		return service.ErrSubscriptionInvalid
	}
	var (
		userID, groupID                       int64
		status                                string
		startsAt, expiresAt                   time.Time
		dailyUsage, weeklyUsage, monthlyUsage float64
		dailyLimit, weeklyLimit, monthlyLimit sql.NullFloat64
	)
	err := tx.QueryRowContext(ctx, `
		SELECT us.user_id, us.group_id, us.status, us.starts_at, us.expires_at,
			us.daily_usage_usd, us.weekly_usage_usd, us.monthly_usage_usd,
			g.daily_limit_usd, g.weekly_limit_usd, g.monthly_limit_usd
		FROM user_subscriptions us
		JOIN groups g ON g.id = us.group_id AND g.deleted_at IS NULL
		WHERE us.id = $1 AND us.deleted_at IS NULL
		FOR UPDATE OF us
	`, *job.SubscriptionID).Scan(
		&userID, &groupID, &status, &startsAt, &expiresAt,
		&dailyUsage, &weeklyUsage, &monthlyUsage,
		&dailyLimit, &weeklyLimit, &monthlyLimit,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrSubscriptionInvalid
	}
	if err != nil {
		return err
	}
	now := time.Now()
	if userID != job.UserID || groupID != job.GroupID || status != service.SubscriptionStatusActive ||
		now.Before(startsAt) || !now.Before(expiresAt) {
		return service.ErrSubscriptionInvalid
	}

	var outstanding float64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(reserved_amount), 0)
		FROM media_billing_jobs
		WHERE subscription_id = $1
			AND status IN ('creating', 'submitted', 'ready')
	`, *job.SubscriptionID).Scan(&outstanding); err != nil {
		return err
	}
	if dailyLimit.Valid && dailyLimit.Float64 > 0 && dailyUsage+outstanding > dailyLimit.Float64 {
		return service.ErrDailyLimitExceeded
	}
	if weeklyLimit.Valid && weeklyLimit.Float64 > 0 && weeklyUsage+outstanding > weeklyLimit.Float64 {
		return service.ErrWeeklyLimitExceeded
	}
	if monthlyLimit.Valid && monthlyLimit.Float64 > 0 && monthlyUsage+outstanding > monthlyLimit.Float64 {
		return service.ErrMonthlyLimitExceeded
	}
	return nil
}

func (r *usageBillingRepository) SubmitMediaBillingJob(ctx context.Context, id, upstreamTaskID string, snapshot json.RawMessage) (err error) {
	if err := r.validateMediaBillingRepository(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	upstreamTaskID = strings.TrimSpace(upstreamTaskID)
	if id == "" || upstreamTaskID == "" || (len(snapshot) > 0 && !json.Valid(snapshot)) {
		return errors.New("invalid media billing submit")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	job, err := r.getMediaBillingJobByIDTx(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if job.UpstreamTaskID != "" && job.UpstreamTaskID != upstreamTaskID {
		return service.ErrUsageBillingRequestConflict
	}
	if job.Status == service.MediaBillingFailed {
		return service.ErrUsageBillingRequestConflict
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE media_billing_jobs
		SET upstream_task_id = CASE WHEN BTRIM(upstream_task_id) = '' THEN $2 ELSE upstream_task_id END,
			snapshot = CASE WHEN BTRIM(upstream_task_id) = '' THEN $3 ELSE snapshot END,
			status = CASE WHEN status = $4 THEN $5 ELSE status END,
			available_at = CASE WHEN status = $4 THEN NOW() ELSE available_at END,
			updated_at = NOW()
		WHERE id = $1
	`, id, upstreamTaskID, nullableJSON(snapshot), service.MediaBillingCreating, service.MediaBillingSubmitted)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (r *usageBillingRepository) GetMediaBillingJob(ctx context.Context, userID, apiKeyID int64, upstreamTaskID string) (*service.MediaBillingJob, error) {
	if err := r.validateMediaBillingRepository(); err != nil {
		return nil, err
	}
	upstreamTaskID = strings.TrimSpace(upstreamTaskID)
	job, err := scanMediaBillingJob(r.db.QueryRowContext(ctx, `
		SELECT `+mediaBillingJobColumns+`
		FROM media_billing_jobs
		WHERE user_id = $1 AND api_key_id = $2 AND upstream_task_id = $3
	`, userID, apiKeyID, upstreamTaskID))
	return translateMediaBillingJobRead(job, err)
}

func (r *usageBillingRepository) GetMediaBillingJobByID(ctx context.Context, id string) (*service.MediaBillingJob, error) {
	if err := r.validateMediaBillingRepository(); err != nil {
		return nil, err
	}
	job, err := scanMediaBillingJob(r.db.QueryRowContext(ctx, `
		SELECT `+mediaBillingJobColumns+`
		FROM media_billing_jobs
		WHERE id = $1
	`, strings.TrimSpace(id)))
	return translateMediaBillingJobRead(job, err)
}

func translateMediaBillingJobRead(job *service.MediaBillingJob, err error) (*service.MediaBillingJob, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrMediaBillingJobNotFound
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (r *usageBillingRepository) getMediaBillingJobByIDTx(ctx context.Context, tx *sql.Tx, id string, lock bool) (*service.MediaBillingJob, error) {
	query := `SELECT ` + mediaBillingJobColumns + ` FROM media_billing_jobs WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	job, err := scanMediaBillingJob(tx.QueryRowContext(ctx, query, strings.TrimSpace(id)))
	return translateMediaBillingJobRead(job, err)
}

func (r *usageBillingRepository) PrepareMediaBillingIntent(ctx context.Context, id string, input *service.MediaBillingIntent) (_ *service.MediaBillingIntent, err error) {
	if err := r.validateMediaBillingRepository(); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, errors.New("media billing intent is nil")
	}
	intent := *input
	intentJSON, err := json.Marshal(&intent)
	if err != nil {
		return nil, fmt.Errorf("encode media billing intent: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	job, err := r.getMediaBillingJobByIDTx(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if job.Intent != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return job.Intent, nil
	}
	if job.Status != service.MediaBillingSubmitted && job.Status != service.MediaBillingReady {
		return nil, service.ErrUsageBillingRequestConflict
	}
	if err := validateMediaBillingIntentOwner(job, &intent); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_billing_jobs
		SET intent = $2, status = $3, available_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND intent IS NULL
	`, job.ID, []byte(intentJSON), service.MediaBillingReady); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return &intent, nil
}

func validateMediaBillingIntentOwner(job *service.MediaBillingJob, intent *service.MediaBillingIntent) error {
	if job == nil || intent == nil {
		return service.ErrUsageBillingRequestConflict
	}
	cmd := &intent.Command
	if cmd.RequestID == "" || cmd.UserID != job.UserID || cmd.APIKeyID != job.APIKeyID ||
		cmd.AccountID != job.AccountID || !sameOptionalInt64(cmd.SubscriptionID, job.SubscriptionID) {
		return service.ErrUsageBillingRequestConflict
	}
	if job.SubscriptionID == nil && cmd.BillingType != service.BillingTypeBalance {
		return service.ErrUsageBillingRequestConflict
	}
	if job.SubscriptionID != nil && cmd.BillingType != service.BillingTypeSubscription {
		return service.ErrUsageBillingRequestConflict
	}
	return nil
}

func (r *usageBillingRepository) ApplyMediaBillingJob(ctx context.Context, id string) (_ *service.UsageBillingApplyResult, err error) {
	if err := r.validateMediaBillingRepository(); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	job, err := r.getMediaBillingJobByIDTx(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if job.Status == service.MediaBillingDone {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return &service.UsageBillingApplyResult{}, nil
	}
	if job.Status == service.MediaBillingBilled {
		result := mediaBillingAlreadyBilledResult(job)
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return result, nil
	}
	if job.Status != service.MediaBillingReady || job.Intent == nil {
		return nil, service.ErrUsageBillingRequestConflict
	}
	if err := validateMediaBillingIntentOwner(job, job.Intent); err != nil {
		return nil, err
	}

	cmd := job.Intent.Command
	cmd.Normalize()
	claimed, err := r.claimUsageBillingKey(ctx, tx, &cmd)
	if err != nil {
		return nil, err
	}
	result := &service.UsageBillingApplyResult{Applied: claimed}
	if !claimed {
		if job.Kind == service.MediaBillingKindGrokVideo && job.SubscriptionID == nil && job.ReservedAmount > 0 {
			holdResult, captureErr := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{
				UserID:       job.UserID,
				APIKeyID:     job.APIKeyID,
				BatchID:      job.ID,
				HoldAmount:   job.ReservedAmount,
				ActualAmount: 0,
			})
			if captureErr != nil {
				return nil, captureErr
			}
			result.NewBalance = holdResult.NewBalance
		}
		result.BalanceFinalizationPending = job.Kind == service.MediaBillingKindImage && cmd.BalancePreauthorized
	} else {
		effectsCommand := cmd
		if job.Kind == service.MediaBillingKindGrokVideo && job.SubscriptionID == nil {
			holdResult, captureErr := r.captureMediaBillingVideoBalance(ctx, tx, job, cmd.BalanceCost)
			if captureErr != nil {
				return nil, captureErr
			}
			result.NewBalance = holdResult.NewBalance
			effectsCommand.BalanceCost = 0
			effectsCommand.BalancePreauthorized = false
		}
		if err := r.applyUsageBillingEffects(ctx, tx, &effectsCommand, result); err != nil {
			return nil, err
		}
	}

	updated, err := tx.ExecContext(ctx, `
		UPDATE media_billing_jobs
		SET status = $2, available_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status = $3
	`, job.ID, service.MediaBillingBilled, service.MediaBillingReady)
	if err != nil {
		return nil, err
	}
	if err := requireOneMediaBillingRow(updated); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func mediaBillingAlreadyBilledResult(job *service.MediaBillingJob) *service.UsageBillingApplyResult {
	result := &service.UsageBillingApplyResult{}
	if job != nil && job.Kind == service.MediaBillingKindImage && job.Intent != nil {
		result.BalanceFinalizationPending = job.Intent.Command.BalancePreauthorized
	}
	return result
}

func (r *usageBillingRepository) captureMediaBillingVideoBalance(ctx context.Context, tx *sql.Tx, job *service.MediaBillingJob, actual float64) (*service.BatchImageBalanceHoldResult, error) {
	actual = service.QuantizeUsageBillingAmount(actual)
	if actual < 0 || math.IsNaN(actual) || math.IsInf(actual, 0) {
		return nil, errors.New("invalid media billing actual amount")
	}
	hold := service.QuantizeUsageBillingAmount(job.ReservedAmount)
	if actual > hold {
		topUp := service.QuantizeUsageBillingAmount(actual - hold)
		_, err := reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{
			UserID:     job.UserID,
			APIKeyID:   job.APIKeyID,
			BatchID:    job.ID,
			HoldAmount: topUp,
		})
		if err != nil {
			if errors.Is(err, service.ErrBatchImageInsufficientBalance) {
				return nil, service.ErrBalanceWithholdingFailed.WithCause(err)
			}
			return nil, err
		}
		hold = actual
	}
	return captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{
		UserID:       job.UserID,
		APIKeyID:     job.APIKeyID,
		BatchID:      job.ID,
		HoldAmount:   hold,
		ActualAmount: actual,
	})
}

func (r *usageBillingRepository) FailMediaBillingJob(ctx context.Context, id string) (_ *service.UsageBillingApplyResult, err error) {
	if err := r.validateMediaBillingRepository(); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	job, err := r.getMediaBillingJobByIDTx(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if job.Status == service.MediaBillingFailed {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return &service.UsageBillingApplyResult{}, nil
	}
	if (job.Status != service.MediaBillingCreating && job.Status != service.MediaBillingSubmitted) || job.Intent != nil {
		return nil, service.ErrUsageBillingRequestConflict
	}
	result := &service.UsageBillingApplyResult{Applied: true}
	if job.SubscriptionID == nil && job.ReservedAmount > 0 {
		holdResult, captureErr := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{
			UserID:       job.UserID,
			APIKeyID:     job.APIKeyID,
			BatchID:      job.ID,
			HoldAmount:   job.ReservedAmount,
			ActualAmount: 0,
		})
		if captureErr != nil {
			return nil, captureErr
		}
		result.NewBalance = holdResult.NewBalance
	}
	updated, err := tx.ExecContext(ctx, `
		UPDATE media_billing_jobs
		SET status = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ($3, $4) AND intent IS NULL
	`, job.ID, service.MediaBillingFailed, service.MediaBillingCreating, service.MediaBillingSubmitted)
	if err != nil {
		return nil, err
	}
	if err := requireOneMediaBillingRow(updated); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func (r *usageBillingRepository) CompleteMediaBillingJob(ctx context.Context, id string) error {
	if err := r.validateMediaBillingRepository(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE media_billing_jobs
		SET status = CASE WHEN status = $3 THEN $3 ELSE $2 END,
			updated_at = CASE WHEN status = $3 THEN updated_at ELSE NOW() END
		WHERE id = $1 AND status IN ($4, $3)
	`, strings.TrimSpace(id), service.MediaBillingDone, service.MediaBillingDone, service.MediaBillingBilled)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	return r.mediaBillingMissingOrConflict(ctx, id)
}

func (r *usageBillingRepository) ListMediaBillingJobs(ctx context.Context, limit int) ([]*service.MediaBillingJob, error) {
	if err := r.validateMediaBillingRepository(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []*service.MediaBillingJob{}, nil
	}
	if limit > 32 {
		limit = 32
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH picked AS (
			SELECT id
			FROM media_billing_jobs
			WHERE status IN ('submitted', 'ready', 'billed')
				AND available_at <= NOW()
			ORDER BY available_at, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE media_billing_jobs AS jobs
		SET available_at = NOW() + ($2::bigint * INTERVAL '1 millisecond'), updated_at = NOW()
		FROM picked
		WHERE jobs.id = picked.id
		RETURNING `+leasedMediaBillingJobColumns,
		limit,
		mediaBillingLeaseDuration.Milliseconds(),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*service.MediaBillingJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanMediaBillingJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *usageBillingRepository) RetryMediaBillingJob(ctx context.Context, id string, delay time.Duration) error {
	if err := r.validateMediaBillingRepository(); err != nil {
		return err
	}
	if delay < 0 {
		delay = 0
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE media_billing_jobs
		SET attempts = attempts + 1,
			available_at = NOW() + ($2::bigint * INTERVAL '1 millisecond'),
			updated_at = NOW()
		WHERE id = $1 AND status IN ('submitted', 'ready', 'billed')
	`, strings.TrimSpace(id), delay.Milliseconds())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	return r.mediaBillingMissingOrConflict(ctx, id)
}

func (r *usageBillingRepository) mediaBillingMissingOrConflict(ctx context.Context, id string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_billing_jobs WHERE id = $1)`, strings.TrimSpace(id)).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return service.ErrMediaBillingJobNotFound
	}
	return service.ErrUsageBillingRequestConflict
}

func requireOneMediaBillingRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return service.ErrUsageBillingRequestConflict
	}
	return nil
}

func (r *usageBillingRepository) validateMediaBillingRepository() error {
	if r == nil || r.db == nil {
		return errors.New("usage billing repository db is nil")
	}
	return nil
}
