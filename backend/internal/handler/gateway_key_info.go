package handler

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

// Key self-service endpoints: what a program holding only an API key may ask the platform about that key.
//
//	GET /v1/sub2api/key      the key itself: status, expiry, quota, rate-limit windows, bound groups
//	GET /v1/sub2api/groups   the groups this key can use, with state and the multiplier it pays
//	GET /v1/sub2api/usage    this key's usage: today, last 7 days, this month, a chosen range
//	GET /v1/sub2api/account  the account behind the key: wallet balance, subscription allowance, today's usage of all its keys
//
// The exposure rule of the first three is "what the key is and what it has done, nothing about the account behind it":
//   - never the key itself (only its stored display prefix), never its IP allow/deny lists or last-used IP;
//   - never the wallet balance, subscription balance, account list, channel or upstream names, or upstream cost;
//   - multipliers are the ones the key actually pays (own group, the user's personal rate, the peak factor);
//   - usage is aggregated for this key id only, with the user-facing cost (actual_cost), never the account cost.
//
// /account (gateway_key_account.go) is the one deliberate exception, and only for the money: the owner asked that a
// program holding only an API key can read the wallet balance and today's usage of the whole account. So whoever holds
// a valid key can read those figures, and a key must be kept like the money it can spend. It still returns nothing that
// says who the owner is (no email, username, phone, id) and nothing about the owner's other keys. The existing
// /v1/usage keeps its own wallet block for compatibility; the first three endpoints do not copy it.
// All four accept a key that is expired or out of quota (it can still say so) and are limited per key.
const (
	keyInfoSchemaVersion = 1
	keyInfoCacheTTL      = 20 * time.Second
	keyInfoQueryBudget   = 2 * time.Second
	keyInfoMaxRangeDays  = 90
)

// ---------- /v1/sub2api/key ----------

type keyInfoWindow struct {
	Window      string     `json:"window"`
	Limit       float64    `json:"limit"`
	Used        float64    `json:"used"`
	Remaining   float64    `json:"remaining"`
	WindowStart *time.Time `json:"window_start,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
}

type keyInfoQuota struct {
	Limited   bool     `json:"limited"`
	Limit     *float64 `json:"limit,omitempty"`
	Used      float64  `json:"used"`
	Remaining *float64 `json:"remaining,omitempty"`
	Unit      string   `json:"unit"`
}

type keyInfoGroupRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
	Primary  bool   `json:"primary"`
}

type keyInfoResponse struct {
	Object        string `json:"object"`
	SchemaVersion int    `json:"schema_version"`
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	// KeyPrefix is the stored display prefix (first characters only), enough to recognise the key.
	KeyPrefix string `json:"key_prefix"`
	// Status is active / inactive / quota_exhausted / expired, as the key itself reports it.
	Status string `json:"status"`
	// IsUsable is false when a request with this key would be refused for a reason about the key itself.
	IsUsable      bool            `json:"is_usable"`
	UnusableCause string          `json:"unusable_cause,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	ExpiresAt     *time.Time      `json:"expires_at"`
	DaysUntilExp  *int            `json:"days_until_expiry,omitempty"`
	Quota         keyInfoQuota    `json:"quota"`
	RateLimits    []keyInfoWindow `json:"rate_limits"`
	// RouteMode is none / single / smart; Groups lists the bound groups in routing order.
	RouteMode        string            `json:"route_mode"`
	Groups           []keyInfoGroupRef `json:"groups"`
	ConcurrencyLimit int               `json:"concurrency_limit"`
	ObservedAt       time.Time         `json:"observed_at"`
}

