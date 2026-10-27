package service

import "github.com/Wei-Shaw/sub2api/internal/accounttimer"

// SchedulableTimers is supplied by the production account repository. It keeps
// the timer storage separate from model tests, cooldown and quality policies.
func (s *adminServiceImpl) SchedulableTimers() accounttimer.Repository {
	if provider, ok := s.accountRepo.(interface {
		SchedulableTimers() accounttimer.Repository
	}); ok {
		return provider.SchedulableTimers()
	}
	return nil
}
