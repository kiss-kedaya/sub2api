package cryptopay

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The config save is already stored when ReloadWithin runs; what these tests pin
// down is that applying it never hangs the request behind a sweep (which owns
// sweepMu for up to 30 minutes), never gets lost, and never piles up.

// newAsyncReloadService builds a service whose reloads are observable: every
// reload reads the stored config exactly once (repo.reads) and publishes it
// (Config()). The default config keeps the channel disabled, so a reload is just
// that read plus the publish.
func newAsyncReloadService(t *testing.T) (*Service, *reloadConfigRepository, CryptoConfig) {
	t.Helper()
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	repo := &reloadConfigRepository{}
	cfg := DefaultCryptoConfig()
	repo.set(t, cfg)
	s := &Service{settings: service.NewSettingService(repo, nil), wallet: wallet.New(nil)}
	t.Cleanup(func() { requireReloadWorkerStopped(t, s) }) // runs after Close
	t.Cleanup(s.Close)
	return s, repo, cfg
}

// requireReloadWorkerStopped fails the test when a reload worker is still alive
// at the end of it. A worker that outlives its test would read package-level
// knobs (reloadApplyTimeout) that a later test rewrites.
func requireReloadWorkerStopped(t *testing.T, s *Service) {
	t.Helper()
	require.Eventually(t, func() bool { return !reloadWorkerRunning(s) }, 5*time.Second, 5*time.Millisecond,
		"a reload worker outlived its test")
}

// holdSweepLock makes s look like it is in the middle of a manual sweep. The
// returned func ends the "sweep"; it is idempotent and also runs on cleanup, so a
// failing test never leaves a goroutine parked on the lock.
func holdSweepLock(t *testing.T, s *Service) (endSweep func()) {
	t.Helper()
	s.sweepMu.Lock()
	var once sync.Once
	endSweep = func() { once.Do(s.sweepMu.Unlock) }
	t.Cleanup(endSweep)
	return endSweep
}

// reloadWithinGuarded calls ReloadWithin but fails the test, instead of hanging
// it, when the call does not come back well after its own bound.
func reloadWithinGuarded(t *testing.T, ctx context.Context, s *Service, wait time.Duration) bool {
	t.Helper()
	got := make(chan bool, 1)
	go func() { got <- s.ReloadWithin(ctx, wait) }()
	select {
	case applied := <-got:
		return applied
	case <-time.After(wait + 3*time.Second):
		t.Fatal("ReloadWithin did not come back within its bound: the caller is hanging behind the sweep")
		return false
	}
}

// answersWithin runs call and fails the test, instead of hanging it, when it does
// not answer within limit.
func answersWithin(t *testing.T, limit time.Duration, call func() error) error {
	t.Helper()
	got := make(chan error, 1)
	go func() { got <- call() }()
	select {
	case err := <-got:
		return err
	case <-time.After(limit):
		t.Fatal("the call is hanging behind the sweep instead of answering")
		return nil
	}
}

func saveScanRange(t *testing.T, repo *reloadConfigRepository, cfg CryptoConfig, blocks int) {
	t.Helper()
	cfg.ScanRange[ChainBSC] = blocks
	repo.set(t, cfg)
}

func liveScanRange(s *Service) int { return s.Config().ScanRange[ChainBSC] }

// requireLiveScanRange checks the live config but fails the test, instead of
// hanging it, when reading it is stuck behind the state lock.
func requireLiveScanRange(t *testing.T, s *Service, want int, msg string) {
	t.Helper()
	got := make(chan int, 1)
	go func() { got <- liveScanRange(s) }()
	select {
	case live := <-got:
		require.Equal(t, want, live, msg)
	case <-time.After(2 * time.Second):
		t.Fatal("reading the live config is stuck: something holds the state lock")
	}
}

func reloadQueued(s *Service) bool {
	s.reloadQ.mu.Lock()
	defer s.reloadQ.mu.Unlock()
	return s.reloadQ.pending != nil
}

func reloadWorkerRunning(s *Service) bool {
	s.reloadQ.mu.Lock()
	defer s.reloadQ.mu.Unlock()
	return s.reloadQ.running
}

func requireSweepLockFree(t *testing.T, s *Service) {
	t.Helper()
	require.Eventually(t, func() bool {
		if !s.sweepMu.TryLock() {
			return false
		}
		s.sweepMu.Unlock()
		return true
	}, 2*time.Second, time.Millisecond, "the sweep lock was left held")
}