// KeyInfo returns what the authenticated API key is.
// GET /v1/sub2api/key
func (h *GatewayHandler) KeyInfo(c *gin.Context) {
	apiKey, ok := h.keyInfoSubject(c)
	if !ok {
		return
	}
	now := timezone.Now()
	resp := keyInfoResponse{
		Object:           "sub2api.key",
		SchemaVersion:    keyInfoSchemaVersion,
		ID:               apiKey.ID,
		Name:             apiKey.Name,
		KeyPrefix:        apiKey.DisplayPrefix(),
		Status:           apiKey.Status,
		CreatedAt:        apiKey.CreatedAt.UTC(),
		ExpiresAt:        utcOrNil(apiKey.ExpiresAt),
		RouteMode:        apiKey.RouteMode(),
		Groups:           h.keyInfoGroups(c.Request.Context(), apiKey),
		ConcurrencyLimit: apiKey.ConcurrencyLimit,
		ObservedAt:       now.UTC(),
	}
	if apiKey.ExpiresAt != nil {
		days := apiKey.GetDaysUntilExpiry()
		resp.DaysUntilExp = &days
	}
	resp.Quota = buildKeyInfoQuota(apiKey)
	resp.RateLimits = h.buildKeyInfoWindows(c.Request.Context(), apiKey)
	resp.IsUsable, resp.UnusableCause = keyUsability(apiKey, resp.Groups)

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

func utcOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func buildKeyInfoQuota(apiKey *service.APIKey) keyInfoQuota {
	q := keyInfoQuota{Limited: apiKey.Quota > 0, Used: apiKey.QuotaUsed, Unit: "USD"}
	if apiKey.Quota > 0 {
		limit := apiKey.Quota
		remaining := apiKey.GetQuotaRemaining()
		q.Limit = &limit
		q.Remaining = &remaining
	}
	return q
}

// buildKeyInfoWindows reports the 5h / 1d / 7d spend windows the key was given. The live counters come from the
// database (the auth snapshot can be a little old); if that read fails the endpoint still answers, from the
// snapshot, rather than turning a status poll into an error.
func (h *GatewayHandler) buildKeyInfoWindows(ctx context.Context, apiKey *service.APIKey) []keyInfoWindow {
	out := make([]keyInfoWindow, 0, 3)
	if !apiKey.HasRateLimits() {
		return out
	}
	var data *service.APIKeyRateLimitData
	if h.apiKeyService != nil {
		readCtx, cancel := context.WithTimeout(ctx, keyInfoQueryBudget)
		defer cancel()
		if loaded, err := h.apiKeyService.GetRateLimitData(readCtx, apiKey.ID); err == nil {
			data = loaded
		}
	}
	add := func(label string, limit float64, window time.Duration, used float64, start *time.Time) {
		if limit <= 0 {
			return
		}
		entry := keyInfoWindow{Window: label, Limit: limit, Used: used, Remaining: max(0, limit-used), WindowStart: utcOrNil(start)}
		if start != nil && !service.IsWindowExpired(start, window) {
			reset := start.Add(window).UTC()
			entry.ResetAt = &reset
		}
		out = append(out, entry)
	}
	if data != nil {
		add("5h", apiKey.RateLimit5h, service.RateLimitWindow5h, data.EffectiveUsage5h(), data.Window5hStart)
		add("1d", apiKey.RateLimit1d, service.RateLimitWindow1d, data.EffectiveUsage1d(), data.Window1dStart)
		add("7d", apiKey.RateLimit7d, service.RateLimitWindow7d, data.EffectiveUsage7d(), data.Window7dStart)
		return out
	}
	add("5h", apiKey.RateLimit5h, service.RateLimitWindow5h, apiKey.EffectiveUsage5h(), apiKey.Window5hStart)
	add("1d", apiKey.RateLimit1d, service.RateLimitWindow1d, apiKey.EffectiveUsage1d(), apiKey.Window1dStart)
	add("7d", apiKey.RateLimit7d, service.RateLimitWindow7d, apiKey.EffectiveUsage7d(), apiKey.Window7dStart)
	return out
}

// keyInfoGroups lists the key's bound groups in routing order. The primary group is already loaded with the key;
// the others are looked up once and cached briefly (the group list is the same for every key of the platform).
func (h *GatewayHandler) keyInfoGroups(ctx context.Context, apiKey *service.APIKey) []keyInfoGroupRef {
	ids := apiKey.CandidateGroupIDs()
	out := make([]keyInfoGroupRef, 0, len(ids))
	if len(ids) == 0 {
		return out
	}
	byID := h.keyInfoGroupCatalog(ctx)
	for i, id := range ids {
		var g *service.Group
		if apiKey.Group != nil && apiKey.Group.ID == id {
			g = apiKey.Group
		} else if found, ok := byID[id]; ok {
			g = found
		}
		if g == nil {
			continue
		}
		out = append(out, keyInfoGroupRef{ID: g.ID, Name: g.Name, Platform: g.Platform, Status: g.Status, Primary: i == 0})
	}
	return out
}

// keyUsability explains, in one place, why a request made with this key right now would be refused for a reason
// about the key. It mirrors the checks authentication performs; it does not predict upstream availability.
func keyUsability(apiKey *service.APIKey, groups []keyInfoGroupRef) (bool, string) {
	switch {
	case apiKey.Status == service.StatusAPIKeyExpired || apiKey.IsExpired():
		return false, "expired"
	case apiKey.Status == service.StatusAPIKeyQuotaExhausted || apiKey.IsQuotaExhausted():
		return false, "quota_exhausted"
	case !apiKey.IsActive():
		return false, "disabled"
	case apiKey.GroupID == nil && len(groups) == 0:
		return false, "no_group"
	case len(groups) > 0 && groups[0].Status != service.StatusActive:
		return false, "group_unavailable"
	}
	return true, ""
}

// ---------- /v1/sub2api/groups ----------

type keyInfoGroup struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Platform    string `json:"platform"`
	Status      string `json:"status"`
	// BoundToKey is true for the groups this very key routes to; the others are groups the user may bind.
	BoundToKey bool `json:"bound_to_key"`
	Primary    bool `json:"primary"`
	// SubscriptionType is standard or subscription.
	SubscriptionType string `json:"subscription_type"`
	// RateMultiplier is the group's base multiplier; ResolvedRateMultiplier includes the user's personal rate for
	// the group; EffectiveRateMultiplier also includes the peak factor that applies right now.
	RateMultiplier          float64  `json:"rate_multiplier"`
	UserRateMultiplier      *float64 `json:"user_rate_multiplier,omitempty"`
	ResolvedRateMultiplier  float64  `json:"resolved_rate_multiplier"`
	PeakRateEnabled         bool     `json:"peak_rate_enabled"`
	PeakStart               *string  `json:"peak_start,omitempty"`
	PeakEnd                 *string  `json:"peak_end,omitempty"`
	PeakRateMultiplier      *float64 `json:"peak_rate_multiplier,omitempty"`
	EffectiveRateMultiplier float64  `json:"effective_rate_multiplier"`
}

