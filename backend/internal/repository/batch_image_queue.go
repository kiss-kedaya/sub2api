package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	defaultBatchImageReadyKey       = "batch_image:queue:ready"
	defaultBatchImageDelayedKey     = "batch_image:queue:delayed"
	defaultBatchImageActiveKey      = "batch_image:queue:active"
	defaultBatchImageInflightPrefix = "batch_image:queue:inflight:"
	defaultBatchImageLockPrefix     = "batch_image:queue:lock:"
	defaultBatchImageInflightTTL    = 7 * 24 * time.Hour
	defaultBatchImageJobLockTTL     = 5 * time.Minute
)

var batchImageMoveDueDelayedScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
end
return #jobs
`)

var batchImageRecoverStaleActiveScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
end
return #jobs
`)

var batchImageReleaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

type batchImageQueue struct {
	rdb            *redis.Client
	readyKey       string
	delayedKey     string
	activeKey      string
	inflightPrefix string
	lockPrefix     string
	inflightTTL    time.Duration
	lockTTL        time.Duration
}

func NewBatchImageQueue(rdb *redis.Client, cfg *config.Config) service.BatchImageQueue {
	return newBatchImageQueueWithOptions(rdb, batchImageQueueOptionsFromConfig(cfg))
}

type batchImageQueueOptions struct {
	ReadyKey       string
	DelayedKey     string
	ActiveKey      string
	InflightPrefix string
	LockPrefix     string
	InflightTTL    time.Duration
	LockTTL        time.Duration
}

func newBatchImageQueueWithOptions(rdb *redis.Client, opts batchImageQueueOptions) *batchImageQueue {
	opts = normalizeBatchImageQueueOptions(opts)
	return &batchImageQueue{
		rdb:            rdb,
		readyKey:       opts.ReadyKey,
		delayedKey:     opts.DelayedKey,
		activeKey:      opts.ActiveKey,
		inflightPrefix: opts.InflightPrefix,
		lockPrefix:     opts.LockPrefix,
		inflightTTL:    opts.InflightTTL,
		lockTTL:        opts.LockTTL,
	}
}

func batchImageQueueOptionsFromConfig(cfg *config.Config) batchImageQueueOptions {
	if cfg == nil {
		return batchImageQueueOptions{}
	}
	return batchImageQueueOptions{
		ReadyKey:       cfg.BatchImage.QueueReadyKey,
		DelayedKey:     cfg.BatchImage.QueueDelayedKey,
		ActiveKey:      cfg.BatchImage.QueueActiveKey,
		InflightPrefix: cfg.BatchImage.InflightKeyPrefix,
		LockPrefix:     cfg.BatchImage.LockKeyPrefix,
		InflightTTL:    time.Duration(cfg.BatchImage.InflightTTLSeconds) * time.Second,
		LockTTL:        time.Duration(cfg.BatchImage.JobLockTTLSeconds) * time.Second,
	}
}

func normalizeBatchImageQueueOptions(opts batchImageQueueOptions) batchImageQueueOptions {
	if opts.ReadyKey == "" {
		opts.ReadyKey = defaultBatchImageReadyKey
	}
	if opts.DelayedKey == "" {
		opts.DelayedKey = defaultBatchImageDelayedKey
	}
	if opts.ActiveKey == "" {
		opts.ActiveKey = defaultBatchImageActiveKey
	}
	if opts.InflightPrefix == "" {
		opts.InflightPrefix = defaultBatchImageInflightPrefix
	}
	if opts.LockPrefix == "" {
		opts.LockPrefix = defaultBatchImageLockPrefix
	}
	if opts.InflightTTL <= 0 {
		opts.InflightTTL = defaultBatchImageInflightTTL
	}
	if opts.LockTTL <= 0 {
		opts.LockTTL = defaultBatchImageJobLockTTL
	}
	return opts
}

func (q *batchImageQueue) Enqueue(ctx context.Context, batchID string) error {
	if !service.IsValidBatchImageID(batchID) {
		return service.ErrInvalidBatchImageQueuePayload
	}

	ok, err := q.rdb.SetNX(ctx, q.inflightKey(batchID), batchID, q.inflightTTL).Result()
	if err != nil {
		return err
	}
	if !ok {
		return service.ErrBatchImageAlreadyQueued
	}
	if err := q.rdb.LPush(ctx, q.readyKey, batchID).Err(); err != nil {
		_ = q.rdb.Del(ctx, q.inflightKey(batchID)).Err()
		return err
	}
	return nil
}

