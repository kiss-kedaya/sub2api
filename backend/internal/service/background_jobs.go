package service

import "os"

const disableBackgroundJobsEnv = "SUB2API_DISABLE_BACKGROUND_JOBS"

// BackgroundJobsDisabled reports whether this process must leave proactive
// maintenance and shared work queues to the production workers. It does not
// change persisted settings or disable request handling, audit delivery, or
// cache-invalidation subscribers. Set the environment before starting a process;
// this switch does not stop jobs that are already running.
func BackgroundJobsDisabled() bool {
	return parseDebugEnvBool(os.Getenv(disableBackgroundJobsEnv))
}

// startBackgroundJob keeps providers fully wired without starting their jobs.
// Existing per-service switches still apply inside start when jobs are allowed.
func startBackgroundJob(start func()) {
	if !BackgroundJobsDisabled() && start != nil {
		start()
	}
}