type keyInfoGroupsResponse struct {
	Object        string         `json:"object"`
	SchemaVersion int            `json:"schema_version"`
	Groups        []keyInfoGroup `json:"groups"`
	Timezone      string         `json:"timezone"`
	ObservedAt    time.Time      `json:"observed_at"`
}

// KeyGroups returns the groups this key can use, with their state and the multiplier it pays.
// GET /v1/sub2api/groups
func (h *GatewayHandler) KeyGroups(c *gin.Context) {
	apiKey, ok := h.keyInfoSubject(c)
	if !ok {
		return
	}
	if h.apiKeyService == nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Group information is unavailable")
		return
	}
	ctx := c.Request.Context()
	readCtx, cancel := context.WithTimeout(ctx, keyInfoQueryBudget)
	defer cancel()
	available, err := h.keyInfoAvailableGroups(readCtx, apiKey.UserID)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Group information is temporarily unavailable")
		return
	}

	bound := make(map[int64]int)
	for i, id := range apiKey.CandidateGroupIDs() {
		bound[id] = i
	}
	now := timezone.Now()
	out := make([]keyInfoGroup, 0, len(available)+1)
	seen := make(map[int64]bool, len(available)+1)
	appendGroup := func(g *service.Group) {
		if g == nil || seen[g.ID] {
			return
		}
		seen[g.ID] = true
		position, isBound := bound[g.ID]
		out = append(out, h.buildKeyInfoGroup(ctx, apiKey.UserID, g, isBound, isBound && position == 0, now))
	}
	// The key's own groups first, in routing order, even when the user could no longer bind them today:
	// the key still routes there and its owner needs to see what it pays.
	catalog := h.keyInfoGroupCatalog(ctx)
	for _, id := range apiKey.CandidateGroupIDs() {
		if apiKey.Group != nil && apiKey.Group.ID == id {
			appendGroup(apiKey.Group)
		} else if g, ok := catalog[id]; ok {
			appendGroup(g)
		}
	}
	for i := range available {
		appendGroup(&available[i])
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, keyInfoGroupsResponse{
		Object:        "sub2api.key_groups",
		SchemaVersion: keyInfoSchemaVersion,
		Groups:        out,
		Timezone:      timezone.Location().String(),
		ObservedAt:    now.UTC(),
	})
}

