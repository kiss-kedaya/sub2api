package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Managed upstream channels are sites we resell from. The service owns
// credentials, sessions, announcement sync and the write-through calls
// (API keys, recharge, redeem, subscriptions). Every write path requires an
// explicit confirmation from the caller and is recorded by the audit
// middleware; nothing here touches our own billing, routing or payment code.
const (
	upstreamChannelCipherPrefix    = "enc:upstream:v1:"
	upstreamChannelAnnouncementTTL = 30 * time.Minute
	upstreamChannelMaxPageSize     = 200
	upstreamChannelSyncLeaderKey   = "upstream:channel:sync:leader"
	upstreamChannelSyncLeaderTTL   = 10 * time.Minute
	upstreamChannelDeliveryTTL     = 90 * 24 * time.Hour

	UpstreamChannelStatusActive   = "active"
	UpstreamChannelStatusDisabled = "disabled"
)

// Channel store errors.
var (
	ErrUpstreamChannelNotFound = errors.New("upstream channel not found")
	ErrUpstreamChannelInvalid  = errors.New("upstream channel is invalid")
)

// UpstreamChannel is one managed upstream site.
type UpstreamChannel struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	SiteURL           string     `json:"site_url"`
	Username          string     `json:"username"`
	HasPassword       bool       `json:"has_password"`
	HasSession        bool       `json:"has_session"`
	SessionExpiresAt  *time.Time `json:"session_expires_at,omitempty"`
	SessionError      string     `json:"session_error,omitempty"`
	TurnstileEnabled  bool       `json:"turnstile_enabled"`
	CaptchaProvider   string     `json:"captcha_provider,omitempty"`
	CaptchaEndpoint   string     `json:"captcha_endpoint,omitempty"`
	HasCaptchaKey     bool       `json:"has_captcha_key"`
	ProxyURL          string     `json:"proxy_url,omitempty"`
	SyncAnnouncements bool       `json:"sync_announcements"`
	Status            string     `json:"status"`
	LastLoginAt       *time.Time `json:"last_login_at,omitempty"`
	LastLoginError    string     `json:"last_login_error,omitempty"`
	LastSyncAt        *time.Time `json:"last_sync_at,omitempty"`
	LastSyncError     string     `json:"last_sync_error,omitempty"`
	Tags              []string   `json:"tags"`
	Notes             string     `json:"notes,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`

	// Write-only fields. They are never serialized back to a client.
	Password      string `json:"-"`
	AccessToken   string `json:"-"`
	RefreshToken  string `json:"-"`
	CaptchaAPIKey string `json:"-"`
}

// UpstreamChannelInput is the create/update payload.
type UpstreamChannelInput struct {
	Name              string
	SiteURL           string
	Username          string
	Password          string
	TurnstileEnabled  *bool
	CaptchaProvider   string
	CaptchaAPIKey     string
	CaptchaEndpoint   string
	ProxyURL          string
	SyncAnnouncements *bool
	Status            string
	Tags              []string
	Notes             string
}

// UpstreamChannelFilter is the list query.
type UpstreamChannelFilter struct {
	Search   string
	Status   string
	Page     int
	PageSize int
}

