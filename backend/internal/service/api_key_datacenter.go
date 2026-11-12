package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipquality"
)

type apiKeyRecentIPLister interface {
	ListRecentIPAddresses(ctx context.Context, apiKeyID int64, limit int) ([]string, error)
}

// datacenterIPClassifier reports which of ips ip-api flags as hosting/proxy.
// It is a field type so tests can replace the third-party lookup.
type datacenterIPClassifier func(ctx context.Context, ips []string) map[string]bool

// classifyDatacenterIPs 默认走 ipquality；失败/未知一律放行（返回空集合）。
func (s *APIKeyService) classifyDatacenterIPs(ctx context.Context, ips []string) map[string]bool {
	if s != nil && s.datacenterClassifier != nil {
		return s.datacenterClassifier(ctx, ips)
	}
	return ipquality.ClassifyDatacenterIPs(ctx, ips)
}

// BlockDatacenterIPs appends the key's recent hosting/proxy IPs to its
// existing IP blacklist. It never removes entries and fails open when the
// classifier cannot be reached.
func (s *APIKeyService) BlockDatacenterIPs(ctx context.Context, id, userID int64) ([]string, int, error) {
	apiKey, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, 0, fmt.Errorf("get api key: %w", err)
	}
	if apiKey.UserID != userID {
		return nil, 0, ErrInsufficientPerms
	}
	return s.AppendDatacenterIPsToBlacklist(ctx, apiKey)
}

// AppendDatacenterIPsToBlacklist is the shared core of the one-shot endpoint and
// the periodic runner: it classifies the key's most recent client IPs and
// appends the hosting/proxy ones to apiKey.IPBlacklist. It performs no ownership
// check, so callers must load (or re-load) the key they are allowed to write.
//
// Contract: append-only, never removes an entry; an empty classification or a
// failed upstream lookup writes nothing and reports no error so the key keeps
// working. added is sorted for stable output; checked is the number of recent
// IPs inspected.
func (s *APIKeyService) AppendDatacenterIPsToBlacklist(ctx context.Context, apiKey *APIKey) ([]string, int, error) {
	if s == nil || apiKey == nil || apiKey.ID <= 0 {
		return nil, 0, nil
	}
	lister, ok := s.apiKeyRepo.(apiKeyRecentIPLister)
	if !ok {
		return nil, 0, fmt.Errorf("recent IP lookup is unavailable")
	}
	ips, err := lister.ListRecentIPAddresses(ctx, apiKey.ID, 200)
	if err != nil {
		return nil, 0, fmt.Errorf("list recent IPs: %w", err)
	}
	if len(ips) == 0 {
		return nil, 0, nil
	}
	datacenter := s.classifyDatacenterIPs(ctx, ips)
	if len(datacenter) == 0 {
		return nil, len(ips), nil
	}
	blocked := make(map[string]struct{}, len(apiKey.IPBlacklist))
	for _, pattern := range apiKey.IPBlacklist {
		blocked[strings.TrimSpace(pattern)] = struct{}{}
	}
	added := make([]string, 0, len(datacenter))
	for candidate := range datacenter {
		if _, exists := blocked[candidate]; exists {
			continue
		}
		blocked[candidate] = struct{}{}
		apiKey.IPBlacklist = append(apiKey.IPBlacklist, candidate)
		added = append(added, candidate)
	}
	if len(added) == 0 {
		return nil, len(ips), nil
	}
	if invalid := ip.ValidateIPPatterns(apiKey.IPBlacklist); len(invalid) > 0 {
		return nil, len(ips), fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
	}
	apiKey.CompiledIPBlacklist = ip.CompileIPRules(apiKey.IPBlacklist)
	if err := s.apiKeyRepo.Update(ctx, apiKey, APIKeyUpdateFields{IPRules: true}); err != nil {
		return nil, len(ips), fmt.Errorf("update api key: %w", err)
	}
	sort.Strings(added)
	return added, len(ips), nil
}
