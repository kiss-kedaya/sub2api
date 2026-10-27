package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const managedProxyCipherPrefix = "enc:managed:v1:"

type encryptedManagedProxyRepository struct {
	ProxyRepository
	encryptor SecretEncryptor
}

// WrapEncryptedManagedProxyRepository preserves historical ordinary proxies.
// Managed credentials are plaintext only inside service values, never in rows.
func WrapEncryptedManagedProxyRepository(inner ProxyRepository, encryptor SecretEncryptor) ProxyRepository {
	return &encryptedManagedProxyRepository{ProxyRepository: inner, encryptor: encryptor}
}

func (r *encryptedManagedProxyRepository) decode(p *Proxy) error {
	return DecodeManagedProxyCredential(p, r.encryptor)
}

// DecodeManagedProxyCredential is also used by account eager-loading; those
// reads intentionally bypass ProxyRepository and must never emit ciphertext.
func DecodeManagedProxyCredential(p *Proxy, encryptor SecretEncryptor) error {
	if p == nil || !strings.HasPrefix(p.Username, "mh_") || !strings.HasPrefix(p.Password, managedProxyCipherPrefix) {
		return nil
	}
	if encryptor == nil {
		return errors.New("managed proxy decryption is unavailable")
	}
	plain, err := encryptor.Decrypt(strings.TrimPrefix(p.Password, managedProxyCipherPrefix))
	if err != nil || plain == "" {
		return errors.New("managed proxy credential cannot be decrypted")
	}
	p.Password = plain
	return nil
}
func (r *encryptedManagedProxyRepository) encode(p *Proxy) (*Proxy, error) {
	copy := *p
	if !strings.HasPrefix(copy.Username, "mh_") || copy.Password == "" {
		return &copy, nil
	}
	if strings.HasPrefix(copy.Password, managedProxyCipherPrefix) {
		return nil, errors.New("managed proxy ciphertext is not an input credential")
	}
	if r.encryptor == nil {
		return nil, errors.New("managed proxy encryption is unavailable")
	}
	cipher, err := r.encryptor.Encrypt(copy.Password)
	if err != nil || cipher == "" {
		return nil, errors.New("managed proxy credential cannot be encrypted")
	}
	copy.Password = managedProxyCipherPrefix + cipher
	return &copy, nil
}
func (r *encryptedManagedProxyRepository) Create(ctx context.Context, p *Proxy) error {
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
func (r *encryptedManagedProxyRepository) Update(ctx context.Context, p *Proxy) error {
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
func (r *encryptedManagedProxyRepository) GetByID(ctx context.Context, id int64) (*Proxy, error) {
	p, err := r.ProxyRepository.GetByID(ctx, id)
	if err == nil {
		err = r.decode(p)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}
func (r *encryptedManagedProxyRepository) decodeList(p []Proxy, err error) ([]Proxy, error) {
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
func (r *encryptedManagedProxyRepository) decodeCounts(p []ProxyWithAccountCount, err error) ([]ProxyWithAccountCount, error) {
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
func (r *encryptedManagedProxyRepository) ListByIDs(ctx context.Context, ids []int64) ([]Proxy, error) {
	p, e := r.ProxyRepository.ListByIDs(ctx, ids)
	return r.decodeList(p, e)
}
func (r *encryptedManagedProxyRepository) ListActive(ctx context.Context) ([]Proxy, error) {
	p, e := r.ProxyRepository.ListActive(ctx)
	return r.decodeList(p, e)
}
func (r *encryptedManagedProxyRepository) ListAllForFallback(ctx context.Context) ([]Proxy, error) {
	p, e := r.ProxyRepository.ListAllForFallback(ctx)
	return r.decodeList(p, e)
}
func (r *encryptedManagedProxyRepository) ListActiveWithAccountCount(ctx context.Context) ([]ProxyWithAccountCount, error) {
	p, e := r.ProxyRepository.ListActiveWithAccountCount(ctx)
	return r.decodeCounts(p, e)
}
func (r *encryptedManagedProxyRepository) List(ctx context.Context, q pagination.PaginationParams) ([]Proxy, *pagination.PaginationResult, error) {
	p, page, e := r.ProxyRepository.List(ctx, q)
	p, e = r.decodeList(p, e)
	return p, page, e
}
func (r *encryptedManagedProxyRepository) ListWithFilters(ctx context.Context, q pagination.PaginationParams, protocol, status, search string) ([]Proxy, *pagination.PaginationResult, error) {
	p, page, e := r.ProxyRepository.ListWithFilters(ctx, q, protocol, status, search)
	p, e = r.decodeList(p, e)
	return p, page, e
}
func (r *encryptedManagedProxyRepository) ListWithFiltersAndAccountCount(ctx context.Context, q pagination.PaginationParams, protocol, status, search string) ([]ProxyWithAccountCount, *pagination.PaginationResult, error) {
	p, page, e := r.ProxyRepository.ListWithFiltersAndAccountCount(ctx, q, protocol, status, search)
	p, e = r.decodeCounts(p, e)
	return p, page, e
}
func (r *encryptedManagedProxyRepository) ExistsByHostPortAuth(ctx context.Context, host string, port int, user, password string) (bool, error) {
	if !strings.HasPrefix(user, "mh_") {
		return r.ProxyRepository.ExistsByHostPortAuth(ctx, host, port, user, password)
	}
	rows, err := r.ListAllForFallback(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range rows {
		if p.Host == host && p.Port == port && p.Username == user && p.Password == password {
			return true, nil
		}
	}
	return false, nil
}
