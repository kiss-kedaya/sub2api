package iprisk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrAuditConflict = errors.New("source state changed; refresh before releasing")
var ErrAuditReadOnly = errors.New("source has no safe generic IP release operation")

type AuditSource struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	Storage     string `json:"storage"`
	Enumeration string `json:"enumeration"`
	CanRelease  bool   `json:"can_release"`
	ManageURL   string `json:"manage_url,omitempty"`
}

// This is a capability inventory, never invented enforcement/history rows.
func AuditSources() []AuditSource {
	return []AuditSource{
		{"anonymous_api", "invalid_credentials", "redis", "indexed", true, ""},
		{"ip_risk", "site", "redis", "indexed", true, ""},
		{"ip_risk_observe", "observation", "redis", "indexed", true, ""},
		{"login_failures", "authentication", "not_connected", "not_connected", false, ""},
		{"login_limiter", "authentication", "not_connected", "not_connected", false, ""},
		{"manual_deny", "site", "settings", "bounded_config", false, "/admin/ip-audit?tab=config#ip-risk-settings"},
		{"allowlist", "site", "settings", "bounded_config", false, "/admin/ip-audit?tab=config#ip-risk-settings"},
		{"ip_history", "historical", "redis", "retained_events", false, ""},
		{"admin_history", "historical", "postgresql", "retained_events", false, "/admin/audit-logs"},
		{"security_penalty", "user_session", "not_connected", "not_connected", false, ""},
		{"key_acl", "individual_api_key", "postgresql", "no_generic_ip_api", false, "/admin/users"},
		{"local_invalid_auth", "instance", "memory", "no_shared_admin_api", false, ""},
		{"host_firewall", "external_host", "not_connected", "not_connected", false, ""},
	}
}

type AuditEntry struct {
	CheckIP      string `json:"check_ip,omitempty"`
	Duration     string `json:"duration"`
	OriginSource string `json:"origin_source,omitempty"`
	UserID       int64  `json:"user_id,omitempty"`
	ActorID      *int64 `json:"actor_id,omitempty"`
	Result       string `json:"result,omitempty"`
	ID           string `json:"id"`
	Source       string `json:"source"`
	Network      string `json:"network"`
	Scope        string `json:"scope"`
	Reason       string `json:"reason"`
	State        string `json:"state"`
	Effective    string `json:"effective"`
	Manual       bool   `json:"manual"`
	CanRelease   bool   `json:"can_release"`
	StartedAt    *int64 `json:"started_at"`
	ExpiresAt    *int64 `json:"expires_at"`
	Count        *int   `json:"count"`
	Threshold    *int   `json:"threshold"`
	Version      string `json:"version,omitempty"`
}

type AuditQuery struct {
	Source string `json:"source"`
	IP     string `json:"ip"`
	State  string `json:"state"`
	Sort   string `json:"sort"`
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
	Cursor int    `json:"cursor"`
	Limit  int    `json:"limit"`
}

type AuditPage struct {
	Partial    bool          `json:"partial"`
	Items      []AuditEntry  `json:"items"`
	HasMore    bool          `json:"has_more"`
	NextCursor int           `json:"next_cursor"`
	Sources    []AuditSource `json:"sources"`
	// False is deliberate: key ACL, account policy and external host state cannot
	// be proven absent by this IP-specific administrative view.
	Unrestricted bool `json:"unrestricted"`
}

func (q AuditQuery) Validate() error {
	found := false
	for _, source := range AuditSources() {
		if source.ID == q.Source {
			found = true
		}
	}
	if !found || q.Limit < 1 || q.Limit > 100 || q.Cursor < 0 || q.Cursor > 1000000 {
		return fmt.Errorf("invalid audit source or pagination")
	}
	if q.State != "" && q.State != "active" && q.State != "disabled" && q.State != "expired" && q.State != "observed" && q.State != "historical" && q.State != "allowlisted" {
		return fmt.Errorf("invalid audit state")
	}
	if q.Sort != "" && q.Sort != "expires_desc" && q.Sort != "expires_asc" {
		return fmt.Errorf("invalid audit sort")
	}
	if q.Start < 0 || q.End < 0 || (q.End != 0 && q.Start >= q.End) {
		return fmt.Errorf("invalid audit time range")
	}
	if q.IP != "" {
		if _, err := ParseNetwork(q.IP); err != nil {
			return fmt.Errorf("invalid audit IP/CIDR")
		}
	}
	return nil
}

