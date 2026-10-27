package requestcapture

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureSlotCannotBeOpenedByConcurrentRollingInstance(t *testing.T) {
	dir, store := t.TempDir(), newMemoryStore()
	config := Config{Enabled: true, QuotaMiB: 8, RetentionDays: 1}
	first, err := New(store, dir, config)
	require.NoError(t, err)
	t.Cleanup(first.Close)
	identity := first.InstanceID()
	task, err := first.Create(context.Background(), CreateTask{TargetType: "user", TargetID: 7, DurationMinutes: 1}, "fixture")
	require.NoError(t, err)
	duplicate, err := New(store, dir, config)
	require.Error(t, err)
	require.Nil(t, duplicate)
	active, err := first.Task(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, "running", active.Status, "duplicate initialization must not interrupt the live owner's tasks")
	first.Close()
	reopened, err := New(store, dir, config)
	require.NoError(t, err)
	defer reopened.Close()
	require.Equal(t, identity, reopened.InstanceID(), "a stable slot must preserve its historical identity")
	prior, err := reopened.Task(context.Background(), task.ID)
	require.NoError(t, err)
	require.NotEqual(t, "running", prior.Status)
}