func (h *GatewayHandler) buildKeyInfoGroup(ctx context.Context, userID int64, g *service.Group, bound, primary bool, now time.Time) keyInfoGroup {
	resolved := h.resolveKeyInfoGroupRate(ctx, userID, g)
	peak := g.PeakMultiplierAt(now)
	item := keyInfoGroup{
		ID:                      g.ID,
		Name:                    g.Name,
		Description:             g.Description,
		Platform:                g.Platform,
		Status:                  g.Status,
		BoundToKey:              bound,
		Primary:                 primary,
		SubscriptionType:        g.SubscriptionType,
		RateMultiplier:          g.RateMultiplier,
		ResolvedRateMultiplier:  resolved,
		PeakRateEnabled:         g.PeakRateEnabled,
		EffectiveRateMultiplier: resolved * peak,
	}
	if resolved != g.RateMultiplier {
		personal := resolved
		item.UserRateMultiplier = &personal
	}
	if g.PeakRateEnabled {
		start, end, multiplier := g.PeakStart, g.PeakEnd, g.PeakRateMultiplier
		item.PeakStart, item.PeakEnd, item.PeakRateMultiplier = &start, &end, &multiplier
	}
	return item
}

func (h *GatewayHandler) resolveKeyInfoGroupRate(ctx context.Context, userID int64, g *service.Group) float64 {
	switch g.Platform {
	case service.PlatformOpenAI, service.PlatformGrok:
		if h.openAIGatewayService != nil {
			return h.openAIGatewayService.ResolveUserGroupRateMultiplier(ctx, userID, g.ID, g.RateMultiplier)
		}
	default:
		if h.gatewayService != nil {
			return h.gatewayService.ResolveUserGroupRateMultiplier(ctx, userID, g.ID, g.RateMultiplier)
		}
	}
	return g.RateMultiplier
}

// ---------- short shared cache for the group lists ----------

// The list of active groups and the groups a user may bind change rarely, but a status script polls them. A short
// per-process cache with a single in-flight load keeps polling from reaching the database each time. Entries are
// read-only copies; nothing request-specific (no key, no user rate) is stored in the shared catalogue.
type keyInfoCache struct {
	mu        sync.Mutex
	catalog   map[int64]*service.Group
	catalogAt time.Time
	available map[int64]keyInfoAvailableEntry
	flight    singleflight.Group
}

type keyInfoAvailableEntry struct {
	groups []service.Group
	at     time.Time
}

var sharedKeyInfoCache keyInfoCache