func AuditIdentity(source, network string) string { return hashNetwork(source + "\n" + network) }
func auditMatches(row AuditEntry, q AuditQuery) bool {
	if q.State != "" && row.State != q.State {
		return false
	}
	if q.IP != "" {
		filter, _ := ParseNetwork(q.IP)
		network, err := ParseNetwork(row.Network)
		if err != nil || (!filter.Contains(network.Addr()) && !network.Contains(filter.Addr())) {
			return false
		}
	}
	// Time filtering uses observed start when present, otherwise actual expiry.
	// Missing timestamps never acquire a synthetic creation time.
	timestamp := row.StartedAt
	if timestamp == nil {
		timestamp = row.ExpiresAt
	}
	if q.Start != 0 && (timestamp == nil || *timestamp < q.Start) {
		return false
	}
	if q.End != 0 && (timestamp == nil || *timestamp >= q.End) {
		return false
	}
	return true
}
func AuditSlice(rows []AuditEntry, q AuditQuery) AuditPage {
	filtered := []AuditEntry{}
	for _, row := range rows {
		if auditMatches(row, q) {
			filtered = append(filtered, row)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if (filtered[i].Scope == "all_user_states") != (filtered[j].Scope == "all_user_states") {
			return filtered[i].Scope == "all_user_states"
		}
		a, b := int64(0), int64(0)
		if filtered[i].ExpiresAt != nil {
			a = *filtered[i].ExpiresAt
		} else if filtered[i].StartedAt != nil {
			a = *filtered[i].StartedAt
		}
		if filtered[j].ExpiresAt != nil {
			b = *filtered[j].ExpiresAt
		} else if filtered[j].StartedAt != nil {
			b = *filtered[j].StartedAt
		}
		if a == b {
			return filtered[i].ID < filtered[j].ID
		}
		if q.Sort == "expires_asc" {
			return a < b
		}
		return a > b
	})
	start := min(q.Cursor, len(filtered))
	end := min(start+q.Limit, len(filtered))
	return AuditPage{Items: filtered[start:end], HasMore: end < len(filtered), NextCursor: end, Sources: AuditSources()}
}

func (m *Manager) AuditPage(ctx context.Context, q AuditQuery) (AuditPage, error) {
	empty := AuditPage{Items: []AuditEntry{}, Sources: AuditSources()}
	if err := q.Validate(); err != nil {
		return empty, err
	}
	p, _ := m.snapshot(ctx)
	switch q.Source {
	case "anonymous_api", "ip_risk", "ip_risk_observe":
		return m.auditIndexed(ctx, p, q)
	case "manual_deny", "allowlist":
		rows := []AuditEntry{}
		if q.Source == "manual_deny" {
			for ruleIndex, rule := range p.config.Blacklist {
				row := AuditEntry{ID: AuditIdentity(q.Source, fmt.Sprintf("%d:%s", ruleIndex, rule.Network)), Source: q.Source, Network: rule.Network, Scope: "site", Reason: "manual_deny", State: "active", Effective: "conditional", Manual: true, Duration: "permanent"}
				if !p.config.Enabled || !rule.Enabled || m.emergencyDisabled {
					row.State = "disabled"
					row.Effective = "disabled"
				}
				if rule.ExpiresAt > 0 {
					expiry := rule.ExpiresAt
					row.Duration = "temporary"
					row.ExpiresAt = &expiry
					if expiry <= time.Now().Unix() {
						row.State = "expired"
						row.Effective = "disabled"
					}
				}
				rows = append(rows, row)
			}
		} else {
			for _, network := range p.config.Whitelist {
				rows = append(rows, AuditEntry{ID: AuditIdentity(q.Source, network), Source: q.Source, Network: network, Scope: "site", Reason: "allowlist", State: "allowlisted", Effective: "conditional", Manual: true, Duration: "permanent"})
			}
		}
		return AuditSlice(rows, q), nil
	case "login_failures", "login_limiter":
		return empty, nil // Existing deployment login sources are not shared with this module.
	case "ip_history":
		rows := []AuditEntry{}
		err := m.riskRedis(ctx, func(budget context.Context) error {
			values, e := m.redis.ZRangeArgs(budget, redis.ZRangeArgs{Key: redisPrefix + "events", Start: 0, Stop: maxEvents - 1, Rev: true}).Result()
			if e != nil {
				return e
			}
			for _, raw := range values {
				var event Event
				if json.Unmarshal([]byte(raw), &event) != nil {
					continue
				}
				if event.At < time.Now().Add(-time.Duration(p.config.EventRetentionHours)*time.Hour).Unix() {
					continue
				}
				at := event.At
				rows = append(rows, AuditEntry{ID: event.ID, Source: q.Source, Network: event.Network, Scope: "historical", Reason: event.Reason, State: "historical", Effective: "historical", Manual: event.Reason == "ip_blacklist", StartedAt: &at})
			}
			return nil
		})
		return AuditSlice(rows, q), err
	default:
		return empty, nil // Capability-only sources need their own authority/scope.
	}
}