// UpstreamAnnouncementRecord is one synced announcement.
type UpstreamAnnouncementRecord struct {
	ID              int64      `json:"id"`
	ChannelID       int64      `json:"channel_id"`
	ChannelName     string     `json:"channel_name"`
	SourceKey       string     `json:"source_key"`
	Title           string     `json:"title"`
	Content         string     `json:"content"`
	NotifyMode      string     `json:"notify_mode"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	SourceUpdatedAt *time.Time `json:"source_updated_at,omitempty"`
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	PushedAt        *time.Time `json:"pushed_at,omitempty"`
}

// UpstreamAnnouncementSyncResult reports one sync pass.
type UpstreamAnnouncementSyncResult struct {
	ChannelID int64 `json:"channel_id"`
	Fetched   int   `json:"fetched"`
	Added     int   `json:"added"`
	Updated   int   `json:"updated"`
	Seeded    int   `json:"seeded"`
	Pushed    int   `json:"pushed"`
	Unread    int64 `json:"unread"`
}

// UpstreamChannelRepository persists channels, announcements and the shared
// notification delivery log.
type UpstreamChannelRepository interface {
	ListUpstreamChannels(ctx context.Context, filter UpstreamChannelFilter) ([]UpstreamChannel, int64, error)
	GetUpstreamChannel(ctx context.Context, id int64) (*UpstreamChannel, error)
	CreateUpstreamChannel(ctx context.Context, channel *UpstreamChannel) error
	UpdateUpstreamChannel(ctx context.Context, channel *UpstreamChannel) error
	DeleteUpstreamChannel(ctx context.Context, id int64) error
	ListUpstreamChannelsForSync(ctx context.Context) ([]UpstreamChannel, error)

	UpsertUpstreamAnnouncements(ctx context.Context, channelID int64, items []UpstreamAnnouncement) (added, updated int, err error)
	CountUpstreamAnnouncements(ctx context.Context, channelID int64) (int64, error)
	ListUpstreamAnnouncements(ctx context.Context, channelID int64, page, pageSize int) ([]UpstreamAnnouncementRecord, int64, error)
	MarkUpstreamAnnouncementsPushed(ctx context.Context, channelID int64) error

	RecordNotificationDelivery(ctx context.Context, delivery NotificationDelivery) error
	PruneNotificationDeliveries(ctx context.Context, before time.Time) error
}

// UpstreamChannelService serves the operations-center channel page.
type UpstreamChannelService struct {
	repo       UpstreamChannelRepository
	client     *UpstreamChannelClient
	encryptor  SecretEncryptor
	hub        *NotificationHub
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
	mu         sync.Mutex
}

func NewUpstreamChannelService(repo UpstreamChannelRepository, encryptor SecretEncryptor) *UpstreamChannelService {
	return &UpstreamChannelService{
		repo:       repo,
		client:     NewUpstreamChannelClient(),
		encryptor:  encryptor,
		hub:        DefaultNotificationHub(),
		instanceID: uuid.NewString(),
	}
}

// SetNotificationHub overrides the hub (tests).
func (s *UpstreamChannelService) SetNotificationHub(hub *NotificationHub) {
	if s == nil || hub == nil {
		return
	}
	s.hub = hub
}

func (s *UpstreamChannelService) encrypt(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", errors.New("secret encryption is unavailable")
	}
	cipher, err := s.encryptor.Encrypt(value)
	if err != nil || cipher == "" {
		return "", errors.New("secret encryption failed")
	}
	return upstreamChannelCipherPrefix + cipher, nil
}

func (s *UpstreamChannelService) decrypt(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, upstreamChannelCipherPrefix) {
		return trimmed, nil
	}
	if s.encryptor == nil {
		return "", errors.New("secret decryption is unavailable")
	}
	plain, err := s.encryptor.Decrypt(strings.TrimPrefix(trimmed, upstreamChannelCipherPrefix))
	if err != nil {
		return "", errors.New("secret decryption failed")
	}
	return plain, nil
}

func normalizeUpstreamChannelFilter(filter UpstreamChannelFilter) UpstreamChannelFilter {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > upstreamChannelMaxPageSize {
		filter.PageSize = upstreamChannelMaxPageSize
	}
	filter.Search = strings.TrimSpace(filter.Search)
	filter.Status = strings.TrimSpace(filter.Status)
	return filter
}

// List returns channels without any secret material.
func (s *UpstreamChannelService) List(ctx context.Context, filter UpstreamChannelFilter) ([]UpstreamChannel, int64, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("upstream channel service unavailable")
	}
	return s.repo.ListUpstreamChannels(ctx, normalizeUpstreamChannelFilter(filter))
}

// Get returns one channel without secret material.
func (s *UpstreamChannelService) Get(ctx context.Context, id int64) (*UpstreamChannel, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("upstream channel service unavailable")
	}
	if id <= 0 {
		return nil, errors.New("channel id is invalid")
	}
	return s.repo.GetUpstreamChannel(ctx, id)
}

// Create registers one channel. The password is encrypted before it is stored.
func (s *UpstreamChannelService) Create(ctx context.Context, input UpstreamChannelInput) (*UpstreamChannel, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("upstream channel service unavailable")
	}
	channel, err := s.applyInput(nil, input, true)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreateUpstreamChannel(ctx, channel); err != nil {
		return nil, err
	}
	return s.repo.GetUpstreamChannel(ctx, channel.ID)
}

// Update edits one channel. An empty password keeps the stored one.
func (s *UpstreamChannelService) Update(ctx context.Context, id int64, input UpstreamChannelInput) (*UpstreamChannel, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("upstream channel service unavailable")
	}
	if id <= 0 {
		return nil, errors.New("channel id is invalid")
	}
	existing, err := s.repo.GetUpstreamChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	channel, err := s.applyInput(existing, input, false)
	if err != nil {
		return nil, err
	}
	channel.ID = id
	if err := s.repo.UpdateUpstreamChannel(ctx, channel); err != nil {
		return nil, err
	}
	return s.repo.GetUpstreamChannel(ctx, id)
}

// Delete removes one channel and its announcements.
func (s *UpstreamChannelService) Delete(ctx context.Context, id int64) error {
	if s == nil || s.repo == nil {
		return errors.New("upstream channel service unavailable")
	}
	if id <= 0 {
		return errors.New("channel id is invalid")
	}
	return s.repo.DeleteUpstreamChannel(ctx, id)
}

func (s *UpstreamChannelService) applyInput(existing *UpstreamChannel, input UpstreamChannelInput, creating bool) (*UpstreamChannel, error) {
	channel := &UpstreamChannel{}
	if existing != nil {
		copy := *existing
		channel = &copy
	}
	if name := strings.TrimSpace(input.Name); name != "" || creating {
		channel.Name = name
	}
	site := strings.TrimRight(strings.TrimSpace(input.SiteURL), "/")
	if site != "" || creating {
		if !strings.HasPrefix(site, "http://") && !strings.HasPrefix(site, "https://") {
			if site != "" {
				return nil, errors.New("site url must start with http:// or https://")
			}
		}
		channel.SiteURL = site
	}
	if username := strings.TrimSpace(input.Username); username != "" || creating {
		channel.Username = username
	}
	if creating {
		channel.Status = UpstreamChannelStatusActive
		channel.SyncAnnouncements = true
	}
	if channel.Name == "" {
		return nil, errors.New("channel name is required")
	}
	if channel.SiteURL == "" {
		return nil, errors.New("site url is required")
	}
	if channel.Username == "" {
		return nil, errors.New("login username is required")
	}
	if password := input.Password; strings.TrimSpace(password) != "" {
		if channel.Password, _ = s.encrypt(password); channel.Password == "" {
			return nil, errors.New("password encryption failed")
		}
	} else if creating {
		return nil, errors.New("login password is required")
	}
	if input.TurnstileEnabled != nil {
		channel.TurnstileEnabled = *input.TurnstileEnabled
	}
	if provider := strings.TrimSpace(input.CaptchaProvider); provider != "" {
		channel.CaptchaProvider = strings.ToLower(provider)
	}
	if key := strings.TrimSpace(input.CaptchaAPIKey); key != "" {
		if channel.CaptchaAPIKey, _ = s.encrypt(key); channel.CaptchaAPIKey == "" {
			return nil, errors.New("captcha key encryption failed")
		}
	}
	if endpoint := strings.TrimSpace(input.CaptchaEndpoint); endpoint != "" {
		channel.CaptchaEndpoint = endpoint
	}
	if proxy := strings.TrimSpace(input.ProxyURL); proxy != "" {
		channel.ProxyURL = proxy
	}
	if input.SyncAnnouncements != nil {
		channel.SyncAnnouncements = *input.SyncAnnouncements
	}
	if status := strings.TrimSpace(input.Status); status != "" {
		if status != UpstreamChannelStatusActive && status != UpstreamChannelStatusDisabled {
			return nil, errors.New("status must be active or disabled")
		}
		channel.Status = status
	}
	if input.Tags != nil {
		channel.Tags = safeUpstreamStrings(input.Tags)
	}
	if input.Notes != "" || creating {
		channel.Notes = strings.TrimSpace(input.Notes)
	}
	return channel, nil
}

type upstreamChannelCredentials struct {
	password     string
	captchaKey   string
	accessToken  string
	refreshToken string
	expiresAt    time.Time
}

func (s *UpstreamChannelService) credentials(channel *UpstreamChannel) (upstreamChannelCredentials, error) {
	var creds upstreamChannelCredentials
	if channel == nil {
		return creds, errors.New("channel is required")
	}
	var err error
	if creds.password, err = s.decrypt(channel.Password); err != nil {
		return creds, err
	}
	if creds.captchaKey, err = s.decrypt(channel.CaptchaAPIKey); err != nil {
		return creds, err
	}
	if creds.accessToken, err = s.decrypt(channel.AccessToken); err != nil {
		return creds, err
	}
	if creds.refreshToken, err = s.decrypt(channel.RefreshToken); err != nil {
		return creds, err
	}
	if channel.SessionExpiresAt != nil {
		creds.expiresAt = *channel.SessionExpiresAt
	}
	return creds, nil
}

// Login authenticates one channel and stores the encrypted session.
func (s *UpstreamChannelService) Login(ctx context.Context, id int64) (*UpstreamChannel, error) {
	channel, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if channel.Status != UpstreamChannelStatusActive {
		return nil, errors.New("channel is disabled")
	}
	creds, err := s.credentials(channel)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(creds.password) == "" {
		return nil, errors.New("channel has no stored password")
	}
	turnstileToken := ""
	needsTurnstile, siteKey, settingsErr := s.client.PublicSettings(ctx, channel.SiteURL, channel.ProxyURL)
	if settingsErr == nil && needsTurnstile {
		if strings.TrimSpace(creds.captchaKey) == "" {
			return nil, errors.New("upstream requires a turnstile token but the channel has no captcha key")
		}
		solver, solverErr := NewUpstreamCaptchaSolver(UpstreamCaptchaConfig{
			Provider: channel.CaptchaProvider,
			APIKey:   creds.captchaKey,
			Endpoint: channel.CaptchaEndpoint,
			ProxyURL: channel.ProxyURL,
		})
		if solverErr != nil {
			return nil, solverErr
		}
		loginURL := strings.TrimRight(channel.SiteURL, "/") + "/login"
		turnstileToken, err = solver.SolveTurnstile(ctx, siteKey, loginURL)
		if err != nil {
			s.recordLoginFailure(ctx, id, err)
			return nil, err
		}
	}
	session, err := s.client.Login(ctx, channel.SiteURL, channel.Username, creds.password, turnstileToken, channel.ProxyURL)
	if err != nil {
		s.recordLoginFailure(ctx, id, err)
		return nil, err
	}
	if err := s.storeSession(ctx, channel, session); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *UpstreamChannelService) recordLoginFailure(ctx context.Context, id int64, err error) {
	channel, getErr := s.Get(ctx, id)
	if getErr != nil || channel == nil {
		return
	}
	channel.LastLoginError = truncateUpstreamChannelText(err.Error())
	if updateErr := s.repo.UpdateUpstreamChannel(ctx, channel); updateErr != nil {
		slog.Warn("upstream_channel_login_record_failed", "channel", id, "error", updateErr)
	}
}

func (s *UpstreamChannelService) storeSession(ctx context.Context, channel *UpstreamChannel, session *UpstreamChannelSession) error {
	if session == nil {
		return errors.New("empty upstream session")
	}
	access, err := s.encrypt(session.AccessToken)
	if err != nil {
		return err
	}
	refresh, err := s.encrypt(session.RefreshToken)
	if err != nil {
		return err
	}
	channel.AccessToken = access
	channel.RefreshToken = refresh
	expires := session.ExpiresAt.UTC()
	channel.SessionExpiresAt = &expires
	channel.LastLoginAt = timePtrUTC(time.Now())
	channel.LastLoginError = ""
	channel.SessionError = ""
	if channel.ID <= 0 {
		return s.repo.CreateUpstreamChannel(ctx, channel)
	}
	return s.repo.UpdateUpstreamChannel(ctx, channel)
}

// session returns a usable session, refreshing or logging in as needed.
func (s *UpstreamChannelService) session(ctx context.Context, channel *UpstreamChannel) (*UpstreamChannelSession, error) {
	if channel == nil {
		return nil, errors.New("channel is required")
	}
	creds, err := s.credentials(channel)
	if err != nil {
		return nil, err
	}
	current := &UpstreamChannelSession{
		AccessToken:  creds.accessToken,
		RefreshToken: creds.refreshToken,
		ExpiresAt:    creds.expiresAt,
	}
	if !current.Expired(time.Now().UTC()) {
		return current, nil
	}
	if strings.TrimSpace(current.RefreshToken) == "" {
		if _, err := s.Login(ctx, channel.ID); err != nil {
			return nil, err
		}
		reloaded, err := s.Get(ctx, channel.ID)
		if err != nil {
			return nil, err
		}
		creds, err = s.credentials(reloaded)
		if err != nil {
			return nil, err
		}
		*channel = *reloaded
		return &UpstreamChannelSession{AccessToken: creds.accessToken, RefreshToken: creds.refreshToken, ExpiresAt: creds.expiresAt}, nil
	}
	refreshed, err := s.client.RefreshSession(ctx, channel.SiteURL, channel.ProxyURL, current)
	if err == nil {
		if storeErr := s.storeSession(ctx, channel, refreshed); storeErr == nil {
			return refreshed, nil
		}
	}
	if _, loginErr := s.Login(ctx, channel.ID); loginErr != nil {
		return nil, loginErr
	}
	reloaded, err := s.Get(ctx, channel.ID)
	if err != nil {
		return nil, err
	}
	creds, err = s.credentials(reloaded)
	if err != nil {
		return nil, err
	}
	*channel = *reloaded
	return &UpstreamChannelSession{AccessToken: creds.accessToken, RefreshToken: creds.refreshToken, ExpiresAt: creds.expiresAt}, nil
}

// Balance reads the upstream wallet balance.
func (s *UpstreamChannelService) Balance(ctx context.Context, id int64) (float64, error) {
	channel, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	session, err := s.session(ctx, channel)
	if err != nil {
		return 0, err
	}
	return s.client.Balance(ctx, channel.SiteURL, channel.ProxyURL, session)
}

// ListAnnouncements pages the synced announcements.
func (s *UpstreamChannelService) ListAnnouncements(ctx context.Context, channelID int64, page, pageSize int) ([]UpstreamAnnouncementRecord, int64, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("upstream channel service unavailable")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > upstreamChannelMaxPageSize {
		pageSize = upstreamChannelMaxPageSize
	}
	return s.repo.ListUpstreamAnnouncements(ctx, channelID, page, pageSize)
}

// SyncAnnouncements pulls announcements and pushes only the ones that are new
// after the first baseline sync.
func (s *UpstreamChannelService) SyncAnnouncements(ctx context.Context, id int64) (UpstreamAnnouncementSyncResult, error) {
	result := UpstreamAnnouncementSyncResult{ChannelID: id}
	channel, err := s.Get(ctx, id)
	if err != nil {
		return result, err
	}
	session, err := s.session(ctx, channel)
	if err != nil {
		return result, err
	}
	items, err := s.client.Announcements(ctx, channel.SiteURL, channel.ProxyURL, session)
	if err != nil {
		channel.LastSyncError = truncateUpstreamChannelText(err.Error())
		if updateErr := s.repo.UpdateUpstreamChannel(ctx, channel); updateErr != nil {
			slog.Warn("upstream_channel_sync_record_failed", "channel", id, "error", updateErr)
		}
		return result, err
	}
	result.Fetched = len(items)
	baseline, err := s.repo.CountUpstreamAnnouncements(ctx, id)
	if err != nil {
		return result, err
	}
	added, updated, err := s.repo.UpsertUpstreamAnnouncements(ctx, id, items)
	if err != nil {
		return result, err
	}
	result.Added, result.Updated = added, updated
	if baseline == 0 && added > 0 {
		// First sync: record the baseline, do not push a backlog to the phone.
		result.Seeded = added
		if err := s.repo.MarkUpstreamAnnouncementsPushed(ctx, id); err != nil {
			slog.Warn("upstream_channel_announcement_seed_failed", "channel", id, "error", err)
		}
	} else if added > 0 {
		result.Pushed = s.publishAnnouncements(ctx, channel, items, false)
	}
	channel.LastSyncAt = timePtrUTC(time.Now())
	channel.LastSyncError = ""
	if err := s.repo.UpdateUpstreamChannel(ctx, channel); err != nil {
		slog.Warn("upstream_channel_sync_state_failed", "channel", id, "error", err)
	}
	return result, nil
}

func (s *UpstreamChannelService) publishAnnouncements(ctx context.Context, channel *UpstreamChannel, items []UpstreamAnnouncement, all bool) int {
	if s.hub == nil || len(items) == 0 {
		return 0
	}
	pushed := 0
	for _, item := range items {
		if !all && item.PublishedAt != nil && item.PublishedAt.Before(time.Now().UTC().Add(-7*24*time.Hour)) {
			continue
		}
		body := truncateUpstreamChannelText(item.Content)
		if len(body) > 600 {
			body = body[:600] + "..."
		}
		result := s.hub.Publish(ctx, NotificationMessage{
			EventKind: "upstream_announcement",
			DedupeKey: fmt.Sprintf("upstream:announcement:%d:%s", channel.ID, item.SourceKey),
			Title:     fmt.Sprintf("[%s] %s", channel.Name, item.Title),
			Body:      body,
			Level:     NotificationLevelInfo,
			Group:     "upstream",
			Target:    NotificationTargetAdmins,
		})
		if !result.Suppressed && len(result.Delivered) > 0 {
			pushed++
		}
	}
	return pushed
}

// Start runs the announcement sync loop. Leadership makes several gateway
// instances share one pass.
func (s *UpstreamChannelService) Start(ctx context.Context) {
	if s == nil || s.repo == nil {
		return
	}
	go func() {
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.syncAll(ctx)
		}
		ticker := time.NewTicker(upstreamChannelAnnouncementTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.syncAll(ctx)
			}
		}
	}()
}

func (s *UpstreamChannelService) syncAll(ctx context.Context) {
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, upstreamChannelSyncLeaderKey, s.instanceID, upstreamChannelSyncLeaderTTL)
	if !ok {
		return
	}
	defer release()
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	channels, err := s.repo.ListUpstreamChannelsForSync(runCtx)
	if err != nil {
		slog.Warn("upstream_channel_sync_list_failed", "error", err)
		return
	}
	for _, channel := range channels {
		if !channel.SyncAnnouncements || channel.Status != UpstreamChannelStatusActive {
			continue
		}
		if _, err := s.SyncAnnouncements(runCtx, channel.ID); err != nil {
			slog.Warn("upstream_channel_sync_failed", "channel", channel.ID, "error", err)
		}
	}
}

func timePtrUTC(value time.Time) *time.Time {
	utc := value.UTC()
	return &utc
}

func (s *UpstreamChannelService) channelSession(ctx context.Context, id int64) (*UpstreamChannel, *UpstreamChannelSession, error) {
	channel, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if channel.Status != UpstreamChannelStatusActive {
		return nil, nil, errors.New("channel is disabled")
	}
	session, err := s.session(ctx, channel)
	if err != nil {
		return nil, nil, err
	}
	return channel, session, nil
}

// APIKeys pages the upstream keys.
func (s *UpstreamChannelService) APIKeys(ctx context.Context, id int64, query UpstreamAPIKeyQuery) (*UpstreamAPIKeyPage, error) {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.ListAPIKeys(ctx, channel.SiteURL, channel.ProxyURL, session, query)
}

// APIKeyGroups lists bindable groups.
func (s *UpstreamChannelService) APIKeyGroups(ctx context.Context, id int64) ([]UpstreamAPIKeyGroup, error) {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.APIKeyGroups(ctx, channel.SiteURL, channel.ProxyURL, session)
}

// CreateAPIKey creates one key upstream.
func (s *UpstreamChannelService) CreateAPIKey(ctx context.Context, id int64, req UpstreamAPIKeyCreateRequest) (*UpstreamAPIKey, error) {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.CreateAPIKey(ctx, channel.SiteURL, channel.ProxyURL, session, req)
}

// UpdateAPIKey edits one key upstream.
func (s *UpstreamChannelService) UpdateAPIKey(ctx context.Context, id, keyID int64, req UpstreamAPIKeyUpdateRequest) (*UpstreamAPIKey, error) {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.UpdateAPIKey(ctx, channel.SiteURL, channel.ProxyURL, session, keyID, req)
}

// DeleteAPIKey removes one key upstream.
func (s *UpstreamChannelService) DeleteAPIKey(ctx context.Context, id, keyID int64) error {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return err
	}
	return s.client.DeleteAPIKey(ctx, channel.SiteURL, channel.ProxyURL, session, keyID)
}

// RevealAPIKey reads the full key value. The caller must audit and must not
// cache it.
func (s *UpstreamChannelService) RevealAPIKey(ctx context.Context, id, keyID int64) (string, error) {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return "", err
	}
	return s.client.RevealAPIKey(ctx, channel.SiteURL, channel.ProxyURL, session, keyID)
}

// RechargeInfo reads the recharge form.
func (s *UpstreamChannelService) RechargeInfo(ctx context.Context, id int64) (*UpstreamRechargeInfo, error) {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.RechargeInfo(ctx, channel.SiteURL, channel.ProxyURL, session)
}

// CreateRecharge creates a real upstream balance order. confirm must be true.
func (s *UpstreamChannelService) CreateRecharge(ctx context.Context, id int64, req UpstreamRechargeRequest, confirm bool) (*UpstreamPaymentLaunch, error) {
	if !confirm {
		return nil, errors.New("creating a recharge order spends real money; pass confirm=true")
	}
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.CreateRecharge(ctx, channel.SiteURL, channel.ProxyURL, session, req)
}

// Redeem applies one redeem code upstream.
func (s *UpstreamChannelService) Redeem(ctx context.Context, id int64, code string, confirm bool) (*UpstreamRedeemResult, error) {
	if !confirm {
		return nil, errors.New("redeeming a code changes the upstream account; pass confirm=true")
	}
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.Redeem(ctx, channel.SiteURL, channel.ProxyURL, session, code)
}

// SubscriptionInfo reads the plans on sale.
func (s *UpstreamChannelService) SubscriptionInfo(ctx context.Context, id int64) (*UpstreamSubscriptionInfo, error) {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.SubscriptionInfo(ctx, channel.SiteURL, channel.ProxyURL, session)
}

// SubscriptionUsage lists active subscriptions.
func (s *UpstreamChannelService) SubscriptionUsage(ctx context.Context, id int64) ([]UpstreamSubscriptionUsage, error) {
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.SubscriptionUsage(ctx, channel.SiteURL, channel.ProxyURL, session)
}

// CreateSubscription buys a subscription upstream. confirm must be true.
func (s *UpstreamChannelService) CreateSubscription(ctx context.Context, id int64, req UpstreamSubscriptionRequest, confirm bool) (*UpstreamPaymentLaunch, error) {
	if !confirm {
		return nil, errors.New("buying a subscription spends real money; pass confirm=true")
	}
	channel, session, err := s.channelSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.client.CreateSubscription(ctx, channel.SiteURL, channel.ProxyURL, session, req)
}

// SetLeaderLock injects cross-instance coordination for the sync loop.
func (s *UpstreamChannelService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}
