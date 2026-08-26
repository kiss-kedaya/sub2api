package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

const (
	liveBalanceWalletKeyPrefix = "billing:live_wallet:"
	liveBalanceMoneyScale      = int32(8)

	// Active attempts must not expire: losing hold metadata before PostgreSQL
	// recovery runs can make a committed charge impossible to reconcile. Only
	// terminal markers expire after finalize/refund.
	liveBalanceTerminalAttemptTTL = 15 * time.Minute
	liveBalanceAdjustmentEventTTL = 30 * 24 * time.Hour

	// Redis Lua represents numbers as IEEE-754 doubles. Keeping integer money
	// values in this range makes comparisons exact; mutations use INCRBY.
	maxExactLuaInteger int64 = 1<<53 - 1
)

var (
	errInvalidLiveBalanceUserID    = errors.New("live balance user id must be positive")
	errInvalidLiveBalanceAttemptID = errors.New("live balance attempt id must not be empty")
	errInvalidLiveBalanceMoney     = errors.New("live balance money value is invalid or exceeds the exact range")
)

var _ service.BillingCache = (*billingCache)(nil)

func liveBalanceWalletKey(userID int64) string {
	return fmt.Sprintf("%s{%d}", liveBalanceWalletKeyPrefix, userID)
}

func liveBalanceWatermarkKey(userID int64) string {
	return fmt.Sprintf("%s{%d}:watermark", liveBalanceWalletKeyPrefix, userID)
}

func liveBalanceAttemptKey(userID int64, attemptID string) string {
	digest := sha256.Sum256([]byte(attemptID))
	return fmt.Sprintf("%s{%d}:attempt:%s", liveBalanceWalletKeyPrefix, userID, hex.EncodeToString(digest[:]))
}

func liveBalanceAdjustmentKey(userID int64, eventID string) string {
	digest := sha256.Sum256([]byte(eventID))
	return fmt.Sprintf("%s{%d}:adjust:%s", liveBalanceWalletKeyPrefix, userID, hex.EncodeToString(digest[:]))
}

var authorizeLiveBalanceScript = redis.NewScript(`
local wallet = redis.call('GET', KEYS[1])
local watermark = redis.call('GET', KEYS[3])
local state = redis.call('HGET', KEYS[2], 'state')
if state ~= false then
  local initial = redis.call('HGET', KEYS[2], 'initial') or '0'
  local reserved = redis.call('HGET', KEYS[2], 'reserved') or '0'
  local actual = redis.call('HGET', KEYS[2], 'actual') or '0'
  if wallet == false then
    return {3, state, '0', reserved, actual}
  end
  if watermark == false then
    return {3, state, wallet, reserved, actual}
  end
  redis.call('PERSIST', KEYS[1])
  redis.call('PERSIST', KEYS[3])
  if tonumber(state) == 1 then
    redis.call('PERSIST', KEYS[2])
  end
  if initial ~= ARGV[2] then
    return {3, state, wallet, reserved, actual}
  end
  return {2, state, wallet, reserved, actual}
end

if wallet == false then
	if ARGV[4] ~= '1' then
		return {4, 0, '0', '0', '0'}
	end
  redis.call('SET', KEYS[1], ARGV[1])
  redis.call('SET', KEYS[3], ARGV[3])
  wallet = ARGV[1]
else
  if watermark == false then
    return {3, 0, wallet, '0', '0'}
  end
  redis.call('PERSIST', KEYS[1])
  redis.call('PERSIST', KEYS[3])
end

if tonumber(wallet) < tonumber(ARGV[2]) then
  return {0, 0, wallet, '0', '0'}
end

redis.call('INCRBY', KEYS[1], -tonumber(ARGV[2]))
wallet = redis.call('GET', KEYS[1])
redis.call('HSET', KEYS[2],
  'state', '1',
  'initial', ARGV[2],
  'reserved', ARGV[2],
  'actual', '0')
redis.call('PERSIST', KEYS[2])
return {1, 1, tostring(wallet), ARGV[2], '0'}
`)