func (m *Manager) auditIndexed(ctx context.Context, p *policy, q AuditQuery) (AuditPage, error) {
	page := AuditPage{Items: []AuditEntry{}, Sources: AuditSources(), NextCursor: q.Cursor}
	if q.IP != "" {
		prefix, _ := ParseNetwork(q.IP)
		if prefix.Bits() == prefix.Addr().BitLen() {
			network := prefix.String()
			if q.Source != "anonymous_api" {
				network = riskNetwork(prefix.Addr(), p.config.IPv6Prefix)
			}
			rows := []AuditEntry{}
			err := m.riskRedis(ctx, func(budget context.Context) error {
				raw, e := m.redis.Get(budget, m.auditBanKey(q.Source, network)).Result()
				if errors.Is(e, redis.Nil) {
					return nil
				}
				if e != nil {
					return e
				}
				var ban Ban
				if e = json.Unmarshal([]byte(raw), &ban); e != nil {
					return e
				}
				row := auditBanRow(q.Source, network, raw, ban)
				row.CheckIP = prefix.Addr().String()
				m.auditBanPolicy(&row, p)
				rows = append(rows, row)
				return nil
			})
			return AuditSlice(rows, q), err
		}
	}

	err := m.riskRedis(ctx, func(budget context.Context) error {
		now, e := m.redis.Time(budget).Result()
		if e != nil {
			return e
		}
		index := anonymousPrefix + "bans"
		cutoff := now.UnixMilli()
		if q.Source != "anonymous_api" {
			index = modePrefix(q.Source == "ip_risk_observe") + "bans"
			cutoff = now.Unix()
		}
		lower := "(" + strconv.FormatInt(cutoff, 10)
		total, e := m.redis.ZCount(budget, index, lower, "+inf").Result()
		if e != nil {
			return e
		}
		// At most 500 candidates per request, even for sparse filters. The cursor
		// advances over examined candidates; an empty filtered page may have more.
		for scanned := 0; scanned < 500 && int64(page.NextCursor) < total && len(page.Items) < q.Limit; {
			args := redis.ZRangeArgs{Key: index, Start: lower, Stop: "+inf", ByScore: true, Offset: int64(page.NextCursor), Count: 100}
			if q.Sort != "expires_asc" {
				args.Start = "+inf"
				args.Stop = lower
				args.Rev = true
			}
			networks, e := m.redis.ZRangeArgs(budget, args).Result()
			if e != nil {
				return e
			}
			if len(networks) == 0 {
				break
			}
			keys := make([]string, len(networks))
			for i, network := range networks {
				keys[i] = m.auditBanKey(q.Source, network)
			}
			values, e := m.redis.MGet(budget, keys...).Result()
			if e != nil {
				return e
			}
			for i, value := range values {
				page.NextCursor++
				scanned++
				if raw, ok := value.(string); ok {
					var ban Ban
					if json.Unmarshal([]byte(raw), &ban) == nil {
						row := auditBanRow(q.Source, networks[i], raw, ban)
						m.auditBanPolicy(&row, p)
						if auditMatches(row, q) {
							page.Items = append(page.Items, row)
						}
					}
				}
				if len(page.Items) >= q.Limit || scanned >= 500 {
					break
				}
			}
		}
		page.HasMore = int64(page.NextCursor) < total
		return nil
	})
	return page, err
}
func (m *Manager) auditBanPolicy(row *AuditEntry, p *policy) {
	if row.Source == "anonymous_api" {
		limit := p.config.AnonymousAPI.Threshold
		row.Threshold = &limit
		if !p.config.AnonymousAPI.Enabled || m.emergencyDisabled {
			row.State = "disabled"
			row.Effective = "disabled"
		}
	}
	if row.Source == "ip_risk_observe" {
		row.State = "observed"
		row.Effective = "observation"
	}
	if row.Source == "ip_risk" && (!p.config.Enabled || p.config.ObserveOnly || m.emergencyDisabled) {
		row.State = "disabled"
		row.Effective = "disabled"
	}
}

func auditBanRow(source, network, raw string, ban Ban) AuditEntry {
	started, expiry := ban.CreatedAt, ban.ExpiresAt
	return AuditEntry{ID: AuditIdentity(source, network), Source: source, Network: network, Scope: map[string]string{"anonymous_api": "invalid_credentials", "ip_risk": "site", "ip_risk_observe": "observation"}[source], Reason: ban.Reason, State: "active", Effective: "conditional", CanRelease: true, Duration: "temporary", StartedAt: &started, ExpiresAt: &expiry, Version: hashNetwork(raw)}
}
func (m *Manager) auditBanKey(source, network string) string {
	if source == "anonymous_api" {
		return anonymousKeys(network)[2]
	}
	return modePrefix(source == "ip_risk_observe") + hashNetwork(network) + ":ban"
}

