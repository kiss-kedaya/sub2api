package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
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
			label := []rune(strings.TrimSpace(b.DisplayName))
			if len(label) > 90 {
				label = label[:90]
			}
			if len(label) == 0 {
				label = []rune(b.Name)
			}
			p, e := s.admin.CreateProxy(ctx, &CreateProxyInput{Name: "Mihomo · " + string(label), Protocol: "http", Host: b.Host, Port: b.Port, Username: b.Username, Password: b.Password, FallbackMode: FallbackModeNone, ExpiryWarnDays: 7})
			if e == nil {
				e = s.bindings.Save(ctx, kernel.HostID(), b.Name, p.ID)
			}
			if e != nil {
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
