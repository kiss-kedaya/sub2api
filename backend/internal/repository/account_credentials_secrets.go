package repository

import (
	"context"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Account credentials contain upstream provider secrets (API keys, OAuth
// tokens, signed cookies, cloud keys). They are stored encrypted at rest inside
// the existing jsonb document: every value of a key listed in
// service.SensitiveCredentialKeys is replaced by deterministic AES-256-GCM
// ciphertext ("enc:v1:" + base64(nonce||sealed)). Non-secret fields such as
// base_url / model_mapping stay readable so the existing projections and
// compare-and-swap statements keep their SQL shape.
//
// This file owns the repository-side plumbing:
//   - decrypt on every service-facing account read (fail-closed);
//   - encrypt on every write, including raw-SQL compare-and-swap parameters;
//   - lazy rewrite of rows that are still legacy plaintext.
type accountCredentialRewriteTracker struct {
	mu       sync.Mutex
	inFlight map[int64]struct{}
}

func newAccountCredentialRewriteTracker() *accountCredentialRewriteTracker {
	return &accountCredentialRewriteTracker{inFlight: make(map[int64]struct{})}
}

func (t *accountCredentialRewriteTracker) begin(id int64) bool {
	if t == nil || id <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.inFlight[id]; exists {
		return false
	}
	t.inFlight[id] = struct{}{}
	return true
}

func (t *accountCredentialRewriteTracker) done(id int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.inFlight, id)
	t.mu.Unlock()
}

// decodeCredentials converts the stored JSONB document into service plaintext.
// A value that claims to be ciphertext must decrypt or the account read fails
// closed; it is never projected as an empty credential.
func (r *accountRepository) decodeCredentials(stored map[string]any) (map[string]any, bool, error) {
	if r == nil {
		return service.DecryptCredentialsFromStorage(nil, stored)
	}
	return service.DecryptCredentialsFromStorage(r.credentialVault, stored)
}

// encryptCredentials converts service plaintext into the storage document.
func (r *accountRepository) encryptCredentials(credentials map[string]any) (map[string]any, error) {
	if r == nil {
		return service.EncryptCredentialsForStorage(nil, credentials)
	}
	return service.EncryptCredentialsForStorage(r.credentialVault, credentials)
}

// wireCredentialsJSON marshals service plaintext credentials in storage form.
// Use it for every raw-SQL parameter that is compared against the credentials
// column (compare-and-swap and identity guards).
func (r *accountRepository) wireCredentialsJSON(credentials map[string]any) ([]byte, error) {
	if r == nil {
		return service.WireCredentialsJSON(nil, credentials)
	}
	return service.WireCredentialsJSON(r.credentialVault, credentials)
}

// wireCredentialsJSONFromBytes re-encrypts credentials that were already
// marshalled from service plaintext (for example a Grok mutation snapshot).
func (r *accountRepository) wireCredentialsJSONFromBytes(raw []byte) ([]byte, error) {
	if r == nil {
		return service.WireCredentialsJSONFromBytes(nil, raw)
	}
	return service.WireCredentialsJSONFromBytes(r.credentialVault, raw)
}

// scheduleCredentialsRewrite rewrites one account row that is still stored as
// legacy plaintext. It runs detached from the request so a read never waits for
// the write, and it is guarded by a compare-and-swap on the observed document so
// a concurrent credential rotation is never clobbered.
func (r *accountRepository) scheduleCredentialsRewrite(ctx context.Context, id int64, updatedAt time.Time, stored map[string]any) {
	if r == nil || r.sql == nil || id <= 0 || !service.SecretVaultAvailable(r.credentialVault) {
		return
	}
	if !r.credentialRewrite.begin(id) {
		return
	}
	encrypted, err := service.EncryptCredentialsForStorage(r.credentialVault, stored)
	if err != nil {
		r.credentialRewrite.done(id)
		return
	}
	base := context.WithoutCancel(ctx)
	go func() {
		defer r.credentialRewrite.done(id)
		rewriteCtx, cancel := context.WithTimeout(base, 15*time.Second)
		defer cancel()
		// Ent performs the write so the JSON encoding matches every dialect and
		// the existing hooks. The updated_at guard makes this a compare-and-swap:
		// a concurrent save bumps the timestamp and the stale rewrite backs off.
		predicates := []dbpredicate.Account{dbaccount.IDEQ(id), dbaccount.DeletedAtIsNil()}
		if r.credentialRewriteCAS {
			predicates = append(predicates, dbaccount.UpdatedAtEQ(updatedAt.UTC()))
		}
		_, err := r.client.Account.Update().
			Where(predicates...).
			SetCredentials(encrypted).
			Save(rewriteCtx)
		if err != nil {
			logger.LegacyPrintf("repository.account",
				"[Account] lazy credential encryption rewrite failed: account=%d err=%v", id, err)
		}
	}()
}

// useAccountCredentialVault wires the shared installation-key vault into a
// repository instance that was constructed through an existing test/DI path.
func useAccountCredentialVault(repo *accountRepository, vault service.SecretVault) {
	if repo == nil {
		return
	}
	repo.credentialVault = vault
	repo.credentialRewriteCAS = true
	if repo.credentialRewrite == nil {
		repo.credentialRewrite = newAccountCredentialRewriteTracker()
	}
}

// lockAndMergeAccountProbeExtra is the package-level compatibility entry point
// used by repository unit tests. A nil vault keeps historical plaintext fixtures
// readable; production always goes through accountRepository.
func lockAndMergeAccountProbeExtra(
	ctx context.Context,
	client *dbent.Client,
	account *service.Account,
	explicitProbeEnabled *bool,
	explicitRateSyncEnabled *bool,
) (map[string]any, error) {
	var repo accountRepository
	return repo.lockAndMergeAccountProbeExtra(ctx, client, account, explicitProbeEnabled, explicitRateSyncEnabled)
}
