package cryptopay

import (
	"errors"
	"time"
)

// Chain states reported by the admin "node status" page.
const (
	// ChainStateDisabled: RPC nodes are configured but the chain is not in
	// crypto.config.chains, so the scanner skips it silently. This is the BSC
	// incident: the cursor froze for hours with nothing in the logs.
	ChainStateDisabled = "disabled"
	// ChainStateNoRPC: enabled, but there is no RPC source of any kind.
	ChainStateNoRPC = "no_rpc"
	// ChainStateError: no node produced a usable head (or the cursor is unreadable).
	ChainStateError = "error"
	// ChainStateStalled: backlog above the threshold and the cursor is not moving.
	ChainStateStalled = "stalled"
	// ChainStateCatchingUp: backlog above the threshold but the cursor is moving.
	ChainStateCatchingUp = "catching_up"
	ChainStateSynced     = "synced"
)

// Node statuses.
const (
	NodeStatusOK         = "ok"
	NodeStatusSlow       = "slow"
	NodeStatusBehind     = "behind"
	NodeStatusError      = "error"
	NodeStatusWrongChain = "wrong_chain"
)

// Fixed error vocabulary. Text returned by a node or a driver is never echoed:
// it can carry the endpoint URL, an API key or arbitrary upstream content.
const (
	NodeErrTimeout    = "timeout"
	NodeErrConnection = "connection"
	NodeErrHTTP429    = "http_429"
	NodeErrHTTP5xx    = "http_5xx"
	NodeErrHTTP4xx    = "http_4xx"
	NodeErrRPC        = "rpc_error"
	NodeErrBadResp    = "bad_response"
	NodeErrWrongChain = "wrong_chain"
	// ErrKindDB marks a failed cursor read; it only appears in last_error.
	ErrKindDB = "db_error"
)

// last_error stages.
const (
	ErrStageHead   = "head"
	ErrStageCursor = "cursor"
)

// Thresholds. The backlog threshold is derived from the chain's own observed
// block rate instead of a fixed block count: 200 blocks is about two and a half
// minutes on a sub-second chain but more than half an hour on Ethereum.
const (
	// chainBacklogWindow is how much chain time of unscanned blocks still counts
	// as synced. 200 blocks on a ~80 blocks/min chain.
	chainBacklogWindow = 150 * time.Second
	// chainBacklogDefaultBlocks applies while the block rate is unknown.
	chainBacklogDefaultBlocks = 200
	chainBacklogMinBlocks     = 10
	chainBacklogMaxBlocks     = 20000
	// chainStalledAfter: an unmoved cursor with a backlog is "stalled" past this.
	chainStalledAfter = 180 * time.Second

	nodeSlowAfter    = 2000 * time.Millisecond
	nodeBehindBlocks = 20
	nodeProbeTimeout = 5 * time.Second

	chainProbeCacheTTL = 5 * time.Second

	chainSampleRetention  = 15 * time.Minute
	chainSampleMinSpacing = 5 * time.Second
	chainSampleCapacity   = 256
	// Speeds need a long enough baseline: the cursor advances one batch at a
	// time, so a short window would read as zero or as a spike.
	chainSpeedMinSpan    = 90 * time.Second
	chainSpeedMinSamples = 2

	chainTickInterval = 15 * time.Second
	chainWatchWindow  = 10 * time.Minute
	chainTickBudget   = 20 * time.Second
	chainDBTimeout    = 3 * time.Second

	// A net catch-up rate below this (blocks/minute) is treated as "not catching up".
	chainNetSpeedEpsilon = 0.01
	chainETAMaxSeconds   = 7 * 24 * 3600
)

var (
	ErrChainNotConfigured     = errors.New("chain has no RPC nodes configured")
	ErrChainHeadUnavailable   = errors.New("no RPC node returned a usable chain head")
	ErrChainCursorUnavailable = errors.New("scan cursor could not be read")

	errChainPendingUnavailable = errors.New("pending deposit count could not be read")
)

// ChainStatusReport is the response of GET /admin/crypto/chains/status.
type ChainStatusReport struct {
	GeneratedAt string             `json:"generated_at"`
	Summary     ChainStatusSummary `json:"summary"`
	Chains      []ChainStatus      `json:"chains"`
}

// ChainStatusSummary counts enabled chains by health. no_rpc counts as error.
type ChainStatusSummary struct {
	Enabled int `json:"enabled"`
	Lagging int `json:"lagging"`
	Stalled int `json:"stalled"`
	Error   int `json:"error"`
}

// ChainStatus is one chain row. A nil pointer means "unknown", never zero.
type ChainStatus struct {
	Chain           string            `json:"chain"`
	Enabled         bool              `json:"enabled"`
	State           string            `json:"state"`
	Confirmations   int               `json:"confirmations"`
	ScanRange       int               `json:"scan_range"`
	Head            *uint64           `json:"head"`
	ConfirmedHead   *uint64           `json:"confirmed_head"`
	Cursor          *uint64           `json:"cursor"`
	Unscanned       *uint64           `json:"unscanned"`
	ProgressPct     *float64          `json:"progress_pct"`
	ScanSpeedBPM    *float64          `json:"scan_speed_bpm"`
	HeadSpeedBPM    *float64          `json:"head_speed_bpm"`
	ETASeconds      *int64            `json:"eta_seconds"`
	CursorUpdatedAt *string           `json:"cursor_updated_at"`
	StalledSeconds  *int64            `json:"stalled_seconds"`
	PendingDeposits *int64            `json:"pending_deposits"`
	LastError       *ChainLastError   `json:"last_error"`
	Nodes           []ChainNodeStatus `json:"nodes"`
}

// ChainLastError carries fixed words only.
type ChainLastError struct {
	Kind  string `json:"kind"`
	Stage string `json:"stage"`
}

// ChainNodeStatus is one RPC node. Label and Host are the only identity shown:
// the path, query, userinfo and port of the configured URL never leave the server.
type ChainNodeStatus struct {
	Label     string  `json:"label"`
	Host      string  `json:"host"`
	Status    string  `json:"status"`
	LatencyMS *int64  `json:"latency_ms"`
	Head      *uint64 `json:"head"`
	Behind    *uint64 `json:"behind"`
	ErrorKind *string `json:"error_kind"`
}

// ChainProgress is the reusable per-chain scan position. The backfill job
// reports the same numbers through Service.ChainProgress instead of keeping a
// second implementation.
type ChainProgress struct {
	Chain         string `json:"chain"`
	Head          uint64 `json:"head"`
	ConfirmedHead uint64 `json:"confirmed_head"`
	Cursor        uint64 `json:"cursor"`
	// CursorKnown is false while the chain has no cursor row yet. Unscanned is
	// then the whole confirmed height, so it never under-reports.
	CursorKnown bool   `json:"cursor_known"`
	Unscanned   uint64 `json:"unscanned"`
}