func (h *GatewayHandler) keyInfoGroupCatalog(ctx context.Context) map[int64]*service.Group {
	cache := &sharedKeyInfoCache
	cache.mu.Lock()
	if cache.catalog != nil && time.Since(cache.catalogAt) < keyInfoCacheTTL {
		out := cache.catalog
		cache.mu.Unlock()
		return out
	}
	stale := cache.catalog
	cache.mu.Unlock()
	if h.apiKeyService == nil {
		return stale
	}
	value, err, _ := cache.flight.Do("catalog", func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keyInfoQueryBudget)
		defer cancel()
		groups, err := h.apiKeyService.ListActiveGroups(loadCtx)
		if err != nil {
			return nil, err
		}
		catalog := make(map[int64]*service.Group, len(groups))
		for i := range groups {
			catalog[groups[i].ID] = &groups[i]
		}
		cache.mu.Lock()
		cache.catalog, cache.catalogAt = catalog, time.Now()
		cache.mu.Unlock()
		return catalog, nil
	})
	if catalog, ok := value.(map[int64]*service.Group); err == nil && ok {
		return catalog
	}
	return stale
}

func (h *GatewayHandler) keyInfoAvailableGroups(ctx context.Context, userID int64) ([]service.Group, error) {
	cache := &sharedKeyInfoCache
	cache.mu.Lock()
	if entry, ok := cache.available[userID]; ok && time.Since(entry.at) < keyInfoCacheTTL {
		cache.mu.Unlock()
		return entry.groups, nil
	}
	cache.mu.Unlock()
	value, err, _ := cache.flight.Do("available:"+itoa(userID), func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keyInfoQueryBudget)
		defer cancel()
		groups, err := h.apiKeyService.GetAvailableGroups(loadCtx, userID)
		if err != nil {
			return nil, err
		}
		cache.mu.Lock()
		if cache.available == nil || len(cache.available) > 4096 {
			cache.available = make(map[int64]keyInfoAvailableEntry)
		}
		cache.available[userID] = keyInfoAvailableEntry{groups: groups, at: time.Now()}
		cache.mu.Unlock()
		return groups, nil
	})
	if err != nil {
		return nil, err
	}
	groups, _ := value.([]service.Group) // only this function stores into the flight, always this type
	return groups, nil
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---------- /v1/sub2api/usage ----------

type keyUsageBlock struct {
	Label string `json:"label"`
	// Start / End are YYYY-MM-DD in the response timezone; End is inclusive.
	Start string `json:"start"`
	End   string `json:"end"`
	usagestats.APIKeyUsageSummary
}