func TestReloadWithinAppliesBeforeReturningWhenNoRunHoldsTheLock(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	saveScanRange(t, repo, cfg, 100)

	require.True(t, reloadWithinGuarded(t, t.Context(), s, 5*time.Second))
	requireLiveScanRange(t, s, 100, "the saved config must be live when the call returns")
	require.EqualValues(t, 1, repo.reads.Load())
	requireSweepLockFree(t, s)
}

func TestReloadWithinDoesNotWaitForARunningSweep(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	endSweep := holdSweepLock(t, s) // a manual sweep owns the service
	saveScanRange(t, repo, cfg, 100)

	start := time.Now()
	require.False(t, reloadWithinGuarded(t, t.Context(), s, 150*time.Millisecond), "must report \"not applied yet\" instead of hanging")
	require.Less(t, time.Since(start), 2*time.Second)
	require.EqualValues(t, 0, repo.reads.Load(), "nothing may be applied while the sweep owns the lock")
	requireLiveScanRange(t, s, 0, "the running sweep must keep the old config live")

	endSweep() // the sweep ends
	require.Eventually(t, func() bool { return liveScanRange(s) == 100 }, 5*time.Second, 10*time.Millisecond,
		"the queued apply must finish on its own once the lock is free")
	require.EqualValues(t, 1, repo.reads.Load())
	requireSweepLockFree(t, s)
}

func TestReloadWithinQueuedSavesShareOneReload(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	endSweep := holdSweepLock(t, s)
	for i := 1; i <= 5; i++ { // an admin hammering Save during a long sweep
		saveScanRange(t, repo, cfg, 100*i)
		require.False(t, reloadWithinGuarded(t, t.Context(), s, 20*time.Millisecond))
	}
	require.True(t, reloadQueued(s))
	endSweep()

	require.Eventually(t, func() bool { return liveScanRange(s) == 500 }, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return !reloadWorkerRunning(s) }, 2*time.Second, 5*time.Millisecond)
	require.EqualValues(t, 1, repo.reads.Load(), "five saves queued behind one sweep must cost one reload")
}

func TestReloadWithinConcurrentCallersAllReturnAppliedAndNothingDeadlocks(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	saveScanRange(t, repo, cfg, 100)

	const callers = 8
	results := make(chan bool, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() { results <- s.ReloadWithin(t.Context(), 10*time.Second) })
	}
	wg.Wait()
	close(results)
	for applied := range results {
		require.True(t, applied)
	}
	requireLiveScanRange(t, s, 100, "every caller returned applied, so the config is live")
	require.Eventually(t, func() bool { return !reloadWorkerRunning(s) }, 2*time.Second, 5*time.Millisecond)
	reads := repo.reads.Load()
	require.GreaterOrEqual(t, reads, int32(1))
	require.LessOrEqual(t, reads, int32(callers), "callers may share a reload, never multiply it")
	requireSweepLockFree(t, s)
}

func TestReloadWithinFinishesAfterTheCallerDisconnects(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	endSweep := holdSweepLock(t, s)
	saveScanRange(t, repo, cfg, 100)

	ctx, disconnect := context.WithCancel(t.Context())
	returned := make(chan bool, 1)
	go func() { returned <- s.ReloadWithin(ctx, time.Minute) }()
	require.Eventually(t, func() bool { return reloadQueued(s) }, time.Second, time.Millisecond)
	disconnect() // the browser gave up

	select {
	case applied := <-returned:
		require.False(t, applied)
	case <-time.After(2 * time.Second):
		t.Fatal("ReloadWithin kept a disconnected caller waiting")
	}
	endSweep()
	require.Eventually(t, func() bool { return liveScanRange(s) == 100 }, 5*time.Second, 10*time.Millisecond,
		"the apply must still happen after the client is gone")
	require.EqualValues(t, 1, repo.reads.Load())
}

// A queued apply that is parked on sweepMu must not own mu: every reader of the
// service state (Config, wallet status, the sweep itself) takes mu.
func TestQueuedReloadNeverHoldsTheStateLockWhileWaitingForTheSweep(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	holdSweepLock(t, s)
	saveScanRange(t, repo, cfg, 100)
	require.False(t, reloadWithinGuarded(t, t.Context(), s, 20*time.Millisecond))
	require.True(t, reloadQueued(s))

	read := make(chan struct{})
	go func() {
		_ = s.Config()
		_, _ = s.walletManager()
		close(read)
	}()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("a queued reload holds the state lock while it waits for the sweep lock")
	}
}

