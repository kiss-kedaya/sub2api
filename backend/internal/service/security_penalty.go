// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeySecurityPenalty = "security_penalty_config"

// Penalties restrict gateway admission only. They never change users.status.
type SecurityPenaltyConfig struct {
	Enabled            bool    `json:"enabled"`
	ObserveOnly        bool    `json:"observe_only"`
	Policy             string  `json:"policy"`
	Threshold          int     `json:"threshold"`
	WindowHours        int     `json:"window_hours"`
	DurationHours      int     `json:"duration_hours"`
	TargetGroupID      *int64  `json:"target_group_id"`
	AllGroups          bool    `json:"all_groups"`
	GroupIDs           []int64 `json:"group_ids"`
	ExemptUserIDs      []int64 `json:"exempt_user_ids"`
	Version            int64   `json:"version"`
	RiskControlEnabled bool    `json:"risk_control_enabled"`
	trustedUserIDs     []int64
}

func DefaultSecurityPenaltyConfig() SecurityPenaltyConfig {
	return SecurityPenaltyConfig{ObserveOnly: true, Policy: "session_block", Threshold: 3, WindowHours: 720, DurationHours: 24, AllGroups: true, GroupIDs: []int64{}, ExemptUserIDs: []int64{}, Version: 1}
}

func validateSecurityPenaltyConfig(c SecurityPenaltyConfig) error {
	if c.Policy != "session_block" && c.Policy != "user_block" && c.Policy != "session_route_then_block" {
		return infraerrors.BadRequest("security_penalty_invalid_policy", "处罚策略无效")
	}
	if c.Threshold < 1 || c.Threshold > 1000 || c.WindowHours < 1 || c.WindowHours > 87600 || c.DurationHours < 1 || c.DurationHours > 87600 {
		return infraerrors.BadRequest("security_penalty_invalid_limits", "阈值须为 1–1000，时间须为 1–87600 小时")
	}
	if c.Enabled && c.Policy == "session_route_then_block" && (c.TargetGroupID == nil || *c.TargetGroupID <= 0) {
		return infraerrors.BadRequest("security_penalty_target_required", "两级处罚需要指定受限分组")
	}
	if !c.AllGroups && len(c.GroupIDs) == 0 {
		return infraerrors.BadRequest("security_penalty_groups_required", "请选择适用分组")
	}
	if len(c.GroupIDs) > 1000 || len(c.ExemptUserIDs) > 1000 {
		return infraerrors.BadRequest("security_penalty_invalid_scope", "分组或豁免用户数量过多")
	}
	for _, ids := range [][]int64{c.GroupIDs, c.ExemptUserIDs} {
		for _, id := range ids {
			if id <= 0 {
				return infraerrors.BadRequest("security_penalty_invalid_scope", "ID 必须为正整数")
			}
		}
	}
	return nil
}

