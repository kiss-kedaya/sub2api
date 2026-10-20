package service

import "testing"

func TestBackgroundJobsDisabled(t *testing.T) {
	t.Setenv(disableBackgroundJobsEnv, "")
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", want: false},
		{name: "empty", value: "", want: false},
		{name: "false", value: "false", want: false},
		{name: "zero", value: "0", want: false},
		{name: "one", value: "1", want: true},
		{name: "true", value: "true", want: true},
		{name: "yes", value: "yes", want: true},
		{name: "on with whitespace", value: " ON ", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(disableBackgroundJobsEnv, tt.value)
			if got := BackgroundJobsDisabled(); got != tt.want {
				t.Fatalf("BackgroundJobsDisabled() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestStartBackgroundJob(t *testing.T) {
	t.Run("disabled leaves job stopped", func(t *testing.T) {
		t.Setenv(disableBackgroundJobsEnv, "true")
		started := false
		startBackgroundJob(func() { started = true })
		if started {
			t.Fatal("job started while background jobs were disabled")
		}
	})

	t.Run("default preserves startup", func(t *testing.T) {
		t.Setenv(disableBackgroundJobsEnv, "")
		started := false
		startBackgroundJob(func() { started = true })
		if !started {
			t.Fatal("job did not start with the default setting")
		}
	})

	t.Run("nil callback is safe", func(t *testing.T) {
		t.Setenv(disableBackgroundJobsEnv, "")
		startBackgroundJob(nil)
	})
}

func TestBalanceOutboxWorkerStartRespectsDisableBackgroundJobs(t *testing.T) {
	t.Setenv(disableBackgroundJobsEnv, "true")
	worker := &LiveBalanceAdjustmentOutboxWorker{}
	worker.Start()
	if worker.running.Load() {
		t.Fatal("balance outbox worker started in a test instance")
	}
}