func TestQueuedReloadStopsWhenTheServiceCloses(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	endSweep := holdSweepLock(t, s) // the sweep keeps running through shutdown
	saveScanRange(t, repo, cfg, 100)
	require.False(t, reloadWithinGuarded(t, t.Context(), s, 20*time.Millisecond))
	require.True(t, reloadWorkerRunning(s))

	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for the sweep or for the queued reload")
	}
	require.Eventually(t, func() bool { return !reloadWorkerRunning(s) }, 2*time.Second, 5*time.Millisecond,
		"the queued reload must give up on Close even though the sweep still owns the lock")
	require.EqualValues(t, 0, repo.reads.Load())
	require.False(t, reloadQueued(s))

	endSweep() // the sweep finally ends: the abandoned lock request must hand the lock straight back
	// Give it time to take the lock first: a TryLock right after the unlock would
	// barge in ahead of the woken waiter and pass even if the lock were never returned.
	time.Sleep(150 * time.Millisecond)
	requireSweepLockFree(t, s)
	require.EqualValues(t, 0, repo.reads.Load(), "nothing may be applied after Close")
}

// A sweep that starts after the save (the hourly automatic one blocks on Lock)
// must queue behind the pending apply instead of overtaking it and running on
// the old config, which for a changed sweep destination means the old address.
func TestQueuedReloadRunsBeforeASweepThatStartsLater(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	endSweep := holdSweepLock(t, s)
	saveScanRange(t, repo, cfg, 100)
	require.False(t, reloadWithinGuarded(t, t.Context(), s, 20*time.Millisecond))
	time.Sleep(150 * time.Millisecond) // the queued apply is parked on the lock first

	seen := make(chan int, 1)
	go func() {
		s.sweepMu.Lock() // the next sweep start
		defer s.sweepMu.Unlock()
		seen <- liveScanRange(s)
	}()
	time.Sleep(150 * time.Millisecond) // and the later sweep queues behind it
	endSweep()

	select {
	case live := <-seen:
		require.Equal(t, 100, live, "the next sweep must start on the saved config")
	case <-time.After(5 * time.Second):
		t.Fatal("the later sweep never got the lock")
	}
}

func TestReloadWithinAfterCloseAppliesNothing(t *testing.T) {
	s, repo, cfg := newAsyncReloadService(t)
	saveScanRange(t, repo, cfg, 100)
	s.Close()

	require.False(t, reloadWithinGuarded(t, t.Context(), s, 2*time.Second))
	require.EqualValues(t, 0, repo.reads.Load())
	require.Eventually(t, func() bool { return !reloadWorkerRunning(s) }, 2*time.Second, 5*time.Millisecond)
}

// gatedConfigRepository parks the first config read mid-reload, so a test can
// land a second save while the first apply is running.
type gatedConfigRepository struct {
	reloadConfigRepository
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (r *gatedConfigRepository) GetValue(ctx context.Context, key string) (string, error) {
	raw, err := r.reloadConfigRepository.GetValue(ctx, key)
	r.once.Do(func() {
		close(r.entered)
		<-r.release
	})
	return raw, err
}

func TestReloadWithinSaveDuringAnApplyIsNotLost(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	repo := &gatedConfigRepository{entered: make(chan struct{}), release: make(chan struct{})}
	cfg := DefaultCryptoConfig()
	saveScanRange(t, &repo.reloadConfigRepository, cfg, 100)
	s := &Service{settings: service.NewSettingService(repo, nil), wallet: wallet.New(nil)}
	t.Cleanup(func() { requireReloadWorkerStopped(t, s) })
	t.Cleanup(s.Close)
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(repo.release) }) })

	first := make(chan bool, 1)
	go func() { first <- s.ReloadWithin(t.Context(), 10*time.Second) }()
	select {
	case <-repo.entered: // the first apply holds sweepMu and has read the old config
	case <-time.After(5 * time.Second):
		t.Fatal("the first reload never started")
	}

	saveScanRange(t, &repo.reloadConfigRepository, cfg, 200) // a second save lands mid-apply
	require.False(t, reloadWithinGuarded(t, t.Context(), s, 20*time.Millisecond), "the running apply cannot include the newer save")
	releaseOnce.Do(func() { close(repo.release) })

	require.True(t, <-first)
	require.Eventually(t, func() bool { return liveScanRange(s) == 200 }, 5*time.Second, 10*time.Millisecond,
		"the save that landed mid-apply must be applied by a following reload")
	require.EqualValues(t, 2, repo.reads.Load())
}

// hungConfigRepository never answers a config read until its context ends, like a
// database that stopped responding.
type hungConfigRepository struct {
	reloadConfigRepository
}

