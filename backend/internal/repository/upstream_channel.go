package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type upstreamChannelRepository struct{ db *sql.DB }

// NewUpstreamChannelRepository builds the managed upstream channel store.
func NewUpstreamChannelRepository(db *sql.DB) service.UpstreamChannelRepository {
	return &upstreamChannelRepository{db: db}
}

const upstreamChannelColumns = `
	id, name, site_url, username, password_cipher, access_token_cipher,
	refresh_token_cipher, session_expires_at, session_error, turnstile_enabled,
	captcha_provider, captcha_api_key_cipher, captcha_endpoint, proxy_url,
	sync_announcements, status, last_login_at, last_login_error, last_sync_at,
	last_sync_error, tags, notes, created_at, updated_at`

func scanUpstreamChannel(row interface {
	Scan(dest ...any) error
}) (*service.UpstreamChannel, error) {
	var channel service.UpstreamChannel
	var sessionExpires, lastLogin, lastSync sql.NullTime
	var tags string
	var password, access, refresh, captchaKey string
	if err := row.Scan(
		&channel.ID, &channel.Name, &channel.SiteURL, &channel.Username, &password,
		&access, &refresh, &sessionExpires, &channel.SessionError, &channel.TurnstileEnabled,
		&channel.CaptchaProvider, &captchaKey, &channel.CaptchaEndpoint, &channel.ProxyURL,
		&channel.SyncAnnouncements, &channel.Status, &lastLogin, &channel.LastLoginError,
		&lastSync, &channel.LastSyncError, &tags, &channel.Notes,
		&channel.CreatedAt, &channel.UpdatedAt,
	); err != nil {
		return nil, err
	}
	channel.Password = password
	channel.AccessToken = access
	channel.RefreshToken = refresh
	channel.CaptchaAPIKey = captchaKey
	channel.HasPassword = strings.TrimSpace(password) != ""
	channel.HasSession = strings.TrimSpace(access) != ""
	channel.HasCaptchaKey = strings.TrimSpace(captchaKey) != ""
	if sessionExpires.Valid {
		value := sessionExpires.Time
		channel.SessionExpiresAt = &value
	}
	if lastLogin.Valid {
		value := lastLogin.Time
		channel.LastLoginAt = &value
	}
	if lastSync.Valid {
		value := lastSync.Time
		channel.LastSyncAt = &value
	}
	channel.Tags = splitUpstreamTags(tags)
	return &channel, nil
}

func splitUpstreamTags(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}
	}
	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (r *upstreamChannelRepository) ListUpstreamChannels(ctx context.Context, filter service.UpstreamChannelFilter) ([]service.UpstreamChannel, int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+upstreamChannelColumns+`
		FROM upstream_channels
		WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR site_url ILIKE '%' || $1 || '%' OR username ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR status = $2)
		ORDER BY id DESC
		LIMIT $3 OFFSET $4`,
		filter.Search, filter.Status, filter.PageSize, (filter.Page-1)*filter.PageSize)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.UpstreamChannel, 0, filter.PageSize)
	for rows.Next() {
		channel, scanErr := scanUpstreamChannel(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, *channel)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM upstream_channels
		WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR site_url ILIKE '%' || $1 || '%' OR username ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR status = $2)`, filter.Search, filter.Status).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *upstreamChannelRepository) GetUpstreamChannel(ctx context.Context, id int64) (*service.UpstreamChannel, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+upstreamChannelColumns+` FROM upstream_channels WHERE id = $1`, id)
	channel, err := scanUpstreamChannel(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrUpstreamChannelNotFound
		}
		return nil, err
	}
	return channel, nil
}

func (r *upstreamChannelRepository) CreateUpstreamChannel(ctx context.Context, channel *service.UpstreamChannel) error {
	if channel == nil {
		return service.ErrUpstreamChannelInvalid
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO upstream_channels (
			name, site_url, username, password_cipher, access_token_cipher,
			refresh_token_cipher, session_expires_at, session_error, turnstile_enabled,
			captcha_provider, captcha_api_key_cipher, captcha_endpoint, proxy_url,
			sync_announcements, status, last_login_at, last_login_error, last_sync_at,
			last_sync_error, tags, notes
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		RETURNING id`,
		channel.Name, channel.SiteURL, channel.Username, channel.Password, channel.AccessToken,
		channel.RefreshToken, channel.SessionExpiresAt, channel.SessionError, channel.TurnstileEnabled,
		channel.CaptchaProvider, channel.CaptchaAPIKey, channel.CaptchaEndpoint, channel.ProxyURL,
		channel.SyncAnnouncements, channel.Status, channel.LastLoginAt, channel.LastLoginError,
		channel.LastSyncAt, channel.LastSyncError, strings.Join(channel.Tags, ","), channel.Notes,
	)
	return row.Scan(&channel.ID)
}

