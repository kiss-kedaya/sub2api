package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type groupWatchRepository struct {
	db *sql.DB
}

func NewGroupWatchRepository(db *sql.DB) service.GroupWatchRepository {
	return &groupWatchRepository{db: db}
}

func (r *groupWatchRepository) ListSubscribedGroupIDs(ctx context.Context, userID int64) ([]int64, error) {
	if r == nil || r.db == nil || userID <= 0 {
		return []int64{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT group_id FROM user_group_watch_subscriptions WHERE user_id = $1 ORDER BY group_id ASC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list group watch subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]int64, 0)
	for rows.Next() {
		var gid int64
		if err := rows.Scan(&gid); err != nil {
			return nil, fmt.Errorf("scan group watch subscription: %w", err)
		}
		out = append(out, gid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group watch subscriptions: %w", err)
	}
	return out, nil
}

func (r *groupWatchRepository) Subscribe(ctx context.Context, userID, groupID int64) error {
	if r == nil || r.db == nil {
		return errors.New("group watch repository unavailable")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO user_group_watch_subscriptions (user_id, group_id, created_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (user_id, group_id) DO NOTHING`,
		userID, groupID)
	if err != nil {
		return fmt.Errorf("subscribe group watch: %w", err)
	}
	return nil
}

func (r *groupWatchRepository) Unsubscribe(ctx context.Context, userID, groupID int64) error {
	if r == nil || r.db == nil {
		return errors.New("group watch repository unavailable")
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM user_group_watch_subscriptions WHERE user_id = $1 AND group_id = $2`,
		userID, groupID)
	if err != nil {
		return fmt.Errorf("unsubscribe group watch: %w", err)
	}
	return nil
}

func (r *groupWatchRepository) ListWatchedGroupIDs(ctx context.Context) ([]int64, error) {
	if r == nil || r.db == nil {
		return []int64{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT group_id FROM user_group_watch_subscriptions ORDER BY group_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list watched group ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]int64, 0)
	for rows.Next() {
		var gid int64
		if err := rows.Scan(&gid); err != nil {
			return nil, fmt.Errorf("scan watched group id: %w", err)
		}
		out = append(out, gid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate watched group ids: %w", err)
	}
	return out, nil
}

func (r *groupWatchRepository) ListSubscriberUsersByGroupID(ctx context.Context, groupID int64) ([]service.GroupWatchSubscriber, error) {
	if r == nil || r.db == nil || groupID <= 0 {
		return []service.GroupWatchSubscriber{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT u.id, COALESCE(u.email, ''), COALESCE(u.username, ''), COALESCE(u.role, 'user'),
		        COALESCE(u.balance_notify_primary_email_disabled, FALSE),
		        COALESCE(u.balance_notify_extra_emails, '')
		   FROM user_group_watch_subscriptions s
		   JOIN users u ON u.id = s.user_id
		  WHERE s.group_id = $1
		    AND u.status = 'active'
		    AND u.deleted_at IS NULL
		  ORDER BY u.id ASC`,
		groupID)
	if err != nil {
		return nil, fmt.Errorf("list group watch subscribers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.GroupWatchSubscriber, 0)
	for rows.Next() {
		var sub service.GroupWatchSubscriber
		var rawExtra string
		if err := rows.Scan(&sub.UserID, &sub.Email, &sub.Username, &sub.Role, &sub.PrimaryEmailBlocked, &rawExtra); err != nil {
			return nil, fmt.Errorf("scan group watch subscriber: %w", err)
		}
		sub.ExtraNotifyEmails = service.ParseNotifyEmails(rawExtra)
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group watch subscribers: %w", err)
	}
	return out, nil
}

func (r *groupWatchRepository) GetGroupWatchStates(ctx context.Context, groupIDs []int64) (map[int64]service.GroupMonitorWatchState, error) {
	out := make(map[int64]service.GroupMonitorWatchState, len(groupIDs))
	if r == nil || r.db == nil || len(groupIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT group_id, status, black_notified, last_bucket_start, cache_rate, availability_pct, updated_at
		   FROM group_monitor_watch_states
		  WHERE group_id = ANY($1)`,
		pq.Array(groupIDs))
	if err != nil {
		return nil, fmt.Errorf("get group watch states: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var st service.GroupMonitorWatchState
		if err := rows.Scan(&st.GroupID, &st.Status, &st.BlackNotified, &st.LastBucketStart, &st.CacheRate, &st.AvailabilityPct, &st.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan group watch state: %w", err)
		}
		out[st.GroupID] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group watch states: %w", err)
	}
	return out, nil
}

func (r *groupWatchRepository) UpsertGroupWatchState(ctx context.Context, state service.GroupMonitorWatchState) error {
	if r == nil || r.db == nil {
		return errors.New("group watch repository unavailable")
	}
	updatedAt := state.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO group_monitor_watch_states (
			group_id, status, black_notified, last_bucket_start, cache_rate, availability_pct, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (group_id) DO UPDATE SET
			status = EXCLUDED.status,
			black_notified = EXCLUDED.black_notified,
			last_bucket_start = EXCLUDED.last_bucket_start,
			cache_rate = EXCLUDED.cache_rate,
			availability_pct = EXCLUDED.availability_pct,
			updated_at = EXCLUDED.updated_at`,
		state.GroupID, state.Status, state.BlackNotified, state.LastBucketStart.UTC(), state.CacheRate, state.AvailabilityPct, updatedAt.UTC())
	if err != nil {
		return fmt.Errorf("upsert group watch state: %w", err)
	}
	return nil
}

func (r *groupWatchRepository) InsertNotificationIfNew(ctx context.Context, notif service.UserGroupWatchNotification) (bool, int64, error) {
	if r == nil || r.db == nil {
		return false, 0, errors.New("group watch repository unavailable")
	}
	metaBytes := []byte("{}")
	if len(notif.Metadata) > 0 {
		if b, err := json.Marshal(notif.Metadata); err == nil {
			metaBytes = b
		}
	}
	createdAt := notif.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO user_group_watch_notifications (
			user_id, group_id, event_type, dedupe_key, title, content, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
		ON CONFLICT (user_id, dedupe_key) DO NOTHING
		RETURNING id`,
		notif.UserID, notif.GroupID, notif.EventType, notif.DedupeKey, notif.Title, notif.Content, string(metaBytes), createdAt.UTC(),
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("insert group watch notification: %w", err)
	}
	return true, id, nil
}

func (r *groupWatchRepository) SummaryNotifications(ctx context.Context, userID int64) (int, int, error) {
	if r == nil || r.db == nil || userID <= 0 {
		return 0, 0, nil
	}
	var total, unread int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE read_at IS NULL)
		   FROM user_group_watch_notifications
		  WHERE user_id = $1`,
		userID).Scan(&total, &unread)
	if err != nil {
		return 0, 0, fmt.Errorf("summary group watch notifications: %w", err)
	}
	return total, unread, nil
}

func (r *groupWatchRepository) ListNotifications(ctx context.Context, userID int64, unreadOnly bool, limit int) ([]service.UserGroupWatchNotification, error) {
	if r == nil || r.db == nil || userID <= 0 {
		return []service.UserGroupWatchNotification{}, nil
	}
	query := `SELECT id, user_id, group_id, event_type, dedupe_key, title, content, metadata, read_at, created_at
	            FROM user_group_watch_notifications
	           WHERE user_id = $1`
	if unreadOnly {
		query += ` AND read_at IS NULL`
	}
	query += ` ORDER BY (read_at IS NULL) DESC, created_at DESC, id DESC LIMIT $2`

	rows, err := r.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list group watch notifications: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UserGroupWatchNotification, 0)
	for rows.Next() {
		var item service.UserGroupWatchNotification
		var metaRaw []byte
		var readAt sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.GroupID, &item.EventType, &item.DedupeKey,
			&item.Title, &item.Content, &metaRaw, &readAt, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan group watch notification: %w", err)
		}
		if readAt.Valid {
			t := readAt.Time
			item.ReadAt = &t
		}
		if len(metaRaw) > 0 {
			var m map[string]string
			if json.Unmarshal(metaRaw, &m) == nil && len(m) > 0 {
				item.Metadata = m
			}
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group watch notifications: %w", err)
	}
	return out, nil
}

func (r *groupWatchRepository) MarkNotificationRead(ctx context.Context, userID, notificationID int64, readAt time.Time) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("group watch repository unavailable")
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE user_group_watch_notifications
		    SET read_at = COALESCE(read_at, $3)
		  WHERE id = $1 AND user_id = $2`,
		notificationID, userID, readAt.UTC())
	if err != nil {
		return false, fmt.Errorf("mark group watch notification read: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected mark group watch notification read: %w", err)
	}
	return n > 0, nil
}

func (r *groupWatchRepository) MarkAllNotificationsRead(ctx context.Context, userID int64, readAt time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("group watch repository unavailable")
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE user_group_watch_notifications
		    SET read_at = $2
		  WHERE user_id = $1 AND read_at IS NULL`,
		userID, readAt.UTC())
	if err != nil {
		return fmt.Errorf("mark all group watch notifications read: %w", err)
	}
	return nil
}

func (r *groupWatchRepository) PruneOldNotifications(ctx context.Context, before time.Time, maxRows int) (int64, error) {
	if r == nil || r.db == nil || maxRows <= 0 {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM user_group_watch_notifications
		  WHERE id IN (
			SELECT id FROM user_group_watch_notifications
			 WHERE created_at < $1
			 ORDER BY created_at ASC
			 LIMIT $2
		  )`,
		before.UTC(), maxRows)
	if err != nil {
		return 0, fmt.Errorf("prune group watch notifications: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected prune group watch notifications: %w", err)
	}
	return n, nil
}
