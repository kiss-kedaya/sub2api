// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const liveTransferFenceKeyPrefix = "concurrency:live_transfer_closed:api_key:"

func liveTransferFenceKey(keyID int64) string {
	return fmt.Sprintf("%s%d", liveTransferFenceKeyPrefix, keyID)
}

var (
	// transferLiveLeaseScript 是一次原子的精确三维交接：把本次句柄持有的普通
	// account/user/key member 移入同一个 Live lease，不依赖 allowance，也不会对
	// 同一维度重复计数。任一必需来源缺失即失败关闭；同一 leaseID 的三维成员
	// 已齐备时视为幂等重放。
	//
	// KEYS: 1=accountRegular, 2=accountLive, 3=userRegular, 4=userLive,
	//       5=apiRegular, 6=apiLive, 7=fence
	// ARGV: 1=accountMax, 2=userMax, 3=apiMax, 4=accountSource, 5=userSource,
	//       6=keySource, 7=leaseID, 8=liveTTL, 9=slotTTL
	// 返回: 1=LIVE, 2=Key 满, 3=Key 来源丢失, 4=账号满, 5=账号来源丢失,
	//       6=用户满, 7=用户来源丢失, 8=已中止, 9=部分 Live 状态冲突
	transferLiveLeaseScript = redis.NewScript(`
		redis.replicate_commands()
		local accountRegular = KEYS[1]
		local accountLive = KEYS[2]
		local userRegular = KEYS[3]
		local userLive = KEYS[4]
		local apiRegular = KEYS[5]
		local apiLive = KEYS[6]
		local fence = KEYS[7]
		local accountMax = tonumber(ARGV[1])
		local userMax = tonumber(ARGV[2])
		local apiMax = tonumber(ARGV[3])
		local accountSource = ARGV[4]
		local userSource = ARGV[5]
		local keySource = ARGV[6]
		local leaseID = ARGV[7]
		local ttl = tonumber(ARGV[8])
		local slotTTL = tonumber(ARGV[9])
		local t = redis.call('TIME')
		local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
		local now = math.floor(nowMs / 1000)
		redis.call('ZREMRANGEBYSCORE', accountLive, '-inf', now - ttl)
		redis.call('ZREMRANGEBYSCORE', userLive, '-inf', now - ttl)
		redis.call('ZREMRANGEBYSCORE', apiLive, '-inf', now - ttl)
		redis.call('ZREMRANGEBYSCORE', apiRegular, '-inf', now - slotTTL)
		redis.call('ZREMRANGEBYSCORE', fence, '-inf', nowMs)

		if redis.call('ZSCORE', fence, leaseID) ~= false then
			return 8
		end
		local accountLiveExists = redis.call('ZSCORE', accountLive, leaseID) ~= false
		local userLiveExists = redis.call('ZSCORE', userLive, leaseID) ~= false
		local apiLiveExists = redis.call('ZSCORE', apiLive, leaseID) ~= false
		if accountLiveExists and userLiveExists and apiLiveExists then
			return 1
		end
		if accountLiveExists or userLiveExists or apiLiveExists then
			return 9
		end

		if accountMax > 0 then
			if accountSource == '' or redis.call('ZSCORE', accountRegular, accountSource) == false then
				return 5
			end
		end
		if userMax > 0 then
			if userSource == '' or redis.call('ZSCORE', userRegular, userSource) == false then
				return 7
			end
		end
		if apiMax > 0 then
			if keySource == '' or redis.call('ZSCORE', apiRegular, keySource) == false then
				return 3
			end
		end

		local accountCount = redis.call('ZCARD', accountRegular) + redis.call('ZCARD', accountLive)
		local userCount = redis.call('ZCARD', userRegular) + redis.call('ZCARD', userLive)
		local apiCount = redis.call('ZCARD', apiRegular) + redis.call('ZCARD', apiLive)
		if accountMax > 0 then
			accountCount = accountCount - 1
			if accountCount >= accountMax then
				return 4
			end
		end
		if userMax > 0 then
			userCount = userCount - 1
			if userCount >= userMax then
				return 6
			end
		end
		if apiMax > 0 then
			apiCount = apiCount - 1
			if apiCount >= apiMax then
				return 2
			end
		end

		if accountMax > 0 then
			redis.call('ZREM', accountRegular, accountSource)
		end
		if userMax > 0 then
			redis.call('ZREM', userRegular, userSource)
		end
		-- A stats-only key member is moved as well, so the unlimited tracking
		-- worker is not left behind as an orphan alongside the Live member.
		if keySource ~= '' then
			redis.call('ZREM', apiRegular, keySource)
		end
		redis.call('ZADD', accountLive, now, leaseID)
		redis.call('ZADD', userLive, now, leaseID)
		redis.call('ZADD', apiLive, now, leaseID)
		redis.call('EXPIRE', accountLive, ttl)
		redis.call('EXPIRE', userLive, ttl)
		redis.call('EXPIRE', apiLive, ttl)
		return 1
	`)

	// migrateLiveLeaseAccountScript 保留同一 Live 租约的 Key/user 成员，只把失败
	// 账号的 Live 成员原子替换为新账号的普通预留。任一既有联合成员缺失或新账号
	// 来源缺失都失败关闭；不重新入队 Key，也不释放 user 所有权。
	//
	// KEYS: 1=newAccountRegular, 2=newAccountLive, 3=oldAccountLive, 4=userLive,
	//       5=apiLive
	// ARGV: 1=accountMax, 2=accountSource, 3=leaseID, 4=liveTTL, 5=slotTTL
	// 返回: 1=LIVE, 2=联合租约丢失, 3=新账号来源丢失, 4=账号满
	migrateLiveLeaseAccountScript = redis.NewScript(`
		redis.replicate_commands()
		local t = redis.call('TIME')
		local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
		local now = math.floor(nowMs / 1000)
		local accountMax = tonumber(ARGV[1])
		local accountSource = ARGV[2]
		local leaseID = ARGV[3]
		local ttl = tonumber(ARGV[4])
		local slotTTL = tonumber(ARGV[5])
		redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now - ttl)
		redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', now - ttl)
		redis.call('ZREMRANGEBYSCORE', KEYS[5], '-inf', now - ttl)
		redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - slotTTL)

		if redis.call('ZSCORE', KEYS[4], leaseID) == false or redis.call('ZSCORE', KEYS[5], leaseID) == false then
			return 2
		end
		if accountMax > 0 then
			if accountSource == '' or redis.call('ZSCORE', KEYS[1], accountSource) == false then
				return 3
			end
			local accountCount = redis.call('ZCARD', KEYS[1]) + redis.call('ZCARD', KEYS[2])
			accountCount = accountCount - 1
			if accountCount >= accountMax then
				return 4
			end
		end
		if accountMax > 0 then
			redis.call('ZREM', KEYS[1], accountSource)
		end
		redis.call('ZREM', KEYS[3], leaseID)
		redis.call('ZADD', KEYS[2], now, leaseID)
		redis.call('ZADD', KEYS[4], now, leaseID)
		redis.call('ZADD', KEYS[5], now, leaseID)
		redis.call('EXPIRE', KEYS[2], ttl)
		redis.call('EXPIRE', KEYS[4], ttl)
		redis.call('EXPIRE', KEYS[5], ttl)
		return 1
	`)

	// abortLiveLeaseTransferScript 用独立的栅栏期限封住本 leaseID 的迟到交接，
	// 再精确删除本次三维来源与 Live 成员；它不会触碰其他 lease 或 attempt。
	// 栅栏先于删除写入，Redis 原子执行的同时保证：若清除先到，迟到交接被拒；
	// 若交接先到，本脚本会把它删除。
	//
	// KEYS: 1=accountRegular, 2=userRegular, 3=apiRegular, 4=accountLive,
	//       5=userLive, 6=apiLive, 7=fence
	// ARGV: 1=accountSource, 2=userSource, 3=keySource, 4=leaseID, 5=fenceTTL
	abortLiveLeaseTransferScript = redis.NewScript(`
		redis.replicate_commands()
		local t = redis.call('TIME')
		local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
		local fenceTTL = tonumber(ARGV[5])
		local fenceUntil = nowMs + fenceTTL * 1000
		redis.call('ZADD', KEYS[7], fenceUntil, ARGV[4])
		redis.call('EXPIRE', KEYS[7], fenceTTL)
		if ARGV[1] ~= '' then redis.call('ZREM', KEYS[1], ARGV[1]) end
		if ARGV[2] ~= '' then redis.call('ZREM', KEYS[2], ARGV[2]) end
		if ARGV[3] ~= '' then redis.call('ZREM', KEYS[3], ARGV[3]) end
		redis.call('ZREM', KEYS[4], ARGV[4])
		redis.call('ZREM', KEYS[5], ARGV[4])
		redis.call('ZREM', KEYS[6], ARGV[4])
		return 1
	`)
)