type keyUsageResponse struct {
	Object        string                             `json:"object"`
	SchemaVersion int                                `json:"schema_version"`
	Timezone      string                             `json:"timezone"`
	Today         keyUsageBlock                      `json:"today"`
	Last7Days     keyUsageBlock                      `json:"last_7_days"`
	ThisMonth     keyUsageBlock                      `json:"this_month"`
	Range         *keyUsageBlock                     `json:"range,omitempty"`
	Daily         []usagestats.APIKeyDailyUsagePoint `json:"daily,omitempty"`
	// Partial lists the blocks that could not be computed within the time budget (they are omitted, not zero).
	Partial    []string  `json:"partial,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

// KeyUsage returns the authenticated key's own usage.
// GET /v1/sub2api/usage[?start_date=YYYY-MM-DD&end_date=YYYY-MM-DD][&timezone=Area/City][&daily=1]
func (h *GatewayHandler) KeyUsage(c *gin.Context) {
	apiKey, ok := h.keyInfoSubject(c)
	if !ok {
		return
	}
	if h.usageService == nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Usage information is unavailable")
		return
	}
	frame := newKeyInfoFrame(c.Query("timezone"))
	userTZ, loc, now, todayStart := frame.TZ, frame.Loc, frame.Now, frame.TodayStart

	var rangeStart, rangeEnd time.Time
	hasRange := false
	startQ, endQ := strings.TrimSpace(c.Query("start_date")), strings.TrimSpace(c.Query("end_date"))
	if startQ != "" || endQ != "" {
		start, end := todayStart.AddDate(0, 0, -29), todayStart
		if startQ != "" {
			parsed, err := timezone.ParseInUserLocation("2006-01-02", startQ, userTZ)
			if err != nil {
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid start_date, use YYYY-MM-DD")
				return
			}
			start = parsed
		}
		if endQ != "" {
			parsed, err := timezone.ParseInUserLocation("2006-01-02", endQ, userTZ)
			if err != nil {
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid end_date, use YYYY-MM-DD")
				return
			}
			end = parsed
		}
		if end.Before(start) {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "end_date must not be before start_date")
			return
		}
		if end.Sub(start) > time.Duration(keyInfoMaxRangeDays-1)*24*time.Hour+time.Hour {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "The date range is limited to 90 days")
			return
		}
		rangeStart, rangeEnd, hasRange = start, end.AddDate(0, 0, 1), true
	}

	statsCtx, cancel := context.WithTimeout(c.Request.Context(), keyInfoQueryBudget*2)
	defer cancel()
	resp := keyUsageResponse{Object: "sub2api.key_usage", SchemaVersion: keyInfoSchemaVersion, Timezone: loc.String(), ObservedAt: now.UTC()}
	partial := make([]string, 0, 3)

	block := func(label string, start, endExclusive time.Time) (keyUsageBlock, bool) {
		summary, err := h.usageService.GetAPIKeyUsageSummary(statsCtx, apiKey.ID, start, endExclusive)
		if err != nil || summary == nil {
			partial = append(partial, label)
			return keyUsageBlock{Label: label}, false
		}
		return keyUsageBlock{
			Label: label, Start: start.In(loc).Format("2006-01-02"), End: endExclusive.AddDate(0, 0, -1).In(loc).Format("2006-01-02"),
			APIKeyUsageSummary: *summary,
		}, true
	}
	resp.Today, _ = block("today", todayStart, todayStart.AddDate(0, 0, 1))
	resp.Last7Days, _ = block("last_7_days", todayStart.AddDate(0, 0, -6), todayStart.AddDate(0, 0, 1))
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, todayStart.Location())
	resp.ThisMonth, _ = block("this_month", monthStart, todayStart.AddDate(0, 0, 1))
	if hasRange {
		if blk, ok := block("range", rangeStart, rangeEnd); ok {
			resp.Range = &blk
		}
	}
	if c.Query("daily") == "1" || c.Query("daily") == "true" {
		from, to := todayStart.AddDate(0, 0, -6), todayStart.AddDate(0, 0, 1)
		if hasRange {
			from, to = rangeStart, rangeEnd
		}
		if points, err := h.usageService.GetAPIKeyDailyUsage(statsCtx, apiKey.UserID, apiKey.ID, from, to); err == nil {
			resp.Daily = points
		} else {
			partial = append(partial, "daily")
		}
	}
	if len(partial) > 0 {
		resp.Partial = partial
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// keyInfoFrame is the "when" of a self-service request, resolved once and the same way by every endpoint that takes
// a timezone parameter: the zone the answer is written in, the current time in it, and the start of the local day.
// An empty, over-long (more than 64 bytes) or unknown timezone falls back to the site's own time zone.
type keyInfoFrame struct {
	TZ         string // the cleaned parameter; "" when none was usable
	Loc        *time.Location
	Now        time.Time
	TodayStart time.Time
}

func newKeyInfoFrame(raw string) keyInfoFrame {
	userTZ := strings.TrimSpace(raw)
	if len(userTZ) > 64 {
		userTZ = ""
	}
	loc := timezone.Location()
	if userTZ != "" {
		if loaded, err := time.LoadLocation(userTZ); err == nil {
			loc = loaded
		}
	}
	now := timezone.NowInUserLocation(userTZ)
	return keyInfoFrame{TZ: userTZ, Loc: loc, Now: now, TodayStart: timezone.StartOfDayInUserLocation(now, userTZ)}
}

// keyInfoSubject returns the authenticated key, or writes the same errors the other key-scoped endpoints use.
func (h *GatewayHandler) keyInfoSubject(c *gin.Context) (*service.APIKey, bool) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return nil, false
	}
	if h.cfg != nil && h.cfg.RunMode == config.RunModeSimple {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Key information is not supported in simple mode")
		return nil, false
	}
	return apiKey, true
}
