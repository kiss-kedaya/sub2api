package mihomo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SubscriptionStatus excludes full URLs, including tokens in provider paths.
// Stable IDs prevent stale browser lists from deleting a different entry.
type SubscriptionStatus struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Enabled   bool       `json:"enabled"`
	Nodes     int        `json:"nodes"`
	Cached    bool       `json:"cached"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type subscriptionCache struct {
	Nodes     []map[string]any  `json:"nodes"`
	Names     map[string]string `json:"names"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func subscriptionID(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:])
}

func subscriptionStatuses(s saved) []SubscriptionStatus {
	result := make([]SubscriptionStatus, 0, len(s.URLs))
	for i, address := range s.URLs {
		id := subscriptionID(address)
		cache, cached := s.SubscriptionCache[id]
		label := s.SubscriptionLabels[id]
		if label == "" {
			label = fmt.Sprintf("Subscription %d · %s", i+1, id[:8])
		}
		item := SubscriptionStatus{ID: id, Label: label, Enabled: !s.DisabledSubscriptions[id], Nodes: len(cache.Nodes), Cached: cached}
		if cached && !cache.UpdatedAt.IsZero() {
			updated := cache.UpdatedAt
			item.UpdatedAt = &updated
		}
		result = append(result, item)
	}
	return result
}

func sourceOperation(op string) bool {
	switch op {
	case "subscription_rename", "subscription_add", "subscription_refresh", "subscription_update", "subscription_remove", "subscription_enable", "subscription_disable", "dynamic_append", "dynamic_replace", "dynamic_remove", "dynamic_clear":
		return true
	}
	return false
}

func sourceTargetRequired(op string) bool {
	switch op {
	case "subscription_rename", "subscription_refresh", "subscription_update", "subscription_remove", "subscription_enable", "subscription_disable", "dynamic_remove":
		return true
	}
	return false
}

// Called under the manager mutex. Copy before mutation so failed operations
// cannot change the running configuration through shared slices or maps.
func prepareSourceChange(next saved, op, target string, urls, dynamic []string) (saved, error) {
	next.URLs = append([]string{}, next.URLs...)
	next.DynamicProxies = append([]string{}, next.DynamicProxies...)
	disabled := make(map[string]bool, len(next.DisabledSubscriptions))
	for k, v := range next.DisabledSubscriptions {
		disabled[k] = v
	}
	next.DisabledSubscriptions = disabled
	cache := make(map[string]subscriptionCache, len(next.SubscriptionCache))
	for k, v := range next.SubscriptionCache {
		cache[k] = v
	}
	next.SubscriptionCache = cache
	labels := make(map[string]string, len(next.SubscriptionLabels))
	for k, v := range next.SubscriptionLabels {
		labels[k] = v
	}
	next.SubscriptionLabels = labels
	if strings.HasPrefix(op, "subscription_") && sourceTargetRequired(op) {
		index := -1
		for i, address := range next.URLs {
			if subscriptionID(address) == target {
				index = i
				break
			}
		}
		if index < 0 {
			return next, errors.New("subscription no longer exists; refresh the list")
		}
		switch op {
		case "subscription_update":
			if len(urls) != 1 {
				return next, errors.New("exactly one replacement subscription is required")
			}
			for i, address := range next.URLs {
				if i != index && address == urls[0] {
					return next, errors.New("subscription already exists")
				}
			}
			next.URLs[index] = urls[0]
			newID := subscriptionID(urls[0])
			wasDisabled := disabled[target]
			oldLabel := labels[target]
			delete(cache, target)
			delete(disabled, target)
			delete(labels, target)
			disabled[newID] = wasDisabled
			labels[newID] = oldLabel
		case "subscription_remove":
			next.URLs = append(next.URLs[:index], next.URLs[index+1:]...)
			delete(disabled, target)
			delete(cache, target)
			delete(labels, target)
		case "subscription_disable":
			disabled[target] = true
		case "subscription_enable":
			delete(disabled, target)
		case "subscription_refresh":
			if disabled[target] {
				return next, errors.New("enable the subscription before refreshing it")
			}
			delete(cache, target)
		}
	}
	var err error
	switch op {
	case "subscription_add":
		if len(urls) == 0 {
			return next, errors.New("a subscription is required")
		}
		next.URLs, err = normalizeURLs(append(next.URLs, urls...))
	case "dynamic_append", "dynamic_replace":
		if len(dynamic) == 0 {
			return next, errors.New("a dynamic proxy is required")
		}
		if op == "dynamic_append" {
			dynamic = append(next.DynamicProxies, dynamic...)
		}
		next.DynamicProxies, err = normalizeDynamicProxies(dynamic)
	case "dynamic_clear":
		next.DynamicProxies = nil
	case "dynamic_remove":
		found := false
		for i, address := range next.DynamicProxies {
			nodes, _, nodeErr := dynamicProxyNodes([]string{address})
			if nodeErr != nil {
				return next, nodeErr
			}
			if len(nodes) == 1 && nodes[0]["name"] == target {
				next.DynamicProxies = append(next.DynamicProxies[:i], next.DynamicProxies[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return next, errors.New("dynamic proxy no longer exists; refresh the list")
		}
	}
	return next, err
}

// Cache each source independently: removing a broken subscription never fetches
// it again. Other cached sources remain usable even when their providers fail.
// Older settings populate missing caches on the first successful source update.
func (m *Manager) resolveSources(ctx context.Context, next *saved, refreshAll bool) error {
	cache := make(map[string]subscriptionCache, len(next.URLs))
	disabled := make(map[string]bool)
	labels := make(map[string]string)
	nodes := []map[string]any{}
	names := map[string]string{}
	seen := map[string]bool{}
	for _, address := range next.URLs {
		id := subscriptionID(address)
		if label := next.SubscriptionLabels[id]; label != "" {
			labels[id] = label
		}
		entry, ok := next.SubscriptionCache[id]
		if next.DisabledSubscriptions[id] {
			disabled[id] = true
			if ok {
				cache[id] = entry
			}
			continue
		}
		if refreshAll || !ok {
			fetched, nodeNames, err := m.fetchNodes(ctx, []string{address})
			if err != nil {
				return fmt.Errorf("subscription %s: %w", id[:8], err)
			}
			entry = subscriptionCache{Nodes: fetched, Names: nodeNames, UpdatedAt: time.Now().UTC()}
		}
		cache[id] = entry
		for _, node := range entry.Nodes {
			name, _ := node["name"].(string)
			if !seen[name] {
				nodes = append(nodes, node)
				names[name] = entry.Names[name]
				seen[name] = true
			}
		}
		if len(nodes) > 1000 {
			return errors.New("at most 1000 subscription nodes")
		}
	}
	if len(next.DynamicProxies) > 0 {
		dynamic, nodeNames, err := dynamicProxyNodes(next.DynamicProxies)
		if err != nil {
			return err
		}
		nodes = append(nodes, dynamic...)
		for name, label := range nodeNames {
			names[name] = label
		}
	}
	next.Nodes = nodes
	next.NodeNames = names
	next.SubscriptionCache = cache
	next.DisabledSubscriptions = disabled
	next.SubscriptionLabels = labels
	return nil
}

func subscriptionNodeSourceIndex(s saved) map[string][]string {
	result := make(map[string][]string)
	for _, address := range s.URLs {
		id := subscriptionID(address)
		for _, node := range s.SubscriptionCache[id].Nodes {
			if name, ok := node["name"].(string); ok {
				result[name] = append(result[name], id)
			}
		}
	}
	return result
}
