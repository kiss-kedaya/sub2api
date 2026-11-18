package service

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
)

var errSmallSimpleMode = errors.New("simple mode is not billed")

func (s *SettingService) SmallRequestRecorder() *requestcapture.SmallRecorder {
	if s == nil {
		return nil
	}
	return s.smallRequestRecorder
}
func (s *SettingService) initializeSmallRequests(db *sql.DB, enabled bool) {
	if s.smallRequestRecorder != nil {
		return
	}
	r := requestcapture.NewSmallRecorder(&requestcapture.SmallStore{DB: db}, func(name string) bool { return opsHeaderCredentialDenylist[name] })
	r.ApplyEnabled(enabled)
	s.smallRequestRecorder = r
	// Independent of local-file capture's optional instance identity.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Done():
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				config, err := s.GetCaptureSettings(ctx)
				cancel()
				if err == nil {
					r.ApplyEnabled(config.SmallRequestEnabled)
				}
			}
		}
	}()
}

// Observe the final accounting row only. No provider usage parsing, estimation
// rules or repository lookup belongs in this optional retention side channel.
func observeSmallUsage(ctx context.Context, log *UsageLog, account *Account, billingErr error, applied bool) {
	h := requestcapture.SmallFromContext(ctx)
	if h == nil || log == nil {
		return
	}
	u := requestcapture.SmallUsage{RequestID: log.RequestID, UserID: log.UserID, APIKeyID: log.APIKeyID, AccountID: log.AccountID, Model: log.RequestedModel, Stream: log.Stream, InputTokens: log.InputTokens, OutputTokens: log.OutputTokens, CacheReadTokens: log.CacheReadTokens, CacheCreationTokens: log.CacheCreationTokens, CalculatedCost: strconv.FormatFloat(log.ActualCost, 'f', 8, 64), ActualCost: "0.00000000", Settlement: "failed"}
	if account != nil {
		u.Platform = account.Platform
	}
	if log.GroupID != nil {
		u.GroupID = *log.GroupID
	}
	if log.UpstreamModel != nil {
		u.UpstreamModel = *log.UpstreamModel
	}
	if u.Model == "" {
		u.Model = log.Model
	}
	if log.DurationMs != nil {
		u.DurationMs = *log.DurationMs
	}
	if billingErr == nil {
		u.Settlement = "already_settled"
		if applied {
			u.ActualCost = u.CalculatedCost
			u.Settlement = "settled"
		}
	}
	if errors.Is(billingErr, errSmallSimpleMode) {
		u.Settlement = "not_billed"
	}
	h.Usage(u)
}
