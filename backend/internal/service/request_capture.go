package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
)

const SettingKeyRequestCapture = "request_capture_config"

// CaptureSettings is independent of gateway and billing configuration. An
// omitted setting never enables body collection on an existing installation.
type CaptureSettings struct {
	Enabled       bool  `json:"enabled"`
	QuotaMiB      int64 `json:"quota_mib"`
	RetentionDays int   `json:"retention_days"`
}

func (c CaptureSettings) captureConfig() requestcapture.Config {
	return requestcapture.Config{Enabled: c.Enabled, QuotaMiB: c.QuotaMiB, RetentionDays: c.RetentionDays}
}

func (s *SettingService) GetCaptureSettings(ctx context.Context) (CaptureSettings, error) {
	c := CaptureSettings{QuotaMiB: 1024, RetentionDays: 7}
	if s == nil || s.settingRepo == nil {
		return c, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyRequestCapture)
	if errors.Is(err, ErrSettingNotFound) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return c, err
		}
	}
	return c, c.captureConfig().Validate()
}

func (s *SettingService) UpdateCaptureSettings(ctx context.Context, c CaptureSettings) error {
	if err := c.captureConfig().Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err = s.settingRepo.SetMultiple(ctx, map[string]string{SettingKeyRequestCapture: string(raw)}); err != nil {
		return err
	}
	if s.requestCaptureRuntime != nil {
		s.requestCaptureRuntime.manager.ApplyConfig(c.captureConfig())
	}
	return nil
}

type captureRuntime struct {
	manager *requestcapture.Manager
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

// InitializeRequestCapture is called once during router composition, after
// migration validation. Optional diagnostic storage must not stop the gateway.
func (s *SettingService) InitializeRequestCapture(db *sql.DB) (*requestcapture.Manager, error) {
	if db == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c, err := s.GetCaptureSettings(ctx)
	cancel()
	if err != nil {
		return nil, err
	}
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	// Concurrent rolling versions must not share one capture identity or stop
	// each other's active tasks. Stable instance storage is explicitly selectable.
	instance := os.Getenv("SUB2API_CAPTURE_INSTANCE")
	if instance == "" {
		instance = "pid-" + strconv.Itoa(os.Getpid())
	}
	if filepath.Base(instance) != instance || instance == "." || instance == ".." {
		return nil, errors.New("invalid capture instance identifier")
	}
	m, err := requestcapture.New(&requestcapture.SQLStore{DB: db}, filepath.Join(dir, "request-captures", instance), c.captureConfig())
	if err != nil {
		return nil, err
	}
	ctx, cancel = context.WithCancel(context.Background())
	r := &captureRuntime{manager: m, cancel: cancel, done: make(chan struct{})}
	s.requestCaptureRuntime = r
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				readCtx, stop := context.WithTimeout(ctx, 2*time.Second)
				next, e := s.GetCaptureSettings(readCtx)
				stop()
				if e == nil {
					m.ApplyConfig(next.captureConfig())
				}
			}
		}
	}()
	return m, nil
}

func (s *SettingService) StopRequestCapture() {
	if s == nil || s.requestCaptureRuntime == nil {
		return
	}
	r := s.requestCaptureRuntime
	r.once.Do(func() { r.cancel(); <-r.done; r.manager.Close() })
}
