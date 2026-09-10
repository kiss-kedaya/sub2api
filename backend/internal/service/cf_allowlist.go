package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	CFAllowlistRechargeThreshold = 100.0
	CFAllowlistMaxIPs            = 5
	cfAllowlistHTTPTimeout       = 15 * time.Second
)

type CFAllowlistItem struct {
	ID        int64     `json:"id"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
}

type CFAllowlistStatus struct {
	Eligible       bool              `json:"eligible"`
	Threshold      float64           `json:"threshold"`
	TotalRecharged float64           `json:"total_recharged"`
	MaxSlots       int               `json:"max_slots"`
	UsedSlots      int               `json:"used_slots"`
	DetectedIP     string            `json:"detected_ip"`
	Configured     bool              `json:"configured"`
	Items          []CFAllowlistItem `json:"items"`
}

type CFAllowlistService struct {
	repo   *CFAllowlistRepository
	users  *UserService
	cfg    *config.Config
	client *http.Client
}

func NewCFAllowlistService(repo *CFAllowlistRepository, users *UserService, cfg *config.Config) *CFAllowlistService {
	return &CFAllowlistService{
		repo:   repo,
		users:  users,
		cfg:    cfg,
		client: &http.Client{Timeout: cfAllowlistHTTPTimeout},
	}
}

func cfAllowlistSlots(totalRecharged float64) int {
	if totalRecharged < CFAllowlistRechargeThreshold {
		return 0
	}
	n := int(totalRecharged / CFAllowlistRechargeThreshold)
	if n > CFAllowlistMaxIPs {
		return CFAllowlistMaxIPs
	}
	return n
}

func NormalizePublicIP(raw string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return "", infraerrors.BadRequest("INVALID_IP", "请填写有效的公网 IP")
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "", infraerrors.BadRequest("INVALID_IP", "不能提交内网或回环地址")
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String(), nil
	}
	return ip.String(), nil
}

func (s *CFAllowlistService) token() string {
	if v := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")); v != "" {
		return v
	}
	if s == nil || s.cfg == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.Cloudflare.APIToken)
}

func (s *CFAllowlistService) zoneID() string {
	if v := strings.TrimSpace(os.Getenv("CLOUDFLARE_ZONE_ID")); v != "" {
		return v
	}
	if s == nil || s.cfg == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.Cloudflare.ZoneID)
}

func (s *CFAllowlistService) configured() bool {
	return s.token() != "" && s.zoneID() != ""
}

func (s *CFAllowlistService) Status(ctx context.Context, userID int64, detectedIP string) (*CFAllowlistStatus, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &CFAllowlistStatus{
		Threshold:      CFAllowlistRechargeThreshold,
		TotalRecharged: user.TotalRecharged,
		MaxSlots:       cfAllowlistSlots(user.TotalRecharged),
		UsedSlots:      len(items),
		DetectedIP:     strings.TrimSpace(detectedIP),
		Configured:     s.configured(),
		Items:          make([]CFAllowlistItem, 0, len(items)),
	}
	out.Eligible = out.MaxSlots > 0
	for _, row := range items {
		out.Items = append(out.Items, CFAllowlistItem{ID: row.ID, IP: row.IP, CreatedAt: row.CreatedAt})
	}
	return out, nil
}

func (s *CFAllowlistService) Add(ctx context.Context, userID int64, rawIP string) (*CFAllowlistItem, error) {
	if !s.configured() {
		return nil, infraerrors.ServiceUnavailable("CF_ALLOWLIST_NOT_CONFIGURED", "管理员尚未配置 Cloudflare 白名单")
	}
	ip, err := NormalizePublicIP(rawIP)
	if err != nil {
		return nil, err
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	slots := cfAllowlistSlots(user.TotalRecharged)
	if slots <= 0 {
		return nil, infraerrors.Forbidden("RECHARGE_REQUIRED", "累计充值满 100 元后才能提交白名单 IP")
	}
	used, err := s.repo.CountByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if used >= slots {
		return nil, infraerrors.Forbidden("ALLOWLIST_SLOT_FULL", "白名额已用完，每累计充值 100 元可增加 1 个 IP")
	}
	ruleID, err := s.createCloudflareRule(ctx, userID, ip)
	if err != nil {
		return nil, err
	}
	row, err := s.repo.Insert(ctx, userID, ip, ruleID)
	if err != nil {
		_ = s.deleteCloudflareRule(ctx, ruleID)
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(err.Error(), "unique") {
			return nil, infraerrors.BadRequest("IP_ALREADY_ALLOWED", "这个 IP 已经在白名单里")
		}
		return nil, err
	}
	return &CFAllowlistItem{ID: row.ID, IP: row.IP, CreatedAt: row.CreatedAt}, nil
}

func (s *CFAllowlistService) Delete(ctx context.Context, userID, id int64) error {
	row, err := s.repo.GetByIDForUser(ctx, id, userID)
	if err != nil {
		return infraerrors.NotFound("ALLOWLIST_NOT_FOUND", "白名单记录不存在")
	}
	if row.CFRuleID != "" {
		if err := s.deleteCloudflareRule(ctx, row.CFRuleID); err != nil {
			return err
		}
	}
	return s.repo.DeleteByIDForUser(ctx, id, userID)
}

type cfAPIEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Result struct {
		ID string `json:"id"`
	} `json:"result"`
}

func (s *CFAllowlistService) createCloudflareRule(ctx context.Context, userID int64, ip string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"mode": "whitelist",
		"configuration": map[string]string{
			"target": "ip",
			"value":  ip,
		},
		"notes": fmt.Sprintf("kedaya user %d", userID),
	})
	var env cfAPIEnvelope
	if err := s.cfDo(ctx, http.MethodPost, "/firewall/access_rules/rules", body, &env); err != nil {
		return "", err
	}
	if strings.TrimSpace(env.Result.ID) == "" {
		return "", infraerrors.InternalServer("CF_ALLOWLIST_FAILED", "Cloudflare 未返回规则 ID")
	}
	return env.Result.ID, nil
}

func (s *CFAllowlistService) deleteCloudflareRule(ctx context.Context, ruleID string) error {
	var env cfAPIEnvelope
	return s.cfDo(ctx, http.MethodDelete, "/firewall/access_rules/rules/"+ruleID, nil, &env)
}

func (s *CFAllowlistService) cfDo(ctx context.Context, method, path string, body []byte, out *cfAPIEnvelope) error {
	zoneID := s.zoneID()
	token := s.token()
	url := "https://api.cloudflare.com/client/v4/zones/" + zoneID + path
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return infraerrors.ServiceUnavailable("CF_ALLOWLIST_UNAVAILABLE", "无法连接 Cloudflare")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out == nil {
		out = &cfAPIEnvelope{}
	}
	_ = json.Unmarshal(raw, out)
	if resp.StatusCode >= 300 || !out.Success {
		msg := "Cloudflare 拒绝了这次白名单请求"
		if len(out.Errors) > 0 && strings.TrimSpace(out.Errors[0].Message) != "" {
			msg = out.Errors[0].Message
		}
		return infraerrors.BadRequest("CF_ALLOWLIST_FAILED", msg)
	}
	return nil
}