var topUpLiveBalanceScript = redis.NewScript(`
local wallet = redis.call('GET', KEYS[1])
local state = redis.call('HGET', KEYS[2], 'state')
if state == false then
  return {4, 0, wallet or '0', '0', '0'}
end

local reserved = redis.call('HGET', KEYS[2], 'reserved') or '0'
local actual = redis.call('HGET', KEYS[2], 'actual') or '0'
if wallet == false then
  return {3, state, '0', reserved, actual}
end
redis.call('PERSIST', KEYS[1])
if tonumber(state) ~= 1 then
  return {3, state, wallet, reserved, actual}
end
redis.call('PERSIST', KEYS[2])
if tonumber(ARGV[1]) < tonumber(reserved) then
  return {3, state, wallet, reserved, actual}
end
if ARGV[1] == reserved then
  return {2, state, wallet, reserved, actual}
end

local additional = tonumber(ARGV[1]) - tonumber(reserved)
if tonumber(wallet) < additional then
  return {0, state, wallet, reserved, actual}
end

redis.call('INCRBY', KEYS[1], -additional)
wallet = redis.call('GET', KEYS[1])
redis.call('HSET', KEYS[2], 'reserved', ARGV[1])
return {1, state, tostring(wallet), ARGV[1], actual}
`)

var finalizeLiveBalanceScript = redis.NewScript(`
local wallet = redis.call('GET', KEYS[1])
local state = redis.call('HGET', KEYS[2], 'state')
if state == false then
  return {4, 0, wallet or '0', '0', '0'}
end

local reserved = redis.call('HGET', KEYS[2], 'reserved') or '0'
local actual = redis.call('HGET', KEYS[2], 'actual') or '0'
if wallet == false then
  return {3, state, '0', reserved, actual}
end
redis.call('PERSIST', KEYS[1])
if tonumber(state) == 2 then
  if actual == ARGV[1] then
    return {2, state, wallet, reserved, actual}
  end
  return {3, state, wallet, reserved, actual}
end
if tonumber(state) ~= 1 then
  return {3, state, wallet, reserved, actual}
end

local adjustment = tonumber(reserved) - tonumber(ARGV[1])
if adjustment ~= 0 then
  redis.call('INCRBY', KEYS[1], adjustment)
  wallet = redis.call('GET', KEYS[1])
end
redis.call('HSET', KEYS[2], 'state', '2', 'actual', ARGV[1])
redis.call('EXPIRE', KEYS[2], ARGV[2])
return {1, 2, tostring(wallet), reserved, ARGV[1]}
`)

var refundLiveBalanceScript = redis.NewScript(`
local wallet = redis.call('GET', KEYS[1])
local state = redis.call('HGET', KEYS[2], 'state')
if state == false then
  return {4, 0, wallet or '0', '0', '0'}
end

local reserved = redis.call('HGET', KEYS[2], 'reserved') or '0'
local actual = redis.call('HGET', KEYS[2], 'actual') or '0'
if wallet == false then
  return {3, state, '0', reserved, actual}
end
redis.call('PERSIST', KEYS[1])
if tonumber(state) == 3 then
  return {2, state, wallet, reserved, actual}
end
if tonumber(state) ~= 1 then
  return {3, state, wallet, reserved, actual}
end

if tonumber(reserved) ~= 0 then
  redis.call('INCRBY', KEYS[1], tonumber(reserved))
  wallet = redis.call('GET', KEYS[1])
end
redis.call('HSET', KEYS[2], 'state', '3', 'actual', '0')
redis.call('EXPIRE', KEYS[2], ARGV[1])
return {1, 3, tostring(wallet), reserved, '0'}
`)

// External balance mutations are deliberately relative. PostgreSQL can lag
// behind the Redis wallet while usage settlements are being aggregated, so an
// absolute cache fill here could recreate already-spent balance. A missing
// wallet is recorded as a terminal no-op: its first authorization will load the
// already-committed database balance instead.
var adjustLiveBalanceScript = redis.NewScript(`
local wallet = redis.call('GET', KEYS[1])
local recorded_delta = redis.call('HGET', KEYS[2], 'delta')
if recorded_delta ~= false then
  if recorded_delta ~= ARGV[1] then
    return {3, 0, wallet or '0', '0', recorded_delta}
  end
  return {2, 0, wallet or '0', '0', recorded_delta}
end

if wallet == false then
  redis.call('HSET', KEYS[2], 'delta', ARGV[1], 'applied', '0')
  redis.call('EXPIRE', KEYS[2], ARGV[2])
  return {4, 0, '0', '0', ARGV[1]}
end

redis.call('PERSIST', KEYS[1])
redis.call('INCRBY', KEYS[1], tonumber(ARGV[1]))
wallet = redis.call('GET', KEYS[1])
redis.call('HSET', KEYS[2], 'delta', ARGV[1], 'applied', '1')
redis.call('EXPIRE', KEYS[2], ARGV[2])
return {1, 0, tostring(wallet), '0', ARGV[1]}
`)

