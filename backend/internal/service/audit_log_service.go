package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	auditLogQueueCapacity = 4096
	auditLogBatchSize     = 100
	auditLogFlushInterval = time.Second

	auditRetentionCheckInterval = 24 * time.Hour
	auditRetentionStartupDelay  = 5 * time.Minute
	auditRetentionBatchSize     = 5000
)

// AuditLogService 管理面操作审计日志服务。
// 写入端为非阻塞异步批量落库（不拖慢管理请求）；
// 读取端提供分页查询；ClearAll 仅保留给内部/运维 CLI 使用，
// 管理端 HTTP 接口已禁止清理（固定 403）。
// 审计日志的常规回收走保留期清理（audit_log_retention_days）。
type AuditLogService struct {
	repo           AuditLogRepository
	settingService *SettingService

	queue chan *AuditLog

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	droppedCount uint64
	writeFailed  uint64
	writtenCount uint64
}

func NewAuditLogService(repo AuditLogRepository, settingService *SettingService) *AuditLogService {
	ctx, cancel := context.WithCancel(context.Background())
	return &AuditLogService{
		repo:           repo,
		settingService: settingService,
		queue:          make(chan *AuditLog, auditLogQueueCapacity),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start 启动异步写入与保留期清理协程。
func (s *AuditLogService) Start() {
	if s == nil || s.repo == nil {
		return
	}
	s.wg.Add(1)
	go s.runWriter()
	startBackgroundJob(func() {
		s.wg.Add(1)
		go s.runRetentionLoop()
	})
}

// Stop 停止服务并尽量落盘队列中剩余记录。
func (s *AuditLogService) Stop() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
}

// Record 非阻塞入队一条审计记录；队列打满时丢弃并计数（管理面流量下几乎不可能发生）。
func (s *AuditLogService) Record(entry *AuditLog) {
	if s == nil || entry == nil {
		return
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	select {
	case <-s.ctx.Done():
		return
	default:
	}
	select {
	case s.queue <- entry:
	default:
		atomic.AddUint64(&s.droppedCount, 1)
	}
}

// List 分页查询审计日志。
func (s *AuditLogService) List(ctx context.Context, filter *AuditLogFilter) (*AuditLogList, error) {
	return s.repo.List(ctx, filter)
}

// RecordNow 同步写入一条审计记录（绕过异步队列，落库后才返回）。
//
// 只给「低频 + 高风险 + 必须保证已留痕」的操作使用（例如破玻璃重置 2FA）：
// 这类操作如果走异步队列，接口返回成功时记录可能还在内存里，进程被重启就丢证据。
func (s *AuditLogService) RecordNow(ctx context.Context, entry *AuditLog) error {
	if s == nil || s.repo == nil || entry == nil {
		return nil
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	return s.repo.Insert(ctx, entry)
}

// GetByID 查询单条详情。
func (s *AuditLogService) GetByID(ctx context.Context, id int64) (*AuditLog, error) {
	return s.repo.GetByID(ctx, id)
}

// ClearAll 全量清空审计日志并写入留痕记录。
// 仅供内部/运维 CLI 使用：管理端 HTTP 接口已固定返回 403，
// 生产环境的人工清理只能由 DBA 在数据库层执行。本方法负责：
//  1. 统计并清空全表
//  2. 同步写入一条 "audit_log.clear" 留痕记录（绕过异步队列，保证落库）
func (s *AuditLogService) ClearAll(ctx context.Context, trace *AuditLog) (int64, error) {
	deleted, err := s.repo.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count audit logs: %w", err)
	}
	if err := s.repo.TruncateAll(ctx); err != nil {
		return 0, fmt.Errorf("truncate audit logs: %w", err)
	}

	if trace != nil {
		trace.Action = AuditActionAuditLogClear
		if trace.CreatedAt.IsZero() {
			trace.CreatedAt = time.Now().UTC()
		}
		if trace.Extra == nil {
			trace.Extra = map[string]any{}
		}
		trace.Extra["deleted_rows"] = deleted
		if err := s.repo.Insert(ctx, trace); err != nil {
			// 留痕失败必须显式暴露：清空已发生，但审计链断裂。
			return deleted, fmt.Errorf("audit logs cleared (%d rows) but failed to persist clear-trace record: %w", deleted, err)
		}
	}
	return deleted, nil
}

func (s *AuditLogService) runWriter() {
	defer s.wg.Done()

	ticker := time.NewTicker(auditLogFlushInterval)
	defer ticker.Stop()

	batch := make([]*AuditLog, 0, auditLogBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		inserted, err := s.repo.BatchInsert(ctx, batch)
		cancel()
		if err != nil {
			atomic.AddUint64(&s.writeFailed, uint64(len(batch)))
			_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"audit log flush failed\" err=%v batch=%d\n",
				time.Now().Format(time.RFC3339Nano), err, len(batch))
		} else {
			atomic.AddUint64(&s.writtenCount, uint64(inserted))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-s.ctx.Done():
			// 停机前排空队列。
			for {
				select {
				case item := <-s.queue:
					if item == nil {
						continue
					}
					batch = append(batch, item)
					if len(batch) >= auditLogBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case item := <-s.queue:
			if item == nil {
				continue
			}
			batch = append(batch, item)
			if len(batch) >= auditLogBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// runRetentionLoop 按保留期定期删除过期审计日志。
// 删除操作幂等，多实例并发执行无害，因此无需选主。
func (s *AuditLogService) runRetentionLoop() {
	defer s.wg.Done()

	startupTimer := time.NewTimer(auditRetentionStartupDelay)
	defer startupTimer.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-startupTimer.C:
	}

	ticker := time.NewTicker(auditRetentionCheckInterval)
	defer ticker.Stop()

	s.runRetentionOnce()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.runRetentionOnce()
		}
	}
}

func (s *AuditLogService) runRetentionOnce() {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()

	if s.settingService == nil {
		return
	}
	days, err := s.settingService.GetAuditLogRetentionDays(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"audit log cleanup skipped: cannot read retention policy\" err=%v\n",
			time.Now().Format(time.RFC3339Nano), err)
		return
	}
	if days <= 0 {
		return // Nonpositive retention keeps logs indefinitely.
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	for {
		deleted, err := s.repo.DeleteBefore(ctx, cutoff, auditRetentionBatchSize)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"audit log retention cleanup failed\" err=%v\n",
				time.Now().Format(time.RFC3339Nano), err)
			return
		}
		if deleted == 0 {
			return
		}
	}
}