type SecurityPenaltyState struct {
	UserID          int64      `json:"user_id"`
	Scope           string     `json:"scope"`
	SubjectKey      string     `json:"subject_key"`
	Policy          string     `json:"policy"`
	Count           int        `json:"count"`
	WindowStartedAt time.Time  `json:"window_started_at"`
	BlockedUntil    *time.Time `json:"blocked_until"`
	RouteUntil      *time.Time `json:"route_until"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Action          string     `json:"action"`
	Reason          string     `json:"reason"`
}

type SecurityPenaltyViolation struct {
	UserID     int64
	APIKeyID   int64
	GroupID    *int64
	RequestID  string
	SessionKey string
	Source     string
	Reason     string
	FullPrompt string
	Model      string
	Endpoint   string
}

// Hash only an explicit stable client identity. Scope is the authenticated user,
// never the API key, so changing keys cannot reset the same conversation.
func SecurityPenaltySessionKey(userID int64, identity string) string {
	identity = strings.TrimSpace(identity)
	if userID <= 0 || identity == "" || len(identity) > 4096 {
		return ""
	}
	hash := sha256.Sum256([]byte(strconv.FormatInt(userID, 10) + "\x00" + identity))
	return hex.EncodeToString(hash[:])
}

func (s *SecurityPenaltyService) OwnsLegacyEnforcement(ctx context.Context, userID int64, groupID *int64) bool {
	if s == nil {
		return false
	}
	cfg, _ := s.RuntimeConfig()
	return !cfg.ObserveOnly && cfg.Applies(userID) && cfg.IncludesGroup(groupID)
}

type SecurityPenaltyAdmission struct {
	Blocked       bool
	TargetGroupID *int64
}

type SecurityPenaltyService struct {
	db        *sql.DB
	settings  SettingRepository
	mu        sync.Mutex
	cached    SecurityPenaltyConfig
	expires   time.Time
	lifecycle sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func NewSecurityPenaltyService(db *sql.DB, settings SettingRepository) *SecurityPenaltyService {
	return &SecurityPenaltyService{db: db, settings: settings, cached: DefaultSecurityPenaltyConfig()}
}

// RuntimeConfig performs no I/O on the gateway path. Disabled snapshots keep
// existing behavior during unrelated database outages; enabled enforcement
// stops admitting requests if the shared policy cannot be refreshed in time.
func (s *SecurityPenaltyService) RuntimeConfig() (SecurityPenaltyConfig, error) {
	if s == nil {
		return DefaultSecurityPenaltyConfig(), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.cached
	if cfg.Version == 0 {
		cfg = DefaultSecurityPenaltyConfig()
	}
	if cfg.Enabled && cfg.RiskControlEnabled && !cfg.ObserveOnly && time.Now().After(s.expires.Add(15*time.Second)) {
		return cfg, errors.New("security penalty policy snapshot is stale")
	}
	return cfg, nil
}

func (s *SecurityPenaltyService) Start() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	refresh := func() error {
		bounded, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		_, err := s.loadConfig(bounded)
		return err
	}
	initialErr := refresh()
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = refresh()
				bounded, stop := context.WithTimeout(ctx, 2*time.Second)
				_, _ = s.db.ExecContext(bounded, `DELETE FROM security_penalty_request_context WHERE (user_id,request_id) IN (SELECT user_id,request_id FROM security_penalty_request_context WHERE expires_at<NOW() ORDER BY expires_at LIMIT 1000 FOR UPDATE SKIP LOCKED)`)
				stop()
			}
		}
	}()
	return initialErr
}
func (s *SecurityPenaltyService) Stop() {
	if s == nil {
		return
	}
	s.lifecycle.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.lifecycle.Unlock()
	s.wg.Wait()
}

func (s *SecurityPenaltyService) GetConfig(ctx context.Context) (SecurityPenaltyConfig, error) {
	if s == nil {
		return DefaultSecurityPenaltyConfig(), nil
	}
	s.mu.Lock()
	if time.Now().Before(s.expires) {
		cfg := s.cached
		s.mu.Unlock()
		return cfg, nil
	}
	s.mu.Unlock()
	return s.loadConfig(ctx)
}

func (s *SecurityPenaltyService) loadConfig(ctx context.Context) (SecurityPenaltyConfig, error) {
	values, err := s.settings.GetMultiple(ctx, []string{SettingKeySecurityPenalty, SettingKeyRiskControlEnabled})
	if err != nil {
		return SecurityPenaltyConfig{}, err
	}
	cfg := DefaultSecurityPenaltyConfig()
	if raw := values[SettingKeySecurityPenalty]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return SecurityPenaltyConfig{}, errors.New("invalid security penalty config")
		}
	}
	if err := validateSecurityPenaltyConfig(cfg); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	cfg.RiskControlEnabled = values[SettingKeyRiskControlEnabled] == "true"
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.Version >= s.cached.Version {
		s.cached = cfg
		s.expires = time.Now().Add(time.Second)
	}
	return s.cached, nil
}

func (s *SecurityPenaltyService) SaveConfig(ctx context.Context, cfg SecurityPenaltyConfig) (SecurityPenaltyConfig, error) {
	if err := validateSecurityPenaltyConfig(cfg); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	if cfg.Enabled && cfg.Policy == "session_route_then_block" && cfg.TargetGroupID != nil {
		var active bool
		if err := s.db.QueryRowContext(ctx, "SELECT status='active' AND deleted_at IS NULL FROM groups WHERE id=$1", *cfg.TargetGroupID).Scan(&active); err != nil || !active {
			return SecurityPenaltyConfig{}, infraerrors.BadRequest("security_penalty_invalid_target", "受限分组不存在或未启用")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SecurityPenaltyConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=$1 FOR UPDATE", SettingKeySecurityPenalty).Scan(&raw); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	old := DefaultSecurityPenaltyConfig()
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	if old.Version != cfg.Version {
		return SecurityPenaltyConfig{}, infraerrors.Conflict("security_penalty_config_conflict", "配置已更新，请刷新后重试")
	}
	cfg.Version++
	cfg.RiskControlEnabled = false
	data, err := json.Marshal(cfg)
	if err != nil {
		return SecurityPenaltyConfig{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE settings SET value=$2,updated_at=NOW() WHERE key=$1", SettingKeySecurityPenalty, string(data)); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	if err = tx.Commit(); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	return s.loadConfig(ctx)
}

func (c SecurityPenaltyConfig) IncludesGroup(groupID *int64) bool {
	if c.AllGroups {
		return true
	}
	for _, id := range c.GroupIDs {
		if groupID != nil && id == *groupID {
			return true
		}
	}
	return false
}

func (c SecurityPenaltyConfig) Applies(userID int64) bool {
	if !c.Enabled || !c.RiskControlEnabled || userID <= 0 {
		return false
	}
	for _, ids := range [][]int64{c.ExemptUserIDs, c.trustedUserIDs} {
		for _, id := range ids {
			if id == userID {
				return false
			}
		}
	}
	return true
}

func penaltySubject(cfg SecurityPenaltyConfig, sessionKey string) (string, string) {
	if cfg.Policy == "user_block" {
		return "user", "user"
	}
	if sessionKey != "" {
		return "session", sessionKey
	}
	return "request", ""
}

func penaltyAction(cfg SecurityPenaltyConfig, state SecurityPenaltyState, now time.Time) string {
	if state.Count <= 0 {
		return "none"
	}
	if state.BlockedUntil != nil && now.Before(*state.BlockedUntil) {
		return "block"
	}
	if now.Sub(state.WindowStartedAt) >= time.Duration(cfg.WindowHours)*time.Hour || (state.BlockedUntil != nil && !now.Before(*state.BlockedUntil)) {
		return "none"
	}
	if state.BlockedUntil != nil && now.Before(*state.BlockedUntil) {
		return "block"
	}
	if cfg.Policy == "session_route_then_block" && state.Count > 0 && state.RouteUntil != nil && now.Before(*state.RouteUntil) {
		return "route"
	}
	return "record"
}

func (s *SecurityPenaltyService) Admission(ctx context.Context, userID int64, sessionKey string) (SecurityPenaltyAdmission, error) {
	cfg, err := s.RuntimeConfig()
	if err != nil {
		return SecurityPenaltyAdmission{}, err
	}
	if !cfg.Applies(userID) || cfg.ObserveOnly {
		return SecurityPenaltyAdmission{}, nil
	}
	scope, key := penaltySubject(cfg, sessionKey)
	if scope == "request" {
		return SecurityPenaltyAdmission{}, nil
	}
	state, err := scanPenaltyState(s.db.QueryRowContext(ctx, "SELECT "+penaltyStateColumns+" FROM security_penalty_subjects WHERE user_id=$1 AND scope=$2 AND subject_key=$3 AND policy=$4", userID, scope, key, cfg.Policy))
	if errors.Is(err, sql.ErrNoRows) {
		return SecurityPenaltyAdmission{}, nil
	}
	if err != nil {
		return SecurityPenaltyAdmission{}, err
	}
	switch penaltyAction(cfg, state, time.Now()) {
	case "block":
		return SecurityPenaltyAdmission{Blocked: true}, nil
	case "route":
		return SecurityPenaltyAdmission{TargetGroupID: cfg.TargetGroupID}, nil
	default:
		return SecurityPenaltyAdmission{}, nil
	}
}

func securityPenaltyReason(v string) string {
	runes := []rune(strings.TrimSpace(v))
	if len(runes) > 1000 {
		runes = runes[:1000]
	}
	return string(runes)
}
