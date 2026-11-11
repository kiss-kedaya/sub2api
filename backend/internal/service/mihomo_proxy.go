package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	mihomoProxyNamePrefix = "Mihomo · "
	// proxies.name is validated as at most 100 bytes by the entity layer, so the
	// display name has to be byte-truncated after the fixed prefix.
	proxyNameMaxBytes = 100
)

type MihomoBindingRepository interface {
	WithHostLock(context.Context, string, func() error) error
	GetProxyID(context.Context, string, string) (int64, error)
	Save(context.Context, string, string, int64) error
}
type MihomoStaticNodeBinder interface {
	BindStaticNodes(context.Context, []string) ([]mihomo.StaticNodeBinding, error)
	DeactivateStaticNodes(context.Context, []string) error
	HostID() string
}
type MihomoNodeProxyImportFailure struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
type MihomoNodeProxyImportResult struct {
	Created  []Proxy                        `json:"-"`
	Existing []Proxy                        `json:"-"`
	Failed   []MihomoNodeProxyImportFailure `json:"failed"`
}
type MihomoProxyService struct {
	admin    AdminService
	proxies  ProxyRepository
	bindings MihomoBindingRepository
}

func NewMihomoProxyService(admin AdminService, proxies ProxyRepository, bindings MihomoBindingRepository) *MihomoProxyService {
	return &MihomoProxyService{admin, proxies, bindings}
}

type mihomoNodeChecker interface {
	TestMihomoNode(context.Context, MihomoNodeProber, string) (*ProxyTestResult, error)
	CheckMihomoNodeQuality(context.Context, MihomoNodeProber, string) (*ProxyQualityCheckResult, error)
}

func (s *MihomoProxyService) TestMihomoNode(ctx context.Context, m MihomoNodeProber, name string) (*ProxyTestResult, error) {
	v, ok := s.admin.(mihomoNodeChecker)
	if !ok {
		return nil, errors.New("node tests unavailable")
	}
	return v.TestMihomoNode(ctx, m, name)
}
func (s *MihomoProxyService) CheckMihomoNodeQuality(ctx context.Context, m MihomoNodeProber, name string) (*ProxyQualityCheckResult, error) {
	v, ok := s.admin.(mihomoNodeChecker)
	if !ok {
		return nil, errors.New("node tests unavailable")
	}
	return v.CheckMihomoNodeQuality(ctx, m, name)
}