// AcquireLiveLeaseTransferring atomically moves the exact regular account,
// user and key reservations into the joint Live lease, so no dimension is
// counted twice. A missing required source or an aborted handoff fails closed
// instead of inventing capacity.
func (c *concurrencyCache) AcquireLiveLeaseTransferring(ctx context.Context, request service.LiveLeaseTransferRequest) (bool, error) {
	if c == nil || c.rdb == nil || request.AccountID <= 0 || request.UserID <= 0 || request.APIKeyID <= 0 || request.LeaseID == "" {
		return false, nil
	}
	result, err := transferLiveLeaseScript.Run(ctx, c.rdb, []string{
		accountSlotKey(request.AccountID),
		liveAccountSlotKey(request.AccountID),
		userSlotKey(request.UserID),
		liveUserSlotKey(request.UserID),
		apiKeySlotKey(request.APIKeyID),
		liveAPIKeySlotKey(request.APIKeyID),
		liveTransferFenceKey(request.APIKeyID),
	},
		request.AccountMax,
		request.UserMax,
		request.APIKeyMax,
		request.AccountRequestID,
		request.UserRequestID,
		request.KeyRequestID,
		request.LeaseID,
		liveLeaseTTLSeconds,
		c.slotTTLSeconds,
	).Int()
	if err != nil {
		// The script may have executed even if the reply was lost; the caller
		// must abort with the exact identities instead of assuming failure.
		return false, fmt.Errorf("%w: %v", service.ErrLiveLeaseTransferUncertain, err)
	}
	switch result {
	case 1:
		return true, nil
	case 2:
		return false, service.ErrAPIKeyConcurrencyLimit
	case 3:
		return false, service.ErrAPIKeyReservationLost
	case 4, 6:
		return false, service.ErrLiveConcurrencyFull
	case 5, 7:
		return false, service.ErrLiveLeaseSourceLost
	case 8:
		return false, service.ErrLiveLeaseTransferFenced
	case 9:
		return false, fmt.Errorf("%w: partial Live lease state for %s", service.ErrLiveUnavailable, request.LeaseID)
	default:
		return false, fmt.Errorf("%w: unexpected Live transfer result %d", service.ErrLiveUnavailable, result)
	}
}

