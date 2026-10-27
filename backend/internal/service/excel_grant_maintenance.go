package service

import (
	"context"
	"time"
)

type excelGrantMaintenanceRepository interface{ SweepExpired(context.Context) error }

func (s *ExcelGrantService) Start() {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.cancel != nil {
		return
	}
	maintenance, ok := s.repo.(excelGrantMaintenanceRepository)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				bounded, c := context.WithTimeout(ctx, 10*time.Second)
				_ = maintenance.SweepExpired(bounded)
				c()
			}
		}
	}()
}
func (s *ExcelGrantService) Stop() {
	s.lifecycle.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.lifecycle.Unlock()
	s.wg.Wait()
}
