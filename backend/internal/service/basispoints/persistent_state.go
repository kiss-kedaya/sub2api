package basispoints

import (
	"context"
	"encoding/json"
	"errors"
)

var ErrStateUnavailable = errors.New("basispoints shared session state is unavailable")
var ErrStateCapacity = errors.New("basispoints shared session state exceeds the entry limit")

// StateStore isolates data by account/key/session scope. Writes are durable
// before tool delivery; catalog writes compare the revision from the preceding read.
type StateStore interface {
	LoadBPSState(context.Context, string, string) ([]byte, uint64, error)
	SaveBPSState(context.Context, string, string, *uint64, []byte) (bool, error)
}

func NewPersistentCaches(ctx context.Context, store StateStore) (*ReplayCache, *CatalogCache) {
	return &ReplayCache{ctx: ctx, store: store}, &CatalogCache{ctx: ctx, store: store}
}

type persistedReplay struct {
	Raw       json.RawMessage `json:"raw"`
	Signature string          `json:"signature"`
}

func (c *ReplayCache) stateError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if errors.Is(err, ErrStateCapacity) {
		c.storeErr = ErrStateCapacity
	} else {
		c.storeErr = ErrStateUnavailable
	}
}
func (c *ReplayCache) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.storeErr
}
func (c *CatalogCache) stateError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if errors.Is(err, ErrStateCapacity) {
		c.storeErr = ErrStateCapacity
	} else {
		c.storeErr = ErrStateUnavailable
	}
}
func (c *CatalogCache) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.storeErr
}

func (c *ReplayCache) persist(scope, id string, item object, clientCall []object) {
	raw, err := json.Marshal(item)
	if err != nil || len(raw) > replayCacheEntryBytes {
		c.stateError(ErrStateCapacity)
		return
	}
	signature := ""
	if len(clientCall) == 1 {
		signature = historyCallFingerprint(clientCall[0])
	}
	encoded, err := json.Marshal(persistedReplay{raw, signature})
	if err != nil {
		c.stateError(err)
		return
	}
	changed, err := c.store.SaveBPSState(c.ctx, scope, "replay:"+id, nil, encoded)
	if err == nil && !changed {
		err = ErrStateUnavailable
	}
	c.stateError(err)
}
func (c *ReplayCache) restore(scope, id, signature string, requireSignature bool) object {
	raw, _, err := c.store.LoadBPSState(c.ctx, scope, "replay:"+id)
	if err != nil {
		c.stateError(err)
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	var entry persistedReplay
	if json.Unmarshal(raw, &entry) != nil {
		c.stateError(ErrStateUnavailable)
		return nil
	}
	if requireSignature && signature != entry.Signature {
		return nil
	}
	var item object
	if decode(entry.Raw, &item) != nil {
		c.stateError(ErrStateUnavailable)
		return nil
	}
	return item
}
