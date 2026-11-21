package service

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

type managedProxyDeletionContextKey struct{}

func withManagedProxyDeletion(ctx context.Context) context.Context {
	return context.WithValue(ctx, managedProxyDeletionContextKey{}, true)
}

// proxySQLExecutor is the minimal raw-SQL surface the wrapper needs for the lazy
// plaintext→ciphertext rewrite.
// ProxySQLExecutor is the raw-SQL surface used by the lazy plaintext rewrite.
type ProxySQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type encryptedProxyRepository struct {
	ProxyRepository
	vault SecretVault
	exec  ProxySQLExecutor

	rewriteMu     sync.Mutex
	rewriteFlight map[int64]struct{}
}

// WrapEncryptedProxyRepository encrypts every proxy password at rest while
// keeping the ProxyRepository contract plaintext for services. Managed proxy
// credentials keep their release guard; the v2 prefix replaces the previous
// managed-only format and legacy rows are rewritten lazily.
func WrapEncryptedProxyRepository(inner ProxyRepository, vault SecretVault, exec ProxySQLExecutor) ProxyRepository {
	return &encryptedProxyRepository{
		ProxyRepository: inner,
		vault:           vault,
		exec:            exec,
		rewriteFlight:   make(map[int64]struct{}),
	}
}

func (r *encryptedProxyRepository) decode(p *Proxy) error {
	if p == nil {
		return nil
	}
	needsRewrite, err := DecodeProxyPasswordInPlace(p, r.vault)
	if err != nil {
		return err
	}
	if needsRewrite {
		r.scheduleRewrite(p.ID, p.Password)
	}
	return nil
}

func (r *encryptedProxyRepository) encode(p *Proxy) (*Proxy, error) {
	copy := *p
	if copy.Password == "" {
		return &copy, nil
	}
	if secretcryptIsEncrypted(copy.Password) {
		return nil, errors.New("proxy ciphertext is not an input credential")
	}
	encrypted, err := EncryptProxyPassword(r.vault, copy.Password)
	if err != nil {
		return nil, err
	}
	copy.Password = encrypted
	return &copy, nil
}

