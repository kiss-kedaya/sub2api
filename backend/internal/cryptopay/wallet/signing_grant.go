package wallet

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

var ErrSigningAuthorizationRequired = errors.New("wallet signing requires an active sweep authorization")

type signingGrantContextKey struct{}
type signingGrant struct {
	nonce       [32]byte
	expires     int64
	destination string
	mac         []byte
}

// This authority is random per process, independent of the seed-encryption
// master, and never written to settings, environment, logs or API responses.
func (m *Manager) initializeSigningAuthority() {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		clear(key)
		return
	}
	m.signingAuthority = key
	m.signingGrants = map[[32]byte]bool{}
}

func (m *Manager) revokeSigningGrants() { clear(m.signingGrants) }

func (m *Manager) grantMAC(grant *signingGrant) []byte {
	mac := hmac.New(sha256.New, m.signingAuthority)
	_, _ = mac.Write([]byte("sub2api/crypto-wallet/sweep-capability/v1:"))
	_, _ = mac.Write(grant.nonce[:])
	var expires [8]byte
	binary.BigEndian.PutUint64(expires[:], uint64(grant.expires))
	_, _ = mac.Write(expires[:])
	_, _ = mac.Write([]byte(grant.destination))
	return mac.Sum(nil)
}

// AuthorizeSweep is called only by the internal sweep facade: manual HTTP
// requests reach it after fresh 2FA; the automatic scheduler is a trusted
// internal caller. It never unlocks an incident or enables a payment channel.
func (m *Manager) AuthorizeSweep(ctx context.Context) (context.Context, func(), error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled || m.destination == "" {
		return nil, nil, ErrSigningDisabled
	}
	if m.securityEnabled && (m.securityIncident != nil || m.securityStatus.Locked || !m.securityStatus.Initialized) {
		return nil, nil, ErrSecurityLocked
	}
	if len(m.signingAuthority) != 32 {
		m.initializeSigningAuthority()
	}
	if len(m.signingAuthority) != 32 {
		return nil, nil, ErrVaultUnavailable
	}
	expires := time.Now().Add(30 * time.Minute)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(expires) {
		expires = deadline
	}
	grant := &signingGrant{expires: expires.UnixNano(), destination: m.destination}
	if _, err := rand.Read(grant.nonce[:]); err != nil {
		return nil, nil, ErrVaultUnavailable
	}
	grant.mac = m.grantMAC(grant)
	m.signingGrants[grant.nonce] = true
	authorized, cancel := context.WithDeadline(ctx, expires)
	var once sync.Once
	revoke := func() {
		once.Do(func() { cancel(); m.mu.Lock(); defer m.mu.Unlock(); delete(m.signingGrants, grant.nonce) })
	}
	return context.WithValue(authorized, signingGrantContextKey{}, grant), revoke, nil
}

// Called with the manager mutex held, immediately before any key is used.
func (m *Manager) validSigningGrant(ctx context.Context) bool {
	grant, ok := ctx.Value(signingGrantContextKey{}).(*signingGrant)
	return ok && grant != nil && ctx.Err() == nil && len(m.signingAuthority) == 32 && m.signingGrants[grant.nonce] &&
		grant.expires > time.Now().UnixNano() && grant.destination == m.destination && hmac.Equal(grant.mac, m.grantMAC(grant))
}

// ValidateSweepAuthorization also guards rebroadcast of bytes signed before
// an incident. Previously broadcast transactions cannot be recalled on chain.
func (m *Manager) ValidateSweepAuthorization(ctx context.Context) error {
	_, err := m.GuardSweepBroadcast(ctx, func() (string, error) { return "", nil })
	return err
}

// GuardSweepBroadcast keeps the same shared vault/config locks as signing
// through the actual network send. A concurrent incident cannot commit its
// exclusive lock and then be crossed by a previously validated broadcast.
// The callback must only send, never reenter Manager or wait for finality.
func (m *Manager) GuardSweepBroadcast(ctx context.Context, send func() (string, error)) (string, error) {
	if err := mode.CheckWritable(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.securityEnabled {
		return send()
	}
	if !m.enabled {
		return "", ErrSigningDisabled
	}
	if !m.validSigningGrant(ctx) {
		return "", ErrSigningAuthorizationRequired
	}
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := m.checkSecurityInTx(ctx, tx); err != nil {
		return "", err
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((value::jsonb->>'enabled')::boolean,false) FROM settings WHERE key=$1 FOR SHARE`, "crypto.config").Scan(&enabled); err != nil || !enabled {
		return "", ErrSigningDisabled
	}
	return send()
}
