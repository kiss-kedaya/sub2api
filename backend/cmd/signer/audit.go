package main

import (
	"encoding/json"
	"os"
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
	mu   sync.Mutex
	path string
}

func newAuditLog(path string) *auditLog {
	return &auditLog{path: path}
}

func (a *auditLog) write(entry auditEntry) {
	entry.Time = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}