var auditReleaseScript = redis.NewScript(`
local value=redis.call('GET',KEYS[1])
if not value or value~=ARGV[1] then return 0 end
if tonumber(ARGV[2])>0 then
 local time=redis.call('TIME')
 local now=tonumber(time[1])*1000+math.floor(tonumber(time[2])/1000)
 local expires=now+redis.call('PTTL',KEYS[1])
 if math.abs(expires-tonumber(ARGV[2]))>2 then return 0 end
end
for i=1,tonumber(ARGV[3]) do redis.call('DEL',KEYS[i]) end
if #KEYS>tonumber(ARGV[3]) then
 for i=tonumber(ARGV[3])+1,#KEYS do redis.call('ZREM',KEYS[i],ARGV[4]) end
end
return 1
`)

func (m *Manager) ReleaseAuditEntry(ctx context.Context, source, id, network, version string) error {
	if version == "" || len(version) > 96 || len(id) != 64 {
		return ErrAuditConflict
	}
	prefix, err := ParseNetwork(network)
	if err != nil {
		return fmt.Errorf("invalid source network")
	}
	network = prefix.String()
	switch source {
	case "anonymous_api", "ip_risk", "ip_risk_observe":
	default:
		return ErrAuditReadOnly
	}
	return m.auditRedis(ctx, func(budget context.Context) error {
		var keys []string
		raw := ""
		expiryMS := int64(0)
		deleteCount := 0
		{
			if id != AuditIdentity(source, network) || (source == "anonymous_api" && prefix.Bits() != prefix.Addr().BitLen()) {
				return ErrAuditConflict
			}
			key := m.auditBanKey(source, network)
			var e error
			raw, e = m.redis.Get(budget, key).Result()
			if errors.Is(e, redis.Nil) {
				return ErrAuditConflict
			}
			if e != nil {
				return e
			}
			if hashNetwork(raw) != version {
				return ErrAuditConflict
			}
			if source == "anonymous_api" {
				owned := anonymousKeys(network)
				keys = []string{owned[2], owned[1], owned[0], owned[3]}
				deleteCount = 2
			} else {
				root := modePrefix(source == "ip_risk_observe") + hashNetwork(network)
				keys = []string{key}
				for _, reason := range []string{"login_attempts", "login_failures", "register_attempts", "register_failures", "verification_attempts"} {
					keys = append(keys, root+":"+reason)
				}
				deleteCount = len(keys)
				keys = append(keys, modePrefix(source == "ip_risk_observe")+"bans")
			}
		}
		n, e := auditReleaseScript.Run(budget, m.redis, keys, raw, expiryMS, deleteCount, network).Int()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrAuditConflict
		}
		return nil
	})
}

// Exact-IP checks query each actual indexed source; no history is synthesized.
func (m *Manager) AuditRemaining(ctx context.Context, ip string) ([]AuditEntry, []string) {
	rows := []AuditEntry{}
	unknown := []string{"key_acl", "security_penalty", "local_invalid_auth", "host_firewall", "login_failures", "login_limiter"}
	for _, source := range []string{"anonymous_api", "ip_risk", "ip_risk_observe", "manual_deny"} {
		page, err := m.AuditPage(ctx, AuditQuery{Source: source, IP: ip, Limit: 100})
		if err != nil {
			unknown = append(unknown, source)
			continue
		}
		if page.HasMore {
			unknown = append(unknown, source)
		}
		for _, row := range page.Items {
			if row.State == "active" {
				rows = append(rows, row)
			}
		}
	}
	return rows, unknown
}

// Keep client-provided free text out of diagnostic/audit metadata.
func AuditReasonCode(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, code := range []string{"operator_review", "false_positive", "configuration_fix", "support_request"} {
		if raw == code {
			return raw
		}
	}
	return ""
}

func (m *Manager) AuditEntryPresent(ctx context.Context, source, id, network string) (bool, error) {
	if _, err := ParseNetwork(network); err != nil {
		return false, err
	}
	present := false
	err := m.riskRedis(ctx, func(budget context.Context) error {
		key := ""
		switch source {
		case "anonymous_api", "ip_risk", "ip_risk_observe":
			key = m.auditBanKey(source, network)

		}
		if key == "" {
			return ErrAuditReadOnly
		}
		n, e := m.redis.Exists(budget, key).Result()
		present = n > 0
		return e
	})
	return present, err
}

// Logical conflicts do not indicate a datastore outage and must not trip the
// shared Redis failure backoff used by request enforcement.
func (m *Manager) auditRedis(ctx context.Context, run func(context.Context) error) error {
	var logical error
	err := m.riskRedis(ctx, func(budget context.Context) error {
		e := run(budget)
		if errors.Is(e, ErrAuditConflict) || errors.Is(e, ErrAuditReadOnly) {
			logical = e
			return nil
		}
		return e
	})
	if err != nil {
		return err
	}
	return logical
}