// Watermarked adjustments are the durable outbox path. A wallet initialized
// from PostgreSQL records the latest external event included in that snapshot.
// Events at or below that watermark are acknowledged without another delta;
// newer events must name the wallet's exact predecessor.
var adjustLiveBalanceAtWatermarkScript = redis.NewScript(`
local wallet = redis.call('GET', KEYS[1])
local recorded_delta = redis.call('HGET', KEYS[3], 'delta')
local recorded_event = redis.call('HGET', KEYS[3], 'event')
local recorded_predecessor = redis.call('HGET', KEYS[3], 'predecessor')
if recorded_delta ~= false then
  if recorded_delta ~= ARGV[1]
      or recorded_event ~= ARGV[3]
      or recorded_predecessor ~= ARGV[4] then
    return {3, 0, wallet or '0', '0', recorded_delta}
  end
  return {2, 0, wallet or '0', '0', recorded_delta}
end

if wallet == false then
  return {3, 0, '0', '0', ARGV[1]}
end

local watermark = redis.call('GET', KEYS[2])
if watermark == false then
  return {3, 0, wallet, '0', ARGV[1]}
end
redis.call('PERSIST', KEYS[1])
redis.call('PERSIST', KEYS[2])

if tonumber(ARGV[3]) <= tonumber(watermark) then
  redis.call('HSET', KEYS[3],
    'delta', ARGV[1],
    'event', ARGV[3],
    'predecessor', ARGV[4],
    'applied', '0')
  redis.call('EXPIRE', KEYS[3], ARGV[2])
  return {2, 0, wallet, '0', ARGV[1]}
end

if watermark ~= ARGV[4] then
  return {3, 0, wallet, '0', ARGV[1]}
end

redis.call('INCRBY', KEYS[1], tonumber(ARGV[1]))
wallet = redis.call('GET', KEYS[1])
redis.call('SET', KEYS[2], ARGV[3])
redis.call('PERSIST', KEYS[2])
redis.call('HSET', KEYS[3],
  'delta', ARGV[1],
  'event', ARGV[3],
  'predecessor', ARGV[4],
  'applied', '1')
redis.call('EXPIRE', KEYS[3], ARGV[2])
return {1, 0, tostring(wallet), '0', ARGV[1]}
`)

func (c *billingCache) GetLiveBalance(ctx context.Context, userID int64) (float64, bool, error) {
	if userID <= 0 {
		return 0, false, errInvalidLiveBalanceUserID
	}
	value, err := c.rdb.Get(ctx, liveBalanceWalletKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get live balance: %w", err)
	}
	units, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse live balance: %w", err)
	}
	return liveBalanceUnitsToFloat(units), true, nil
}

// AuthorizeLiveBalance initializes a user's persistent wallet once and places
// an idempotent hold. A later DB fallback can never overwrite that live value.
func (c *billingCache) AuthorizeLiveBalance(ctx context.Context, userID int64, attemptID string, fallbackBalance, holdAmount float64) (service.LiveBalanceResult, error) {
	return c.AuthorizeLiveBalanceAtWatermark(ctx, userID, attemptID, fallbackBalance, 0, holdAmount)
}

func (c *billingCache) AuthorizeLiveBalanceAtWatermark(
	ctx context.Context,
	userID int64,
	attemptID string,
	fallbackBalance float64,
	fallbackWatermark int64,
	holdAmount float64,
) (service.LiveBalanceResult, error) {
	return c.AuthorizeLiveBalanceAtWatermarkIfSafe(
		ctx, userID, attemptID, fallbackBalance, fallbackWatermark, holdAmount, true,
	)
}

// AuthorizeExistingLiveBalance is the normal request fast path. It performs
// one atomic Redis operation and never initializes a missing wallet from a
// placeholder value, so PostgreSQL is consulted only for a cold wallet.
func (c *billingCache) AuthorizeExistingLiveBalance(
	ctx context.Context,
	userID int64,
	attemptID string,
	holdAmount float64,
) (service.LiveBalanceResult, error) {
	return c.AuthorizeLiveBalanceAtWatermarkIfSafe(
		ctx, userID, attemptID, 0, 0, holdAmount, false,
	)
}

