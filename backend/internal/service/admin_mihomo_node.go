package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

// MihomoNodeProber exposes one Mihomo node through a short-lived local proxy
// and keeps the latest check result. *mihomo.Manager implements it.
type MihomoNodeProber interface {
	ProbeNode(ctx context.Context, name string, check func(ctx context.Context, proxyURL string)) error
	RecordNodeCheck(name string, check mihomo.NodeCheck)
}

// TestMihomoNode runs the proxy list's connection test through one Mihomo node.
// The result is kept by the kernel manager, never in the proxy latency cache,
// and node state, rotation and region rules stay unchanged.
func (s *adminServiceImpl) TestMihomoNode(ctx context.Context, kernel MihomoNodeProber, name string) (*ProxyTestResult, error) {
	var result *ProxyTestResult
	err := kernel.ProbeNode(ctx, name, func(ctx context.Context, proxyURL string) {
		// The listener disappears with its kernel; drop the pooled clients for it.
		defer httpclient.EvictProxyClients(proxyURL)
		result = s.testProxyURL(ctx, proxyURL)
	})
	if err == nil && result == nil {
		err = errors.New("node check did not run")
	}
	if err != nil {
		return nil, mihomoNodeCheckError(err)
	}
	kernel.RecordNodeCheck(name, mihomoNodeCheck(proxyTestLatencyInfo(result)))
	return result, nil
}

// CheckMihomoNodeQuality runs the proxy list's quality check through one
// Mihomo node, with the same targets, scoring and summary.
func (s *adminServiceImpl) CheckMihomoNodeQuality(ctx context.Context, kernel MihomoNodeProber, name string) (*ProxyQualityCheckResult, error) {
	var result *ProxyQualityCheckResult
	var exitInfo *ProxyExitInfo
	err := kernel.ProbeNode(ctx, name, func(ctx context.Context, proxyURL string) {
		defer httpclient.EvictProxyClients(proxyURL)
		result, exitInfo = s.checkProxyURLQuality(ctx, proxyURL)
	})
	if err == nil && result == nil {
		err = errors.New("node check did not run")
	}
	if err != nil {
		return nil, mihomoNodeCheckError(err)
	}
	kernel.RecordNodeCheck(name, mihomoNodeCheck(proxyQualityLatencyInfo(result, exitInfo)))
	return result, nil
}

// mihomoNodeCheck presents a latency snapshot the way the proxy list does.
func mihomoNodeCheck(info *ProxyLatencyInfo) mihomo.NodeCheck {
	check := mihomo.NodeCheck{
		CheckedAt:      info.UpdatedAt.Unix(),
		LatencyStatus:  "failed",
		LatencyMessage: info.Message,
		IPAddress:      info.IPAddress,
		Country:        info.Country,
		CountryCode:    info.CountryCode,
		Region:         info.Region,
		City:           info.City,
		QualityStatus:  info.QualityStatus,
		QualityScore:   info.QualityScore,
		QualityGrade:   info.QualityGrade,
		QualitySummary: info.QualitySummary,
		QualityChecked: info.QualityCheckedAt,
	}
	if info.Success {
		check.LatencyStatus = "success"
		check.LatencyMs = info.LatencyMs
	}
	return check
}

func mihomoNodeCheckError(err error) error {
	if errors.Is(err, mihomo.ErrUnknownNode) {
		return infraerrors.NotFound("MIHOMO_NODE_NOT_FOUND", "node no longer exists; refresh the list")
	}
	return infraerrors.Conflict("MIHOMO_NODE_CHECK_UNAVAILABLE", err.Error())
}