// scheduleRewrite re-encrypts one legacy row off the request path. The CAS on
// the previously observed value keeps a concurrent password rotation intact.
func (r *encryptedProxyRepository) scheduleRewrite(id int64, plaintext string) {
	if r == nil || r.exec == nil || id <= 0 || plaintext == "" || !SecretVaultAvailable(r.vault) {
		return
	}
	encrypted, err := EncryptProxyPassword(r.vault, plaintext)
	if err != nil || encrypted == "" {
		return
	}
	r.rewriteMu.Lock()
	if r.rewriteFlight == nil {
		r.rewriteFlight = make(map[int64]struct{})
	}
	if _, exists := r.rewriteFlight[id]; exists {
		r.rewriteMu.Unlock()
		return
	}
	r.rewriteFlight[id] = struct{}{}
	r.rewriteMu.Unlock()
	go func() {
		defer func() {
			r.rewriteMu.Lock()
			delete(r.rewriteFlight, id)
			r.rewriteMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := r.exec.ExecContext(ctx, `
			UPDATE proxies SET password = $1, updated_at = NOW()
			WHERE id = $2 AND password IS NOT DISTINCT FROM $3 AND deleted_at IS NULL
		`, encrypted, id, plaintext); err != nil {
			logger.LegacyPrintf("repository.proxy",
				"[Proxy] lazy password encryption rewrite failed: proxy=%d err=%v", id, err)
		}
	}()
}

func (r *encryptedProxyRepository) decodeList(p []Proxy, err error) ([]Proxy, error) {
	if err != nil {
		return nil, err
	}
	for i := range p {
		if err = r.decode(&p[i]); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (r *encryptedProxyRepository) decodeCounts(p []ProxyWithAccountCount, err error) ([]ProxyWithAccountCount, error) {
	if err != nil {
		return nil, err
	}
	for i := range p {
		if err = r.decode(&p[i].Proxy); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (r *encryptedProxyRepository) Create(ctx context.Context, p *Proxy) error {
	copy, err := r.encode(p)
	if err != nil {
		return err
	}
	if err = r.ProxyRepository.Create(ctx, copy); err != nil {
		return err
	}
	if err = r.decode(copy); err != nil {
		return err
	}
	*p = *copy
	return nil
}

func (r *encryptedProxyRepository) Update(ctx context.Context, p *Proxy) error {
	copy, err := r.encode(p)
	if err != nil {
		return err
	}
	if err = r.ProxyRepository.Update(ctx, copy); err != nil {
		return err
	}
	if err = r.decode(copy); err != nil {
		return err
	}
	*p = *copy
	return nil
}

func (r *encryptedProxyRepository) GetByID(ctx context.Context, id int64) (*Proxy, error) {
	p, err := r.ProxyRepository.GetByID(ctx, id)
	if err == nil {
		err = r.decode(p)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *encryptedProxyRepository) Delete(ctx context.Context, id int64) error {
	p, err := r.ProxyRepository.GetByID(ctx, id)
	if err != nil {
		return err
	}
	allowed, _ := ctx.Value(managedProxyDeletionContextKey{}).(bool)
	if p != nil && IsManagedProxyUsername(p.Username) && !allowed {
		return infraerrors.Conflict("MANAGED_PROXY_RELEASE_REQUIRED", "Remove this static proxy from its managed node page so its listener can be closed safely")
	}
	return r.ProxyRepository.Delete(ctx, id)
}

func (r *encryptedProxyRepository) ListByIDs(ctx context.Context, ids []int64) ([]Proxy, error) {
	p, e := r.ProxyRepository.ListByIDs(ctx, ids)
	return r.decodeList(p, e)
}

func (r *encryptedProxyRepository) ListActive(ctx context.Context) ([]Proxy, error) {
	p, e := r.ProxyRepository.ListActive(ctx)
	return r.decodeList(p, e)
}

func (r *encryptedProxyRepository) ListAllForFallback(ctx context.Context) ([]Proxy, error) {
	p, e := r.ProxyRepository.ListAllForFallback(ctx)
	return r.decodeList(p, e)
}

func (r *encryptedProxyRepository) ListActiveWithAccountCount(ctx context.Context) ([]ProxyWithAccountCount, error) {
	p, e := r.ProxyRepository.ListActiveWithAccountCount(ctx)
	return r.decodeCounts(p, e)
}

func (r *encryptedProxyRepository) List(ctx context.Context, q pagination.PaginationParams) ([]Proxy, *pagination.PaginationResult, error) {
	p, page, e := r.ProxyRepository.List(ctx, q)
	p, e = r.decodeList(p, e)
	return p, page, e
}

func (r *encryptedProxyRepository) ListWithFilters(ctx context.Context, q pagination.PaginationParams, protocol, status, search string) ([]Proxy, *pagination.PaginationResult, error) {
	p, page, e := r.ProxyRepository.ListWithFilters(ctx, q, protocol, status, search)
	p, e = r.decodeList(p, e)
	return p, page, e
}

func (r *encryptedProxyRepository) ListWithFiltersAndAccountCount(ctx context.Context, q pagination.PaginationParams, protocol, status, search string) ([]ProxyWithAccountCount, *pagination.PaginationResult, error) {
	p, page, e := r.ProxyRepository.ListWithFiltersAndAccountCount(ctx, q, protocol, status, search)
	p, e = r.decodeCounts(p, e)
	return p, page, e
}

// ExistsByHostPortAuth compares plaintext credentials in Go: random-nonce
// ciphertext cannot be compared inside SQL without a plaintext blind index.
func (r *encryptedProxyRepository) ExistsByHostPortAuth(ctx context.Context, host string, port int, user, password string) (bool, error) {
	rows, err := r.ListAllForFallback(ctx)
	if err != nil {
		return false, err
	}
	for i := range rows {
		p := &rows[i]
		if p.Host != host || p.Port != port || p.Username != user {
			continue
		}
		if password == "" {
			if p.Password == "" {
				return true, nil
			}
			continue
		}
		if p.Password == password {
			return true, nil
		}
	}
	return false, nil
}