func (c *billingCache) AuthorizeLiveBalanceAtWatermarkIfSafe(
	ctx context.Context,
	userID int64,
	attemptID string,
	fallbackBalance float64,
	fallbackWatermark int64,
	holdAmount float64,
	allowInitialize bool,
) (service.LiveBalanceResult, error) {
	if err := validateLiveBalanceIdentity(userID, attemptID); err != nil {
		return service.LiveBalanceResult{}, err
	}
	if fallbackWatermark < 0 || fallbackWatermark > maxExactLuaInteger {
		return service.LiveBalanceResult{}, errors.New("live balance initialization watermark is invalid")
	}
	fallbackUnits, err := liveBalanceMoneyToUnits(fallbackBalance, false)
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("fallback balance: %w", err)
	}
	holdUnits, err := liveBalanceMoneyToUnits(holdAmount, true)
	if err != nil || holdAmount < 0 {
		return service.LiveBalanceResult{}, fmt.Errorf("hold amount: %w", errInvalidLiveBalanceMoney)
	}

	raw, err := authorizeLiveBalanceScript.Run(ctx, c.rdb, []string{
		liveBalanceWalletKey(userID),
		liveBalanceAttemptKey(userID, attemptID),
		liveBalanceWatermarkKey(userID),
	}, fallbackUnits, holdUnits, fallbackWatermark, boolToRedisFlag(allowInitialize)).Result()
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("initialize live balance: %w", err)
	}
	return parseLiveBalanceResult(raw)
}

func boolToRedisFlag(value bool) int {
	if value {
		return 1
	}
	return 0
}

// TopUpLiveBalance raises the cumulative hold to targetHoldAmount. Passing an
// already-applied target is an idempotent no-op rather than a second debit.
func (c *billingCache) TopUpLiveBalance(ctx context.Context, userID int64, attemptID string, targetHoldAmount float64) (service.LiveBalanceResult, error) {
	if err := validateLiveBalanceIdentity(userID, attemptID); err != nil {
		return service.LiveBalanceResult{}, err
	}
	targetUnits, err := liveBalanceMoneyToUnits(targetHoldAmount, true)
	if err != nil || targetHoldAmount < 0 {
		return service.LiveBalanceResult{}, fmt.Errorf("target hold amount: %w", errInvalidLiveBalanceMoney)
	}

	return c.runLiveBalanceScript(ctx, topUpLiveBalanceScript,
		userID, attemptID, targetUnits)
}

// FinalizeLiveBalance replaces the accumulated hold with the actual charge.
// If actual exceeds the hold, the bounded difference is still debited so a
// completed request cannot escape billing; the result may therefore be negative.
func (c *billingCache) FinalizeLiveBalance(ctx context.Context, userID int64, attemptID string, actualAmount float64) (service.LiveBalanceResult, error) {
	if err := validateLiveBalanceIdentity(userID, attemptID); err != nil {
		return service.LiveBalanceResult{}, err
	}
	actualUnits, err := liveBalanceMoneyToUnits(actualAmount, false)
	if err != nil || actualAmount < 0 {
		return service.LiveBalanceResult{}, fmt.Errorf("actual amount: %w", errInvalidLiveBalanceMoney)
	}

	return c.runLiveBalanceScript(ctx, finalizeLiveBalanceScript,
		userID, attemptID, actualUnits, int(liveBalanceTerminalAttemptTTL.Seconds()))
}

// RefundLiveBalance releases the complete hold for a failed request.
func (c *billingCache) RefundLiveBalance(ctx context.Context, userID int64, attemptID string) (service.LiveBalanceResult, error) {
	if err := validateLiveBalanceIdentity(userID, attemptID); err != nil {
		return service.LiveBalanceResult{}, err
	}
	return c.runLiveBalanceScript(ctx, refundLiveBalanceScript,
		userID, attemptID, int(liveBalanceTerminalAttemptTTL.Seconds()))
}

// AdjustLiveBalance applies a committed, non-usage balance delta once. It must
// only be called after the corresponding PostgreSQL transaction commits.
func (c *billingCache) AdjustLiveBalance(ctx context.Context, userID int64, eventID string, delta float64) (service.LiveBalanceResult, error) {
	if err := validateLiveBalanceIdentity(userID, eventID); err != nil {
		return service.LiveBalanceResult{}, err
	}
	deltaUnits, err := liveBalanceMoneyToUnits(delta, false)
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("balance adjustment: %w", err)
	}

	raw, err := adjustLiveBalanceScript.Run(ctx, c.rdb, []string{
		liveBalanceWalletKey(userID),
		liveBalanceAdjustmentKey(userID, eventID),
	}, deltaUnits, int(liveBalanceAdjustmentEventTTL.Seconds())).Result()
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("adjust live balance: %w", err)
	}
	return parseLiveBalanceResult(raw)
}