func (r *upstreamChannelRepository) UpdateUpstreamChannel(ctx context.Context, channel *service.UpstreamChannel) error {
	if channel == nil || channel.ID <= 0 {
		return service.ErrUpstreamChannelInvalid
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE upstream_channels SET
			name = $2, site_url = $3, username = $4, password_cipher = $5,
			access_token_cipher = $6, refresh_token_cipher = $7, session_expires_at = $8,
			session_error = $9, turnstile_enabled = $10, captcha_provider = $11,
			captcha_api_key_cipher = $12, captcha_endpoint = $13, proxy_url = $14,
			sync_announcements = $15, status = $16, last_login_at = $17,
			last_login_error = $18, last_sync_at = $19, last_sync_error = $20,
			tags = $21, notes = $22, updated_at = NOW()
		WHERE id = $1`,
		channel.ID, channel.Name, channel.SiteURL, channel.Username, channel.Password,
		channel.AccessToken, channel.RefreshToken, channel.SessionExpiresAt, channel.SessionError,
		channel.TurnstileEnabled, channel.CaptchaProvider, channel.CaptchaAPIKey,
		channel.CaptchaEndpoint, channel.ProxyURL, channel.SyncAnnouncements, channel.Status,
		channel.LastLoginAt, channel.LastLoginError, channel.LastSyncAt, channel.LastSyncError,
		strings.Join(channel.Tags, ","), channel.Notes,
	)
	return err
}

func (r *upstreamChannelRepository) DeleteUpstreamChannel(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM upstream_channels WHERE id = $1`, id)
	return err
}

func (r *upstreamChannelRepository) ListUpstreamChannelsForSync(ctx context.Context) ([]service.UpstreamChannel, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+upstreamChannelColumns+`
		FROM upstream_channels
		WHERE status = 'active' AND sync_announcements = TRUE
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.UpstreamChannel, 0, 16)
	for rows.Next() {
		channel, scanErr := scanUpstreamChannel(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *channel)
	}
	return out, rows.Err()
}

func (r *upstreamChannelRepository) UpsertUpstreamAnnouncements(ctx context.Context, channelID int64, items []service.UpstreamAnnouncement) (int, int, error) {
	added, updated := 0, 0
	for _, item := range items {
		var inserted bool
		err := r.db.QueryRowContext(ctx, `
			INSERT INTO upstream_channel_announcements (
				channel_id, source_key, title, content, notify_mode, published_at, source_updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (channel_id, source_key) DO UPDATE SET
				title = EXCLUDED.title,
				content = EXCLUDED.content,
				notify_mode = EXCLUDED.notify_mode,
				published_at = EXCLUDED.published_at,
				source_updated_at = EXCLUDED.source_updated_at,
				updated_at = NOW()
			RETURNING (xmax = 0)`,
			channelID, item.SourceKey, item.Title, item.Content, item.NotifyMode,
			item.PublishedAt, item.SourceUpdatedAt,
		).Scan(&inserted)
		if err != nil {
			return added, updated, err
		}
		if inserted {
			added++
		} else {
			updated++
		}
	}
	return added, updated, nil
}

func (r *upstreamChannelRepository) CountUpstreamAnnouncements(ctx context.Context, channelID int64) (int64, error) {
	var total int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM upstream_channel_announcements WHERE channel_id = $1`, channelID).Scan(&total)
	return total, err
}

func (r *upstreamChannelRepository) ListUpstreamAnnouncements(ctx context.Context, channelID int64, page, pageSize int) ([]service.UpstreamAnnouncementRecord, int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.channel_id, COALESCE(c.name, ''), a.source_key, a.title, a.content,
		       a.notify_mode, a.published_at, a.source_updated_at, a.first_seen_at, a.pushed_at
		FROM upstream_channel_announcements a
		LEFT JOIN upstream_channels c ON c.id = a.channel_id
		WHERE ($1 = 0 OR a.channel_id = $1)
		ORDER BY COALESCE(a.published_at, a.first_seen_at) DESC
		LIMIT $2 OFFSET $3`, channelID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.UpstreamAnnouncementRecord, 0, pageSize)
	for rows.Next() {
		var record service.UpstreamAnnouncementRecord
		var published, sourceUpdated, pushed sql.NullTime
		if scanErr := rows.Scan(
			&record.ID, &record.ChannelID, &record.ChannelName, &record.SourceKey,
			&record.Title, &record.Content, &record.NotifyMode, &published,
			&sourceUpdated, &record.FirstSeenAt, &pushed,
		); scanErr != nil {
			return nil, 0, scanErr
		}
		if published.Valid {
			value := published.Time
			record.PublishedAt = &value
		}
		if sourceUpdated.Valid {
			value := sourceUpdated.Time
			record.SourceUpdatedAt = &value
		}
		if pushed.Valid {
			value := pushed.Time
			record.PushedAt = &value
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM upstream_channel_announcements
		WHERE ($1 = 0 OR channel_id = $1)`, channelID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (r *upstreamChannelRepository) MarkUpstreamAnnouncementsPushed(ctx context.Context, channelID int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE upstream_channel_announcements SET pushed_at = NOW()
		WHERE channel_id = $1 AND pushed_at IS NULL`, channelID)
	return err
}

func (r *upstreamChannelRepository) RecordNotificationDelivery(ctx context.Context, delivery service.NotificationDelivery) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_deliveries (channel, event_kind, dedupe_key, target, status, error, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		delivery.Channel, delivery.EventKind, delivery.DedupeKey, delivery.Target,
		delivery.Status, delivery.Error, delivery.CreatedAt.UTC())
	return err
}

func (r *upstreamChannelRepository) PruneNotificationDeliveries(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM notification_deliveries WHERE created_at < $1`, before.UTC())
	return err
}