// MigrateLiveLeaseAccount keeps the joint Key/user Live members and atomically
// replaces the failed account member with a new ordinary account reservation.
// A missing joint member or account source fails closed; the new ordinary
// member is removed only on success, so the caller's release path stays exact.
func (c *concurrencyCache) MigrateLiveLeaseAccount(ctx context.Context, request service.LiveLeaseTransferRequest) (bool, error) {
	if c == nil || c.rdb == nil || request.AccountID <= 0 || request.UserID <= 0 || request.APIKeyID <= 0 || request.LeaseID == "" {
		return false, nil
	}
	result, err := migrateLiveLeaseAccountScript.Run(ctx, c.rdb, []string{
		accountSlotKey(request.AccountID),
		liveAccountSlotKey(request.AccountID),
		liveAccountSlotKey(request.ReplacedAccountID),
		liveUserSlotKey(request.UserID),
		liveAPIKeySlotKey(request.APIKeyID),
	},
		request.AccountMax,
		request.AccountRequestID,
		request.LeaseID,
		liveLeaseTTLSeconds,
		c.slotTTLSeconds,
	).Int()
	if err != nil {
		return false, fmt.Errorf("%w: %v", service.ErrLiveLeaseTransferUncertain, err)
	}
	switch result {
	case 1:
		return true, nil
	case 2, 3:
		return false, service.ErrLiveLeaseSourceLost
	case 4:
		return false, service.ErrLiveConcurrencyFull
	default:
		return false, fmt.Errorf("%w: unexpected Live migration result %d", service.ErrLiveUnavailable, result)
	}
}

// ReleaseLiveLeaseAccount removes only this account's Live member so the joint
// Key/user members survive across SDP retry attempts.
func (c *concurrencyCache) ReleaseLiveLeaseAccount(ctx context.Context, accountID int64, leaseID string) error {
	if c == nil || c.rdb == nil || accountID <= 0 || leaseID == "" {
		return nil
	}
	return c.rdb.ZRem(ctx, liveAccountSlotKey(accountID), leaseID).Err()
}

// AbortLiveLeaseTransfer fences a handoff whose acknowledgement was lost and
// removes exactly this transfer's members. The fence deadline is independent
// of the Key wait deadline: it only has to outlive the in-flight transfer
// command, after which the deleted sources fail closed on any late replay.
func (c *concurrencyCache) AbortLiveLeaseTransfer(ctx context.Context, request service.LiveLeaseTransferRequest) error {
	if c == nil || c.rdb == nil || request.LeaseID == "" {
		return nil
	}
	return abortLiveLeaseTransferScript.Run(ctx, c.rdb, []string{
		accountSlotKey(request.AccountID),
		userSlotKey(request.UserID),
		apiKeySlotKey(request.APIKeyID),
		liveAccountSlotKey(request.AccountID),
		liveUserSlotKey(request.UserID),
		liveAPIKeySlotKey(request.APIKeyID),
		liveTransferFenceKey(request.APIKeyID),
	},
		request.AccountRequestID,
		request.UserRequestID,
		request.KeyRequestID,
		request.LeaseID,
		liveLeaseTTLSeconds,
	).Err()
}
