package service

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// 账号断路器与 RateLimitService 的衔接：熔断动作复用现有的「临时不可调度」机制。
//
//   - 写库：AccountRepository.SetTempUnschedulable。SQL 里 until 取 max，并且只更新
//     temp_unschedulable_*，所以不会缩短更长的手动/规则冷却，也不会碰手动 schedulable=false；
//     同一事务里写 scheduler outbox 并同步调度器账号快照，所以调度选号、粘连会话清理、
//     failover 排除集、后台账号列表都和其它临时不可调度来源一视同仁。
//   - OpenAI 平台额外通知进程内运行时阻断（BlockAccountScheduling），弥合快照传播的那几毫秒。
//   - 管理员手动清除限流/临时不可调度（notifyAccountSchedulingBlockCleared）时同步重置断路器，
//     测试成功触发的自动恢复走的也是这条路。

var errCircuitBreakerNoRepo = errors.New("account circuit breaker: account repository is not configured")

// SchedulerAccountRefresher 是账号仓库的可选接口：往调度器 outbox 写一条账号变更事件，
// 让调度快照重建这个账号所在的桶。断路器在冷却到期后用它把账号放回桶里（见 scheduleRecoveryRefresh）。
type SchedulerAccountRefresher interface {
	RefreshSchedulerAccount(ctx context.Context, accountID int64) error
}

// SetAccountCircuitBreaker 挂上断路器（可选依赖；为 nil 时所有查询都按「没有熔断」处理）。
func (s *RateLimitService) SetAccountCircuitBreaker(b *AccountCircuitBreaker) {
	s.circuitBreaker = b
}

// AccountCircuitBreaker 返回已挂的断路器，可能为 nil。
func (s *RateLimitService) AccountCircuitBreaker() *AccountCircuitBreaker {
	if s == nil {
		return nil
	}
	return s.circuitBreaker
}

// AccountCircuitOpen 报告账号当前是否处于本进程断路器的冷却期。
// 重试/故障转移循环在再次打同一个账号之前调用：账号被熔断后，在途请求立刻停止对它的重试。
func (s *RateLimitService) AccountCircuitOpen(accountID int64) bool {
	if s == nil {
		return false
	}
	return s.circuitBreaker.IsOpen(accountID)
}

// persistAccountCircuitTrip 是断路器的持久化回调：把账号写成临时不可调度。
func (s *RateLimitService) persistAccountCircuitTrip(ctx context.Context, trip AccountCircuitTrip) error {
	if s == nil || s.accountRepo == nil {
		return errCircuitBreakerNoRepo
	}
	if err := s.accountRepo.SetTempUnschedulable(ctx, trip.AccountID, trip.Until, trip.Reason); err != nil {
		return err
	}
	// 只用 ID/平台/名字：运行时阻断不需要完整账号，也就不必在熔断路径上多一次读库。
	s.notifyAccountSchedulingBlocked(&Account{ID: trip.AccountID, Platform: trip.Platform, Name: trip.AccountName}, trip.Until, "circuit_breaker")
	logger.L().With(zap.String("component", "service.account_circuit_breaker")).Info(
		"account_circuit_breaker.persisted",
		zap.Int64("account_id", trip.AccountID),
		zap.Time("until", trip.Until),
		zap.Duration("cooldown", trip.Cooldown.Round(time.Second)),
	)
	return nil
}

// AccountCircuitOpen 让 handler 的重试循环通过 GatewayService 查询断路器（见 handler.accountCircuitGate）。
func (s *GatewayService) AccountCircuitOpen(accountID int64) bool {
	return s != nil && s.rateLimitService.AccountCircuitOpen(accountID)
}

// CircuitRecoveryIn 返回最早结束冷却的熔断账号还要多久恢复（见 AccountCircuitBreaker.EarliestRecovery）。
func (s *RateLimitService) CircuitRecoveryIn() (time.Duration, bool) {
	if s == nil {
		return 0, false
	}
	return s.circuitBreaker.EarliestRecovery()
}

// CircuitRecoveryIn 让 handler 给「没有可用账号」的 503 算 Retry-After。
func (s *GatewayService) CircuitRecoveryIn() (time.Duration, bool) {
	if s == nil {
		return 0, false
	}
	return s.rateLimitService.CircuitRecoveryIn()
}

// CircuitRecoveryIn 让 OpenAI 系 handler 给「没有可用账号」的 503 算 Retry-After。
func (s *OpenAIGatewayService) CircuitRecoveryIn() (time.Duration, bool) {
	if s == nil {
		return 0, false
	}
	return s.rateLimitService.CircuitRecoveryIn()
}

// AccountCircuitOpen 让 OpenAI 系 handler 的重试循环通过 OpenAIGatewayService 查询断路器。
func (s *OpenAIGatewayService) AccountCircuitOpen(accountID int64) bool {
	return s != nil && s.rateLimitService.AccountCircuitOpen(accountID)
}
