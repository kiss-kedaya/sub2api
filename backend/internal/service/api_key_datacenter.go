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
	lister, ok := s.apiKeyRepo.(apiKeyRecentIPLister)
	if !ok {
		return nil, 0, fmt.Errorf("recent IP lookup is unavailable")
	}
	ips, err := lister.ListRecentIPAddresses(ctx, id, 200)
	if err != nil {
		return nil, 0, fmt.Errorf("list recent IPs: %w", err)
	}
	if len(ips) == 0 {
		return nil, 0, nil
	}
	datacenter := ipquality.ClassifyDatacenterIPs(ctx, ips)
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