func (r *hungConfigRepository) GetValue(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestQueuedReloadGivesUpAndFreesTheSweepLockWhenTheDatabaseHangs(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	previous := reloadApplyTimeout
	reloadApplyTimeout = 100 * time.Millisecond
	t.Cleanup(func() { reloadApplyTimeout = previous })
	s := &Service{settings: service.NewSettingService(&hungConfigRepository{}, nil), wallet: wallet.New(nil)}
	t.Cleanup(func() { requireReloadWorkerStopped(t, s) })
	t.Cleanup(s.Close)

	require.False(t, reloadWithinGuarded(t, t.Context(), s, 5*time.Second), "an apply cut off by its timeout is not applied yet")
	requireSweepLockFree(t, s) // a hung database must not pin the sweep lock forever
	require.Eventually(t, func() bool { return !reloadWorkerRunning(s) }, 2*time.Second, 5*time.Millisecond)
}

// failingConfigRepository answers every config read with an error, like a
// database that is up but refuses the query.
type failingConfigRepository struct {
	reloadConfigRepository
}

func (r *failingConfigRepository) GetValue(context.Context, string) (string, error) {
	return "", errors.New("database unavailable")
}

// ReloadWithin reports deferral only. An apply that ran and logged an error is
// retried by the config watcher exactly as before; telling the admin "waiting
// for the sweep to end" about it would be wrong (a security-locked wallet makes
// every apply report a guard error, for instance).
func TestReloadWithinReportsDeferralNotApplyErrors(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	s := &Service{settings: service.NewSettingService(&failingConfigRepository{}, nil), wallet: wallet.New(nil)}
	t.Cleanup(func() { requireReloadWorkerStopped(t, s) })
	t.Cleanup(s.Close)

	require.True(t, reloadWithinGuarded(t, t.Context(), s, 5*time.Second))
	s.mu.RLock()
	retry := s.reloadFailed
	s.mu.RUnlock()
	require.True(t, retry, "the config watcher must still retry the failed apply")
	requireSweepLockFree(t, s)
}

func withAdminSweepLockWait(t *testing.T, wait time.Duration) {
	t.Helper()
	previous := adminSweepLockWait
	adminSweepLockWait = wait
	t.Cleanup(func() { adminSweepLockWait = previous })
}

// The signer and vault changes serialise with sweeps too. While a sweep runs
// they must say so instead of hanging until the browser gives up.
func TestSignerAndVaultRequestsAnswerBusyWhileASweepRuns(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	withAdminSweepLockWait(t, 60*time.Millisecond)
	s := &Service{wallet: wallet.New(nil)}
	holdSweepLock(t, s)
	ctx := t.Context()

	for name, call := range map[string]func() error{
		"setup":                          func() error { _, err := s.SetupSignerShards(ctx, SignerSetupRequest{}); return err },
		"resplit":                        func() error { _, err := s.ResplitSignerShards(ctx, SignerSetupRequest{}); return err },
		"unlock":                         func() error { _, err := s.UnlockSignerWindow(ctx, "share"); return err },
		"enable automatic sweep":         func() error { _, err := s.EnableAutoSweep(ctx, "share"); return err },
		"disable automatic sweep":        func() error { _, err := s.DisableAutoSweep(ctx); return err },
		"import hot mnemonic with share": func() error { return s.ImportHotMnemonicWithShare(ctx, "words", "share") },
		"import hot mnemonic":            func() error { return s.ImportHotMnemonic(ctx, "words") },
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			require.ErrorIs(t, answersWithin(t, 3*time.Second, call), ErrSweepBusy)
			require.Less(t, time.Since(start), 2*time.Second)
		})
	}
}

func TestSignerRequestRidesOutAShortHolderOfTheSweepLock(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	withAdminSweepLockWait(t, 5*time.Second)
	s := &Service{wallet: wallet.New(nil)}
	endSweep := holdSweepLock(t, s)
	time.AfterFunc(80*time.Millisecond, endSweep) // e.g. the config watcher applying a change

	// The vault is not set up in this fixture, so the call itself fails; what
	// matters is that it got past the lock instead of answering busy.
	err := answersWithin(t, 4*time.Second, func() error { _, err := s.DisableAutoSweep(t.Context()); return err })
	require.NotErrorIs(t, err, ErrSweepBusy)
	requireSweepLockFree(t, s)
}

func TestSignerRequestStopsWaitingWhenTheClientDisconnects(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	withAdminSweepLockWait(t, 10*time.Second)
	s := &Service{wallet: wallet.New(nil)}
	holdSweepLock(t, s)

	ctx, disconnect := context.WithCancel(t.Context())
	time.AfterFunc(40*time.Millisecond, disconnect)
	start := time.Now()
	err := answersWithin(t, 3*time.Second, func() error { _, err := s.UnlockSignerWindow(ctx, "share"); return err })
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), 2*time.Second)
}