func (q *batchImageQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (service.ReservedBatchImageJob, error) {
	result, err := q.rdb.BRPop(ctx, blockTimeout, q.readyKey).Result()
	if errors.Is(err, redis.Nil) {
		return service.ReservedBatchImageJob{}, service.ErrBatchImageQueueEmpty
	}
	if err != nil {
		return service.ReservedBatchImageJob{}, err
	}
	if len(result) != 2 || !service.IsValidBatchImageID(result[1]) {
		return service.ReservedBatchImageJob{}, service.ErrInvalidBatchImageQueuePayload
	}

	batchID := result[1]
	if err := q.rdb.ZAdd(ctx, q.activeKey, redis.Z{
		Score:  float64(time.Now().UnixMilli()),
		Member: batchID,
	}).Err(); err != nil {
		return service.ReservedBatchImageJob{}, err
	}
	return service.ReservedBatchImageJob{BatchID: batchID}, nil
}

func (q *batchImageQueue) RequeueAfter(ctx context.Context, batchID string, delay time.Duration) error {
	if !service.IsValidBatchImageID(batchID) {
		return service.ErrInvalidBatchImageQueuePayload
	}
	pipe := q.rdb.TxPipeline()
	pipe.ZRem(ctx, q.activeKey, batchID)
	pipe.ZRem(ctx, q.delayedKey, batchID)
	if delay <= 0 {
		pipe.LPush(ctx, q.readyKey, batchID)
	} else {
		pipe.ZAdd(ctx, q.delayedKey, redis.Z{
			Score:  float64(time.Now().Add(delay).UnixMilli()),
			Member: batchID,
		})
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (q *batchImageQueue) Ack(ctx context.Context, batchID string) error {
	if !service.IsValidBatchImageID(batchID) {
		return service.ErrInvalidBatchImageQueuePayload
	}
	pipe := q.rdb.TxPipeline()
	pipe.ZRem(ctx, q.activeKey, batchID)
	pipe.ZRem(ctx, q.delayedKey, batchID)
	pipe.Del(ctx, q.inflightKey(batchID))
	_, err := pipe.Exec(ctx)
	return err
}

func (q *batchImageQueue) Heartbeat(ctx context.Context, batchID string) error {
	if !service.IsValidBatchImageID(batchID) {
		return service.ErrInvalidBatchImageQueuePayload
	}
	return q.rdb.ZAdd(ctx, q.activeKey, redis.Z{
		Score:  float64(time.Now().UnixMilli()),
		Member: batchID,
	}).Err()
}

func (q *batchImageQueue) MoveDueDelayedToReady(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	return batchImageMoveDueDelayedScript.Run(ctx, q.rdb, []string{q.delayedKey, q.readyKey}, time.Now().UnixMilli(), limit).Int()
}

func (q *batchImageQueue) RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	if staleAfter <= 0 {
		return 0, service.ErrInvalidBatchImageQueuePayload
	}
	if limit <= 0 {
		limit = 100
	}
	cutoff := time.Now().Add(-staleAfter).UnixMilli()
	return batchImageRecoverStaleActiveScript.Run(ctx, q.rdb, []string{q.activeKey, q.readyKey}, cutoff, limit).Int()
}

func (q *batchImageQueue) TryAcquireJobLock(ctx context.Context, batchID string, ttl time.Duration) (service.BatchImageJobLock, bool, error) {
	if !service.IsValidBatchImageID(batchID) {
		return nil, false, service.ErrInvalidBatchImageQueuePayload
	}
	if ttl <= 0 {
		ttl = q.lockTTL
	}
	token, err := newBatchImageLockToken()
	if err != nil {
		return nil, false, err
	}
	key := q.lockKey(batchID)
	ok, err := q.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return &batchImageRedisJobLock{rdb: q.rdb, key: key, token: token}, true, nil
}

func (q *batchImageQueue) inflightKey(batchID string) string {
	return q.inflightPrefix + batchID
}

func (q *batchImageQueue) lockKey(batchID string) string {
	return q.lockPrefix + batchID
}

type batchImageRedisJobLock struct {
	rdb   *redis.Client
	key   string
	token string
}

func (l *batchImageRedisJobLock) Release(ctx context.Context) error {
	if l == nil || l.rdb == nil || l.key == "" || l.token == "" {
		return nil
	}
	return batchImageReleaseLockScript.Run(ctx, l.rdb, []string{l.key}, l.token).Err()
}

func newBatchImageLockToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

var _ service.BatchImageQueue = (*batchImageQueue)(nil)
