package main

import (
	"context"
	"log"
	"sync"
	"time"
)

// auditEntry records one signer operation. It must never contain key
// material; only addresses, amounts and outcomes.
type auditEntry struct {
	Time    string `json:"time"`
	Op      string `json:"op"`
	Kind    string `json:"kind,omitempty"`
	Index   uint32 `json:"index"`
	ChainID int64  `json:"chain_id,omitempty"`
	To      string `json:"to,omitempty"`
	Token   string `json:"token,omitempty"`
	Amount  string `json:"amount,omitempty"`
	Result  string `json:"result"`
	Reason  string `json:"reason,omitempty"`
}

type auditLog struct {
	mu    sync.Mutex
	store *store
}

func newAuditLog(st *store) *auditLog {
	return &auditLog{store: st}
}

func (a *auditLog) write(entry auditEntry) {
	entry.Time = time.Now().UTC().Format(time.RFC3339)
	if a == nil || a.store == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.store.writeAudit(ctx, entry); err != nil {
		// An unwritable audit trail must be visible, not silent.
		log.Printf("signer audit: %v", err)
	}
}
