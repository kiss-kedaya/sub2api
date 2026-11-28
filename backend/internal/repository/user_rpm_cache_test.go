package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type evalKeyPrefixTestHook struct {
	prefix string
}

func (h evalKeyPrefixTestHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h evalKeyPrefixTestHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		name := strings.ToLower(cmd.Name())
		if (name == "eval" || name == "evalsha") && len(args) >= 4 {
			if numKeys, err := strconv.Atoi(fmt.Sprint(args[2])); err == nil && numKeys > 0 {
				for i := 0; i < numKeys && 3+i < len(args); i++ {
					if s, ok := args[3+i].(string); ok && !strings.HasPrefix(s, h.prefix) {
						args[3+i] = h.prefix + s
					}
				}
			}
		}
		return next(ctx, cmd)
	}
}

func (h evalKeyPrefixTestHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestUserRPMCache_SingleRTTIncrementGetAndRollover(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	hook := &redisCommandRecorderHook{}
	rdb.AddHook(hook)

	cache := NewUserRPMCache(rdb)
	ctx := context.Background()

	base := time.Unix(1750004400, 0).UTC() // exact minute boundary: 1750004400 / 60 = 29166740
	mr.SetTime(base)
	minute := base.Unix() / 60

	// 预热两个包级脚本（避免把首次 NOSCRIPT->EVAL 加载计入稳态 RTT）。
	_, err := cache.GetUserRPM(ctx, 9999)
	require.NoError(t, err)
	_, err = cache.IncrementUserRPM(ctx, 9999)
	require.NoError(t, err)

	// 未命中时 Get 返回 0 且严格 1 RTT，无独立 TIME 命令。
	hook.reset()
	got, err := cache.GetUserGroupRPM(ctx, 42, 7)
	require.NoError(t, err)
	require.Equal(t, 0, got)
	rtts, cmds, _ := hook.snapshot()
	require.Equal(t, 1, rtts, "GetUserGroupRPM must take 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)

	// IncrementUserGroupRPM 单 RTT 递增并设置 120s TTL，key 格式保持 rpm:ug:{uid}:{gid}:{minute}。
	hook.reset()
	c1, err := cache.IncrementUserGroupRPM(ctx, 42, 7)
	require.NoError(t, err)
	require.Equal(t, 1, c1)
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "IncrementUserGroupRPM must take 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)
	require.Equal(t, 0, hook.countCommand("time"))

	c2, err := cache.IncrementUserGroupRPM(ctx, 42, 7)
	require.NoError(t, err)
	require.Equal(t, 2, c2)

	ugKey := fmt.Sprintf("%s%d:%d:%d", userGroupRPMKeyPrefix, 42, 7, minute)
	rawVal, err := mr.Get(ugKey)
	require.NoError(t, err)
	require.Equal(t, "2", rawVal)
	require.Equal(t, userRPMKeyTTL, mr.TTL(ugKey))

	got, err = cache.GetUserGroupRPM(ctx, 42, 7)
	require.NoError(t, err)
	require.Equal(t, 2, got)

	// IncrementUserRPM / GetUserRPM 单 RTT 且 key 格式保持 rpm:u:{uid}:{minute}。
	hook.reset()
	u1, err := cache.IncrementUserRPM(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, 1, u1)
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "IncrementUserRPM must take 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)

	uKey := fmt.Sprintf("%s%d:%d", userRPMKeyPrefix, 42, minute)
	rawVal, err = mr.Get(uKey)
	require.NoError(t, err)
	require.Equal(t, "1", rawVal)
	require.Equal(t, userRPMKeyTTL, mr.TTL(uKey))

	got, err = cache.GetUserRPM(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, 1, got)

	// 跨分钟滚动：新分钟计数归零并独立累加。
	mr.SetTime(base.Add(time.Minute))
	got, err = cache.GetUserGroupRPM(ctx, 42, 7)
	require.NoError(t, err)
	require.Equal(t, 0, got)
	nextCount, err := cache.IncrementUserGroupRPM(ctx, 42, 7)
	require.NoError(t, err)
	require.Equal(t, 1, nextCount)
	nextKey := fmt.Sprintf("%s%d:%d:%d", userGroupRPMKeyPrefix, 42, 7, minute+1)
	rawVal, err = mr.Get(nextKey)
	require.NoError(t, err)
	require.Equal(t, "1", rawVal)
}

func TestUserRPMCache_PrefixHookCompatible(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	rdb.AddHook(evalKeyPrefixTestHook{prefix: "ns:"})

	cache := NewUserRPMCache(rdb)
	ctx := context.Background()
	base := time.Unix(1750004400, 0).UTC()
	mr.SetTime(base)

	count, err := cache.IncrementUserRPM(ctx, 88)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// KEYS[1] 经 prefixHook 加前缀后，Redis 实际落盘在 ns:rpm:u:88:{minute}。
	wantKey := fmt.Sprintf("ns:%s%d:%d", userRPMKeyPrefix, 88, base.Unix()/60)
	require.True(t, mr.Exists(wantKey), "prefixHook should transparently namespace KEYS[1]")

	got, err := cache.GetUserRPM(ctx, 88)
	require.NoError(t, err)
	require.Equal(t, 1, got)
}