func (c *billingCache) AdjustLiveBalanceAtWatermark(
	ctx context.Context,
	userID int64,
	eventID string,
	eventWatermark int64,
	predecessorWatermark int64,
	deltaUnits int64,
) (service.LiveBalanceResult, error) {
	if err := validateLiveBalanceIdentity(userID, eventID); err != nil {
		return service.LiveBalanceResult{}, err
	}
	if eventWatermark <= 0 || predecessorWatermark < 0 ||
		eventWatermark <= predecessorWatermark ||
		eventWatermark > maxExactLuaInteger || predecessorWatermark > maxExactLuaInteger {
		return service.LiveBalanceResult{}, errors.New("live balance outbox watermark is invalid")
	}
	if deltaUnits == 0 || deltaUnits > maxExactLuaInteger || deltaUnits < -maxExactLuaInteger {
		return service.LiveBalanceResult{}, fmt.Errorf("balance adjustment: %w", errInvalidLiveBalanceMoney)
	}

	raw, err := adjustLiveBalanceAtWatermarkScript.Run(ctx, c.rdb, []string{
		liveBalanceWalletKey(userID),
		liveBalanceWatermarkKey(userID),
		liveBalanceAdjustmentKey(userID, eventID),
	},
		deltaUnits,
		int(liveBalanceAdjustmentEventTTL.Seconds()),
		eventWatermark,
		predecessorWatermark,
	).Result()
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("adjust live balance at watermark: %w", err)
	}
	return parseLiveBalanceResult(raw)
}

func (c *billingCache) runLiveBalanceScript(ctx context.Context, script *redis.Script, userID int64, attemptID string, args ...any) (service.LiveBalanceResult, error) {
	raw, err := script.Run(ctx, c.rdb, []string{
		liveBalanceWalletKey(userID),
		liveBalanceAttemptKey(userID, attemptID),
	}, args...).Result()
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("update live balance: %w", err)
	}
	return parseLiveBalanceResult(raw)
}

func parseLiveBalanceResult(raw any) (service.LiveBalanceResult, error) {
	values, ok := raw.([]any)
	if !ok || len(values) != 5 {
		return service.LiveBalanceResult{}, fmt.Errorf("invalid live balance script result: %T", raw)
	}
	outcome, err := liveBalanceReplyInt(values[0])
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("parse live balance outcome: %w", err)
	}
	state, err := liveBalanceReplyInt(values[1])
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("parse live balance state: %w", err)
	}
	balance, err := liveBalanceReplyInt(values[2])
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("parse live balance available amount: %w", err)
	}
	reserved, err := liveBalanceReplyInt(values[3])
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("parse live balance reserved amount: %w", err)
	}
	actual, err := liveBalanceReplyInt(values[4])
	if err != nil {
		return service.LiveBalanceResult{}, fmt.Errorf("parse live balance actual amount: %w", err)
	}
	if outcome < int64(service.LiveBalanceOutcomeInsufficient) || outcome > int64(service.LiveBalanceOutcomeNotFound) ||
		state < int64(service.LiveBalanceAttemptNone) || state > int64(service.LiveBalanceAttemptRefunded) {
		return service.LiveBalanceResult{}, fmt.Errorf("invalid live balance result codes: outcome=%d state=%d", outcome, state)
	}
	return service.LiveBalanceResult{
		Outcome:          service.LiveBalanceOutcome(outcome),
		State:            service.LiveBalanceAttemptState(state),
		AvailableBalance: liveBalanceUnitsToFloat(balance),
		ReservedAmount:   liveBalanceUnitsToFloat(reserved),
		ActualAmount:     liveBalanceUnitsToFloat(actual),
	}, nil
}

func liveBalanceReplyInt(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	case []byte:
		return strconv.ParseInt(string(v), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis reply type %T", value)
	}
}

func validateLiveBalanceIdentity(userID int64, attemptID string) error {
	if userID <= 0 {
		return errInvalidLiveBalanceUserID
	}
	if attemptID == "" {
		return errInvalidLiveBalanceAttemptID
	}
	return nil
}

func liveBalanceMoneyToUnits(value float64, roundUp bool) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errInvalidLiveBalanceMoney
	}
	money := decimal.NewFromFloat(value).Shift(liveBalanceMoneyScale)
	if roundUp {
		money = money.RoundCeil(0)
	} else {
		money = money.Round(0)
	}
	limit := decimal.NewFromInt(maxExactLuaInteger)
	if money.GreaterThan(limit) || money.LessThan(limit.Neg()) {
		return 0, errInvalidLiveBalanceMoney
	}
	return money.IntPart(), nil
}

func liveBalanceUnitsToFloat(units int64) float64 {
	return float64(units) / math.Pow10(int(liveBalanceMoneyScale))
}
