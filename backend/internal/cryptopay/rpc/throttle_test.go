package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEndpointThrottleSpacesCalls(t *testing.T) {
	throttle := NewEndpointThrottle(40 * time.Millisecond)
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 3; i++ {
		require.NoError(t, throttle.Wait(ctx))
	}
	// Three waits reserve three slots: the third starts at least 2 intervals in.
	require.GreaterOrEqual(t, time.Since(start), 80*time.Millisecond,
		"calls must not all start at once")
}

func TestEndpointThrottleDisabledWhenIntervalZero(t *testing.T) {
	throttle := NewEndpointThrottle(0)
	start := time.Now()
	for i := 0; i < 5; i++ {
		require.NoError(t, throttle.Wait(context.Background()))
	}
	require.Less(t, time.Since(start), 20*time.Millisecond,
		"zero interval must not pace anything")
}

func TestEndpointThrottleHonoursContextCancel(t *testing.T) {
	throttle := NewEndpointThrottle(500 * time.Millisecond)
	require.NoError(t, throttle.Wait(context.Background())) // reserve the next slot

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := throttle.Wait(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"a cancelled caller must not block on the throttle")
}

func TestThrottleForIsPerEndpoint(t *testing.T) {
	SetEndpointThrottleInterval(10 * time.Millisecond)
	t.Cleanup(func() { SetEndpointThrottleInterval(0) })

	a := ThrottleFor("https://a.example/rpc")
	b := ThrottleFor("https://a.example/rpc/")
	c := ThrottleFor("https://other.example/rpc")
	require.NotNil(t, a)
	require.Same(t, a, b, "trailing slash must not create a second throttle")
	require.NotSame(t, a, c, "different providers must not share pacing")
}

func TestSetEndpointThrottleIntervalZeroDisables(t *testing.T) {
	SetEndpointThrottleInterval(10 * time.Millisecond)
	require.NotNil(t, ThrottleFor("https://a.example/rpc"))
	SetEndpointThrottleInterval(0)
	require.Nil(t, ThrottleFor("https://a.example/rpc"),
		"disabling must take effect for later calls")
}
