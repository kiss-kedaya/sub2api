package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"
)

type CountryFilter struct {
	Mode                   string   `json:"mode"` // off, exclude, include
	Codes                  []string `json:"codes"`
	AllowUnknown           bool     `json:"allow_unknown"`
	DynamicProviderManaged bool     `json:"dynamic_provider_managed"`
}

type CountryObservation struct {
	Code      string    `json:"code,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

func validCountry(code string) bool {
	if code == "ZZ" || code == "UK" {
		return false
	}
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return false
	}
	region, err := language.ParseRegion(code)
	return err == nil && region.IsCountry() && region.String() == code
}

func normalizeCountryFilter(f CountryFilter) (CountryFilter, error) {
	if f.Mode == "" {
		f.Mode = "off"
	}
	if f.Mode != "off" && f.Mode != "exclude" && f.Mode != "include" {
		return f, errors.New("invalid country filter mode")
	}
	if len(f.Codes) > 300 {
		return f, errors.New("too many country codes")
	}
	unique := map[string]bool{}
	codes := []string{}
	for _, code := range f.Codes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if !validCountry(code) {
			return f, errors.New("invalid country code")
		}
		if !unique[code] {
			unique[code] = true
			codes = append(codes, code)
		}
	}
	if f.Mode != "off" && len(codes) == 0 {
		return f, errors.New("select at least one country or region")
	}
	sort.Strings(codes)
	f.Codes = codes
	return f, nil
}

func countryAllowed(s saved, name string) bool {
	f := s.CountryFilter
	if f.Mode == "" || f.Mode == "off" {
		return true
	}
	if strings.HasPrefix(name, "DYNAMIC-") {
		// A different CONNECT may have a different IP even immediately after a
		// successful country probe. Never enforce a stale per-node observation.
		return f.DynamicProviderManaged || f.AllowUnknown
	}
	code := s.Countries[name].Code
	if !validCountry(code) {
		return f.AllowUnknown
	}
	found := false
	for _, selected := range f.Codes {
		if code == selected {
			found = true
			break
		}
	}
	if f.Mode == "include" {
		return found
	}
	return !found
}

func countryCodes() []string {
	codes := []string{}
	for _, r := range language.Supported.Regions() {
		code := r.String()
		if validCountry(code) {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	return codes
}

// Country checks use separate local proxy processes. The production group is
// never temporarily widened to include a banned node during a measurement.
func (m *Manager) observeCountry(ctx context.Context, node map[string]any) CountryObservation {
	observation := CountryObservation{CheckedAt: time.Now(), Error: "lookup_failed"}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	name, ok := node["name"].(string)
	if !ok || name == "" {
		return observation
	}
	_ = m.withIsolatedNode(ctx, name, node, func(proxy *url.URL) {
		if code := m.lookupCountry(ctx, proxy); code != "" {
			observation.Code = code
			observation.Error = ""
		}
	})
	return observation
}

// lookupCountry returns the exit country seen through proxy, or "" when the
// lookup fails or does not name a valid country.
func (m *Manager) lookupCountry(ctx context.Context, proxy *url.URL) string {
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := m.countryLookupURL
	if endpoint == "" {
		endpoint = "https://api.country.is/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil || len(body) > 8192 {
		return ""
	}
	var answer struct {
		Country string `json:"country"`
		IP      string `json:"ip"`
	}
	if json.Unmarshal(body, &answer) != nil || net.ParseIP(answer.IP) == nil {
		return ""
	}
	code := strings.ToUpper(strings.TrimSpace(answer.Country))
	if !validCountry(code) {
		return ""
	}
	return code
}

// Each click checks up to 20 least-recently checked nodes, at most two at once.
// Failed checks become unknown; no stale location is silently reused.
func (m *Manager) scanCountries(ctx context.Context, s saved, target string) (map[string]CountryObservation, error) {
	if strings.HasPrefix(target, "DYNAMIC-") {
		return nil, errors.New("dynamic exit regions change between connections; configure regions at the provider")
	}
	nodes := make([]map[string]any, 0, len(s.Nodes))
	for _, node := range s.Nodes {
		name, _ := node["name"].(string)
		if !strings.HasPrefix(name, "DYNAMIC-") {
			nodes = append(nodes, node)
		}
	}
	if target != "" {
		nodes = nil
		for _, node := range s.Nodes {
			if node["name"] == target {
				nodes = append(nodes, node)
			}
		}
		if len(nodes) == 0 {
			return nil, errors.New("unknown node")
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		a, _ := nodes[i]["name"].(string)
		b, _ := nodes[j]["name"].(string)
		return s.Countries[a].CheckedAt.Before(s.Countries[b].CheckedAt)
	})
	if len(nodes) > 20 {
		nodes = nodes[:20]
	}
	results := map[string]CountryObservation{}
	for k, v := range s.Countries {
		results[k] = v
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 2)
	for _, node := range nodes {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func(node map[string]any) {
			defer wg.Done()
			defer func() { <-slots }()
			name, _ := node["name"].(string)
			result := m.observeCountry(ctx, node)
			mu.Lock()
			results[name] = result
			mu.Unlock()
		}(node)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, errors.New("country check cancelled; previous results retained")
	}
	return results, nil
}