func (s *MihomoProxyService) CreateMihomoNodeProxies(ctx context.Context, kernel MihomoStaticNodeBinder, names []string) (*MihomoNodeProxyImportResult, error) {
	if len(names) == 0 || len(names) > 100 {
		return nil, infraerrors.BadRequest("MIHOMO_SELECTION_INVALID", "select between 1 and 100 nodes")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result := &MihomoNodeProxyImportResult{Created: []Proxy{}, Existing: []Proxy{}, Failed: []MihomoNodeProxyImportFailure{}}
	err := s.bindings.WithHostLock(ctx, kernel.HostID(), func() (runErr error) {
		bound, err := kernel.BindStaticNodes(ctx, names)
		if err != nil {
			return infraerrors.Conflict("MIHOMO_BIND_FAILED", err.Error())
		}
		deactivate := []string{}
		defer func() {
			if len(deactivate) == 0 {
				return
			}
			clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			if err := kernel.DeactivateStaticNodes(clean, deactivate); err != nil {
				runErr = errors.New("unregistered managed listeners could not be disabled")
			}
		}()
		for _, b := range bound {
			id, lookupErr := s.bindings.GetProxyID(ctx, kernel.HostID(), b.Name)
			if lookupErr != nil {
				if b.New {
					deactivate = append(deactivate, b.Name)
				}
				result.Failed = append(result.Failed, MihomoNodeProxyImportFailure{b.Name, "could not read proxy binding"})
				continue
			}
			if id > 0 {
				p, e := s.proxies.GetByID(ctx, id)
				if e == nil && p != nil && p.Protocol == "http" && p.Host == b.Host && p.Port == b.Port && p.Username == b.Username && p.Password == b.Password {
					result.Existing = append(result.Existing, *p)
					continue
				}
				if b.New {
					deactivate = append(deactivate, b.Name)
				}
				result.Failed = append(result.Failed, MihomoNodeProxyImportFailure{b.Name, "saved proxy no longer matches its managed listener"})
				continue
			}
			name := mihomoProxyName(b.DisplayName, b.Name)
			p, e := s.admin.CreateProxy(ctx, &CreateProxyInput{Name: name, Protocol: "http", Host: b.Host, Port: b.Port, Username: b.Username, Password: b.Password, FallbackMode: FallbackModeNone, ExpiryWarnDays: 7})
			if e == nil {
				e = s.bindings.Save(ctx, kernel.HostID(), b.Name, p.ID)
			}
			if e != nil {
				slog.Error("managed_proxy_registration_failed", "node", b.Name, "error", e)
				if b.New {
					deactivate = append(deactivate, b.Name)
				}
				if p != nil {
					clean, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					deleteErr := s.admin.DeleteProxy(withManagedProxyDeletion(clean), p.ID)
					c()
					if deleteErr != nil {
						return errors.New("managed proxy registration failed and its row needs cleanup")
					}
				}
				result.Failed = append(result.Failed, MihomoNodeProxyImportFailure{b.Name, "could not register encrypted managed proxy"})
				continue
			}
			result.Created = append(result.Created, *p)
		}
		return nil
	})
	return result, err
}

// RemoveMihomoNodeProxies runs on the listener's selected host and verifies
// every known row before closing any listener. Already-deleted rows can still
// release an orphan listener. Ordinary proxy-list deletion is denied upstream.
func (s *MihomoProxyService) RemoveMihomoNodeProxies(ctx context.Context, kernel MihomoStaticNodeBinder, names []string) (int, error) {
	if len(names) == 0 || len(names) > 100 {
		return 0, infraerrors.BadRequest("MIHOMO_SELECTION_INVALID", "select between 1 and 100 nodes")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	removed := 0
	err := s.bindings.WithHostLock(ctx, kernel.HostID(), func() error {
		type selected struct {
			name string
			id   int64
		}
		rows := make([]selected, 0, len(names))
		seen := map[string]bool{}
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			id, err := s.bindings.GetProxyID(ctx, kernel.HostID(), name)
			if err != nil {
				return errors.New("could not read managed proxy binding")
			}
			if id > 0 {
				count, err := s.proxies.CountAccountsByProxyID(ctx, id)
				if err != nil {
					return errors.New("could not verify managed proxy references")
				}
				if count > 0 {
					return ErrProxyInUse
				}
			}
			rows = append(rows, selected{name, id})
		}
		for _, row := range rows {
			if err := kernel.DeactivateStaticNodes(ctx, []string{row.name}); err != nil {
				return infraerrors.Conflict("MIHOMO_RELEASE_FAILED", "Managed proxy listener could not be closed")
			}
			if row.id > 0 {
				if err := s.admin.DeleteProxy(withManagedProxyDeletion(ctx), row.id); err != nil {
					clean, release := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
					_, restoreErr := kernel.BindStaticNodes(clean, []string{row.name})
					release()
					if restoreErr != nil {
						return infraerrors.Conflict("MIHOMO_RELEASE_RESTORE_FAILED", "Proxy deletion failed and its listener needs recovery on this host")
					}
					return err
				}
			}
			removed++
		}
		return nil
	})
	return removed, err
}

// truncateUTF8Bytes shortens s to at most max bytes without splitting a
// multi-byte UTF-8 sequence. Callers use it when a column limit is measured in
// bytes rather than runes.
func truncateUTF8Bytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

// mihomoProxyName builds the display name stored in proxies.name. The entity
// layer limits that column to 100 bytes, so the variable part is truncated on a
// rune boundary after accounting for the fixed prefix.
func mihomoProxyName(displayName, fallback string) string {
	label := strings.TrimSpace(displayName)
	if label == "" {
		label = fallback
	}
	return mihomoProxyNamePrefix + truncateUTF8Bytes(label, proxyNameMaxBytes-len(mihomoProxyNamePrefix))
}
