//go:build embed

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// defaultInjectedHTMLCacheTTL is the fallback lifetime of the rendered
// index.html. It bounds staleness when a cross-instance invalidation broadcast
// is lost: the next request after expiry re-reads settings from the database.
const defaultInjectedHTMLCacheTTL = 45 * time.Second

// HTMLCache manages the cached index.html with injected settings.
//
// The entry is keyed on the settings version supplied by the settings service
// and additionally expires after ttl. Before this change the cache was only
// invalidated by a write handled *in this process*, so a replica that never
// wrote kept serving its own window.__APP_CONFIG__ forever.
type HTMLCache struct {
	mu              sync.RWMutex
	cachedHTML      []byte
	etag            string
	baseHTMLHash    string // Hash of the original index.html (immutable after build)
	renderedVersion uint64 // Settings version the cached HTML was rendered for
	expiresAt       time.Time
	ttl             time.Duration
}

// CachedHTML represents the cache state
type CachedHTML struct {
	Content []byte
	ETag    string
}

// NewHTMLCache creates a new HTML cache instance with the default TTL
func NewHTMLCache() *HTMLCache {
	return NewHTMLCacheWithTTL(defaultInjectedHTMLCacheTTL)
}

// NewHTMLCacheWithTTL creates a cache whose rendered HTML expires after ttl.
// A non-positive ttl falls back to the default so a misconfiguration can never
// make the injection permanent again.
func NewHTMLCacheWithTTL(ttl time.Duration) *HTMLCache {
	if ttl <= 0 {
		ttl = defaultInjectedHTMLCacheTTL
	}
	return &HTMLCache{ttl: ttl}
}

// SetBaseHTML initializes the cache with the base HTML template
func (c *HTMLCache) SetBaseHTML(baseHTML []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	hash := sha256.Sum256(baseHTML)
	c.baseHTMLHash = hex.EncodeToString(hash[:8]) // First 8 bytes for brevity
}

// Invalidate marks the cache as stale
func (c *HTMLCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cachedHTML = nil
	c.etag = ""
	c.renderedVersion = 0
	c.expiresAt = time.Time{}
}

// Get returns the cached HTML or nil when absent/expired.
func (c *HTMLCache) Get() *CachedHTML {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.getLocked(0, false)
}

// GetForVersion returns the cached HTML only when it was rendered for the given
// settings version and has not outlived its TTL. A nil result means the caller
// must re-read settings and re-render.
func (c *HTMLCache) GetForVersion(version uint64) *CachedHTML {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.getLocked(version, true)
}

func (c *HTMLCache) getLocked(version uint64, checkVersion bool) *CachedHTML {
	if c.cachedHTML == nil {
		return nil
	}
	if !c.expiresAt.IsZero() && !time.Now().Before(c.expiresAt) {
		return nil
	}
	if checkVersion && c.renderedVersion != version {
		return nil
	}
	return &CachedHTML{
		Content: c.cachedHTML,
		ETag:    c.etag,
	}
}

// ETag returns the ETag of the currently cached HTML, if any.
func (c *HTMLCache) ETag() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cachedHTML == nil {
		return ""
	}
	return c.etag
}

// Set updates the cache with new rendered HTML using the current settings
// version (0 means "unversioned"; GetForVersion(0) still matches).
func (c *HTMLCache) Set(html []byte, settingsJSON []byte) {
	c.SetForVersion(html, settingsJSON, 0)
}

// SetForVersion stores rendered HTML together with the settings version it was
// produced from, and restarts the TTL window.
func (c *HTMLCache) SetForVersion(html []byte, settingsJSON []byte, version uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cachedHTML = html
	c.renderedVersion = version
	c.etag = c.generateETag(settingsJSON)
	c.expiresAt = time.Now().Add(c.ttl)
}

// generateETag creates an ETag from base HTML hash + settings hash
func (c *HTMLCache) generateETag(settingsJSON []byte) string {
	settingsHash := sha256.Sum256(settingsJSON)
	return `"` + c.baseHTMLHash + "-" + hex.EncodeToString(settingsHash[:8]) + `"`
}
