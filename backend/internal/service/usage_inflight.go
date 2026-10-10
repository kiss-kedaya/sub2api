package service

import (
	"context"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// UsageInflightSnapshot 是一条还没写进 usage_logs 的在途请求。
// 只放 Redis。请求结束就删，避免和落库行重复。
type UsageInflightSnapshot struct {
	RequestID        string    `json:"request_id"`
	UserID           int64     `json:"user_id"`
	APIKeyID         int64     `json:"api_key_id"`
	GroupID          int64     `json:"group_id,omitempty"`
	AccountID        int64     `json:"account_id,omitempty"`
	Email            string    `json:"email,omitempty"`
	APIKeyName       string    `json:"api_key_name,omitempty"`
	Model            string    `json:"model,omitempty"`
	ReasoningEffort  string    `json:"reasoning_effort,omitempty"`
	InboundEndpoint  string    `json:"inbound_endpoint,omitempty"`
	UpstreamEndpoint string    `json:"upstream_endpoint,omitempty"`
	IPAddress        string    `json:"ip_address,omitempty"`
	Stream           bool      `json:"stream,omitempty"`
	WebSocket        bool      `json:"websocket,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	FirstTokenMs     *int      `json:"first_token_ms,omitempty"`
	InputTokens      int       `json:"input_tokens"`
	OutputTokens     int       `json:"output_tokens"`
	ReservedAmount   float64   `json:"reserved_amount"`
	ExpiresAtMs      int64     `json:"expires_at_ms"`
}

// UsageInflightStore 在途请求的 Redis 读写。Redis 不可用时实现应直接返回，不能挡请求。
type UsageInflightStore interface {
	Save(ctx context.Context, snap UsageInflightSnapshot) error
	Delete(ctx context.Context, userID int64, requestID string) error
	ListUser(ctx context.Context, userID int64) ([]UsageInflightSnapshot, error)
	ListAll(ctx context.Context) ([]UsageInflightSnapshot, error)
}

type usageInflightBox struct {
	store UsageInflightStore
}

var usageInflightStore atomic.Pointer[usageInflightBox]

// SetUsageInflightStore 在路由启动时挂上。nil 表示关闭。
func SetUsageInflightStore(store UsageInflightStore) {
	if store == nil {
		usageInflightStore.Store(nil)
		return
	}
	usageInflightStore.Store(&usageInflightBox{store: store})
}

func currentUsageInflightStore() UsageInflightStore {
	box := usageInflightStore.Load()
	if box == nil {
		return nil
	}
	return box.store
}

// SaveUsageInflight 写入或刷新一条在途记录。失败只丢这一次刷新。
func SaveUsageInflight(ctx context.Context, snap UsageInflightSnapshot) {
	store := currentUsageInflightStore()
	if store == nil || snap.UserID <= 0 || strings.TrimSpace(snap.RequestID) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 200*time.Millisecond)
	defer cancel()
	_ = store.Save(saveCtx, snap)
}

// DeleteUsageInflight 请求结束时删掉在途记录。
func DeleteUsageInflight(ctx context.Context, userID int64, requestID string) {
	store := currentUsageInflightStore()
	if store == nil || userID <= 0 || strings.TrimSpace(requestID) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 200*time.Millisecond)
	defer cancel()
	_ = store.Delete(delCtx, userID, requestID)
}

// QueryIncludesUsageInflight 列表是否带上在途行。空值默认带。导出传 false。
func QueryIncludesUsageInflight(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}
	return value
}

// VisibleUsageInflight 取出当前用户（userID>0）或全部在途请求，并按列表筛选收窄。
// userID 为 0 时读在途用户集合，不扫整库。
func VisibleUsageInflight(ctx context.Context, userID int64, filters usagestats.UsageLogFilters, finished map[string]struct{}) []UsageInflightSnapshot {
	store := currentUsageInflightStore()
	if store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := 300 * time.Millisecond
	if userID <= 0 {
		timeout = time.Second
	}
	listCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	var (
		rows []UsageInflightSnapshot
		err  error
	)
	if userID > 0 {
		rows, err = store.ListUser(listCtx, userID)
	} else {
		rows, err = store.ListAll(listCtx)
	}
	if err != nil || len(rows) == 0 {
		return nil
	}
	return filterUsageInflight(rows, filters, finished)
}

func filterUsageInflight(rows []UsageInflightSnapshot, filters usagestats.UsageLogFilters, finished map[string]struct{}) []UsageInflightSnapshot {
	if filters.BillingType != nil || strings.TrimSpace(filters.BillingMode) != "" || filters.UpstreamModelMismatch != nil {
		return nil
	}
	if filters.NativeCompactionV2 != nil && *filters.NativeCompactionV2 {
		return nil
	}
	out := make([]UsageInflightSnapshot, 0, len(rows))
	model := strings.TrimSpace(filters.Model)
	requestID := strings.TrimSpace(filters.RequestID)
	for _, row := range rows {
		if row.RequestID == "" {
			continue
		}
		if _, done := finished[row.RequestID]; done {
			continue
		}
		if requestID != "" && !strings.Contains(strings.ToLower(row.RequestID), strings.ToLower(requestID)) {
			continue
		}
		if filters.APIKeyID != 0 && row.APIKeyID != filters.APIKeyID {
			continue
		}
		if filters.AccountID != 0 && row.AccountID != filters.AccountID {
			continue
		}
		if filters.GroupID != 0 && row.GroupID != filters.GroupID {
			continue
		}
		if model != "" && row.Model != model {
			continue
		}
		if filters.StartTime != nil && row.StartedAt.Before(*filters.StartTime) {
			continue
		}
		if filters.EndTime != nil && !row.StartedAt.Before(*filters.EndTime) {
			continue
		}
		if filters.Stream != nil && *filters.Stream != row.Stream {
			continue
		}
		if filters.RequestType != nil && !inflightRequestTypeMatches(row, RequestType(*filters.RequestType)) {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

func inflightRequestTypeMatches(row UsageInflightSnapshot, kind RequestType) bool {
	switch kind {
	case RequestTypeStream:
		return row.Stream && !row.WebSocket
	case RequestTypeSync:
		return !row.Stream && !row.WebSocket
	case RequestTypeWSV2:
		return row.WebSocket
	default:
		return false
	}
}

const usageInflightPageSize = 10

// PageUsageInflight 固定每页 10 条。调用方要下一页就再传 page。
func PageUsageInflight(rows []UsageInflightSnapshot, page, pageSize int) ([]UsageInflightSnapshot, int) {
	if page < 1 {
		page = 1
	}
	if pageSize != usageInflightPageSize {
		pageSize = usageInflightPageSize
	}
	total := len(rows)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return rows[start:end], total
}

// InflightUsageRowID 给在途行一个负数主键，避免和已落库 id 撞车，也避免多条都是 0。
func InflightUsageRowID(requestID string) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(requestID))
	id := int64(hash.Sum64() & 0x7fffffffffffffff)
	if id == 0 {
		id = 1
	}
	return -id
}
