package cryptopay

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

// Manual confirmation lets an authorised operator credit a pending deposit
// without waiting for confirmation depth or the outflow-security scan. It never
// writes a balance or a deposit status itself: every credit goes through
// ledger.Process, so address ownership, amount limits, the deterministic order
// identity and the unique deposit key all still apply.

const (
	DepositConfirmCredited        = "credited"
	DepositConfirmAlreadyCredited = "already_credited"
	DepositConfirmRejected        = "rejected"
	DepositConfirmError           = "error"

	// MaxDepositConfirmIDs bounds one request. Ids are processed one by one.
	MaxDepositConfirmIDs = 50

	depositConfirmBatchDeadline  = 3 * time.Minute
	depositConfirmVerifyTimeout  = 45 * time.Second
	depositConfirmProcessTimeout = 60 * time.Second
	depositConfirmReadTimeout    = 10 * time.Second

	// depositConfirmLockNamespace is the first key of a two-key advisory lock
	// ("CPMC"). The two-key form lives in a different key space from the
	// single-bigint locks the scanner and sweeper use.
	depositConfirmLockNamespace int32 = 0x43504d43
)

var (
	ErrDepositConfirmInvalid        = errors.New("invalid deposit confirmation request")
	ErrDepositConfirmUnavailable    = errors.New("deposit confirmation is unavailable")
	ErrDepositConfirmSecurityLocked = errors.New("deposit confirmation is refused while the wallet security lock is active")
	// ErrDepositConfirmReadOnly is the shared-database review gate; the handler
	// maps it to 403.
	ErrDepositConfirmReadOnly = mode.ErrReadOnly

	errDepositNoRPC = errors.New("no rpc endpoint is configured for this chain")
)

// DepositConfirmResult is one id's outcome. Status is one of the
// DepositConfirm* constants.
type DepositConfirmResult struct {
	ID      int64  `json:"id"`
	Status  string `json:"status"`
	OrderID *int64 `json:"order_id,omitempty"`
	Message string `json:"message"`
}

// DepositConfirmSummary is the response body. Credited counts ids this request
// credited; Failed counts rejected and error ids. An id that was already
// credited is neither: it needs no action.
type DepositConfirmSummary struct {
	Results  []DepositConfirmResult `json:"results"`
	Credited int                    `json:"credited"`
	Failed   int                    `json:"failed"`
}

// NormalizeDepositConfirmIDs removes duplicates, keeps first-seen order and
// enforces 1..MaxDepositConfirmIDs positive ids.
func NormalizeDepositConfirmIDs(ids []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrDepositConfirmInvalid
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 || len(out) > MaxDepositConfirmIDs {
		return nil, ErrDepositConfirmInvalid
	}
	return out, nil
}

type depositProcessor interface {
	Process(ctx context.Context, t scanner.Transfer, decimals int) (bool, error)
}

// depositLocker serialises confirmations of one deposit across requests and
// instances. TryLock returns (nil, nil) when another confirmation holds it.
type depositLocker interface {
	TryLock(ctx context.Context, id int64) (release func() error, err error)
}

type depositConfirmer struct {
	db        *sql.DB
	processor depositProcessor
	locker    depositLocker
	pool      func(chain string) (*rpc.Pool, error)
	adminID   int64
	minCredit float64
	maxCredit float64
}

type depositConfirmRow struct {
	id          int64
	chain       string
	txHash      string
	logIndex    int
	from, to    string
	token       string
	amount      string
	blockNumber int64
	blockHash   string
	status      string
	reason      string
	orderID     sql.NullInt64
}

func (c *depositConfirmer) load(ctx context.Context, id int64) (*depositConfirmRow, error) {
	var r depositConfirmRow
	err := c.db.QueryRowContext(ctx, `SELECT id, chain, tx_hash, log_index, from_address, to_address, token,
		CAST(amount AS TEXT), block_number, block_hash, status, reason, order_id
		FROM deposit_transactions WHERE id = $1`, id).
		Scan(&r.id, &r.chain, &r.txHash, &r.logIndex, &r.from, &r.to, &r.token,
			&r.amount, &r.blockNumber, &r.blockHash, &r.status, &r.reason, &r.orderID)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// depositPoolEntry caches one chain's verified pool for the whole batch. A
// timeout is never cached: it says nothing about the chain.
type depositPoolEntry struct {
	pool *rpc.Pool
	msg  string
}

func (c *depositConfirmer) chainPool(ctx context.Context, cache map[string]depositPoolEntry, chain string) (*rpc.Pool, string) {
	if entry, ok := cache[chain]; ok {
		return entry.pool, entry.msg
	}
	base, err := c.pool(chain)
	if err != nil {
		msg := "该链的 RPC 节点不可用，未做任何改动。"
		if errors.Is(err, errDepositNoRPC) {
			msg = "该链未配置 RPC 节点（crypto.config.rpc." + chain + "），无法做链上复核，未做任何改动。"
		}
		cache[chain] = depositPoolEntry{msg: msg}
		return nil, msg
	}
	probeCtx, cancel := context.WithTimeout(ctx, depositConfirmVerifyTimeout)
	defer cancel()
	// Same chain-id binding reconcile uses: an endpoint that answers for another
	// EVM chain is dropped before any receipt is requested.
	pool, err := scanner.VerifiedChainPool(probeCtx, base, chain)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, "RPC 节点探测超时，请稍后重试，未做任何改动。"
		}
		msg := "没有通过链 ID 校验的 RPC 节点（" + rpcFailureDetail(err) + "），未做任何改动。"
		cache[chain] = depositPoolEntry{msg: msg}
		return nil, msg
	}
	cache[chain] = depositPoolEntry{pool: pool}
	return pool, ""
}

// rpcFailureDetail exposes only the fixed category of an rpc.Failure. Anything
// else (a chain-id mismatch string, a joined list) collapses to one sentence so
// no endpoint or node-supplied text reaches the operator or the logs.
func rpcFailureDetail(err error) string {
	var failure *rpc.Failure
	if errors.As(err, &failure) {
		return failure.Error()
	}
	return "节点不可用或链 ID 不符"
}

func (c *depositConfirmer) run(ctx context.Context, ids []int64) *DepositConfirmSummary {
	summary := &DepositConfirmSummary{Results: make([]DepositConfirmResult, 0, len(ids))}
	cache := make(map[string]depositPoolEntry)
	for _, id := range ids {
		result := c.confirmOne(ctx, cache, id)
		switch result.Status {
		case DepositConfirmCredited:
			summary.Credited++
		case DepositConfirmAlreadyCredited:
		default:
			summary.Failed++
		}
		summary.Results = append(summary.Results, result)
	}
	return summary
}

// confirmOne handles one id. An error in one id never affects another.
func (c *depositConfirmer) confirmOne(ctx context.Context, cache map[string]depositPoolEntry, id int64) (result DepositConfirmResult) {
	var row *depositConfirmRow
	defer func() { c.logResult(result, row) }()
	fail := func(status, message string) DepositConfirmResult {
		return DepositConfirmResult{ID: id, Status: status, Message: message}
	}
	if ctx.Err() != nil {
		return fail(DepositConfirmError, "批量处理已超时，这一条尚未处理，可安全重试。")
	}

	release, err := c.locker.TryLock(ctx, id)
	if err != nil {
		slog.Warn("[cryptopay] manual deposit lock failed", "deposit_id", id, "cause", rpc.SafeError(err))
		return fail(DepositConfirmError, "无法获取这条记录的处理锁，请稍后重试，未做任何改动。")
	}
	if release == nil {
		return fail(DepositConfirmError, "这条记录正在被另一次确认处理，请稍后刷新查看结果。")
	}
	defer func() {
		if err := release(); err != nil {
			slog.Warn("[cryptopay] manual deposit lock release failed", "deposit_id", id, "cause", rpc.SafeError(err))
		}
	}()

	loaded, err := c.load(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(DepositConfirmRejected, "记录不存在。")
	}
	if err != nil {
		slog.Warn("[cryptopay] manual deposit read failed", "deposit_id", id, "cause", rpc.SafeError(err))
		return fail(DepositConfirmError, "读取记录失败，请稍后重试，未做任何改动。")
	}
	row = loaded

	switch row.status {
	case "pending":
	case "credited":
		return DepositConfirmResult{ID: id, Status: DepositConfirmAlreadyCredited, OrderID: orderPtr(row.orderID),
			Message: "这条记录已经入账，无需重复确认。"}
	default:
		return fail(DepositConfirmRejected, fmt.Sprintf("记录状态是 %s（%s），不能手动入账。",
			cleanText(row.status, 16), depositReasonText(row.reason, c.minCredit, c.maxCredit)))
	}

	if !IsEVMChain(row.chain) {
		return fail(DepositConfirmRejected, "只支持 EVM 链上的充值手动入账。")
	}
	// Decimals come only from the trusted token table. Unlike tokenDecimals there
	// is no default of 6: an unlisted contract is refused, never guessed.
	decimals, watched := watchedTokenDecimals(row.chain, row.token)
	if !watched {
		return fail(DepositConfirmRejected, "这个代币不在该链的受信代币清单里，不能手动入账。")
	}
	if !canonicalTxHash(row.txHash) {
		return fail(DepositConfirmRejected, "记录里的 tx_hash 不是规范的小写十六进制，需要人工核对。")
	}
	if strings.TrimSpace(row.blockHash) == "" || row.blockNumber <= 0 {
		return fail(DepositConfirmRejected, "记录缺少区块号或区块哈希，无法做防重组复核，需要人工核对。")
	}

	pool, poolMsg := c.chainPool(ctx, cache, row.chain)
	if pool == nil {
		return fail(DepositConfirmError, poolMsg)
	}

	verifyCtx, cancelVerify := context.WithTimeout(ctx, depositConfirmVerifyTimeout)
	transfer, err := scanner.VerifyStoredDeposit(verifyCtx, pool, scanner.StoredDeposit{
		Chain: row.chain, TxHash: row.txHash, LogIndex: row.logIndex, From: row.from, To: row.to,
		Token: row.token, Amount: row.amount, BlockNumber: uint64(row.blockNumber), BlockHash: row.blockHash,
	}, decimals)
	cancelVerify()
	if err != nil {
		status, message := classifyDepositVerifyError(err)
		return fail(status, message)
	}

	// Once the chain agrees, the credit must be allowed to finish even if the
	// batch clock ran out or the operator closed the tab. Process is resumable
	// and idempotent, so a bounded detached context is safe.
	processCtx, cancelProcess := context.WithTimeout(context.WithoutCancel(ctx), depositConfirmProcessTimeout)
	credited, processErr := c.processor.Process(processCtx, transfer, decimals)
	cancelProcess()
	if processErr != nil {
		slog.Warn("[cryptopay] manual deposit credit failed", "deposit_id", id, "cause", rpc.SafeError(processErr))
	}

	// The row, not the return value, is the truth: Process also returns without
	// error when it declined to credit.
	readCtx, cancelRead := context.WithTimeout(context.WithoutCancel(ctx), depositConfirmReadTimeout)
	after, readErr := c.load(readCtx, id)
	cancelRead()
	if readErr != nil {
		slog.Warn("[cryptopay] manual deposit read-back failed", "deposit_id", id, "cause", rpc.SafeError(readErr))
		return fail(DepositConfirmError, "已提交入账处理，但读不回结果；请刷新到账记录确认。重复确认不会重复入账。")
	}
	row = after

	switch after.status {
	case "credited":
		out := DepositConfirmResult{ID: id, OrderID: orderPtr(after.orderID)}
		if credited {
			out.Status, out.Message = DepositConfirmCredited, "已入账。"
		} else {
			out.Status, out.Message = DepositConfirmAlreadyCredited, "这条记录已被入账（并发处理），本次没有重复入账。"
		}
		return out
	case "pending":
		if processErr != nil {
			return fail(DepositConfirmError, depositProcessErrorMessage(processErr))
		}
		return fail(DepositConfirmRejected, "账本拒绝入账：发送方是系统自有地址，或收款地址不属于任何用户。记录保持待处理，余额未变。")
	case "ignored":
		return fail(DepositConfirmRejected, "账本按当前规则忽略了这笔充值（"+
			depositReasonText(after.reason, c.minCredit, c.maxCredit)+"），没有入账。")
	default:
		return fail(DepositConfirmRejected, fmt.Sprintf("记录现在的状态是 %s（%s），需要人工核对。",
			cleanText(after.status, 16), depositReasonText(after.reason, c.minCredit, c.maxCredit)))
	}
}

func (c *depositConfirmer) logResult(result DepositConfirmResult, row *depositConfirmRow) {
	attrs := []any{"admin_id", c.adminID, "deposit_id", result.ID, "status", result.Status}
	if row != nil {
		attrs = append(attrs, "chain", cleanText(row.chain, 32), "tx_hash", cleanText(row.txHash, 66), "log_index", row.logIndex)
	}
	if result.OrderID != nil {
		attrs = append(attrs, "order_id", *result.OrderID)
	}
	if result.Status == DepositConfirmCredited || result.Status == DepositConfirmAlreadyCredited {
		slog.Info("[cryptopay] manual deposit confirmation", attrs...)
		return
	}
	slog.Warn("[cryptopay] manual deposit confirmation", attrs...)
}

func orderPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func depositProcessErrorMessage(err error) string {
	switch {
	case errors.Is(err, mode.ErrReadOnly):
		return "当前实例处于只读测试模式，没有入账。"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "入账处理超时或被取消，结果未知；请刷新到账记录确认。重复确认不会重复入账。"
	default:
		return "入账处理失败，记录仍是待处理，可稍后重试。重复确认不会重复入账。"
	}
}

// classifyDepositVerifyError separates "the chain says no" (rejected) from "we
// could not ask" (error). Free text from the cause never reaches the operator:
// only fixed sentences and rpc.SafeError output are used.
func classifyDepositVerifyError(err error) (status, message string) {
	switch {
	case errors.Is(err, scanner.ErrTransactionReverted):
		return DepositConfirmRejected, "链上这笔交易已回滚（receipt status=0），不能入账。"
	case errors.Is(err, scanner.ErrNonCanonicalBlock):
		return DepositConfirmRejected, "记录里的区块已不在规范链上（疑似链重组），不能入账。"
	case errors.Is(err, scanner.ErrTransferReincluded):
		return DepositConfirmRejected, "这笔转账在链上换了区块或日志位置（疑似链重组），库里的记录已过期，不能入账；请等扫描器重新核对。"
	case errors.Is(err, scanner.ErrTransferMismatch):
		return DepositConfirmRejected, "链上收据里没有与记录完全一致的唯一 Transfer 日志（代币、发送方、收款方、金额、日志序号），不能入账。"
	case errors.Is(err, scanner.ErrStoredDepositInvalid):
		return DepositConfirmRejected, "库里记录的金额格式或精度异常，无法与链上比对，需要人工核对。"
	case errors.Is(err, scanner.ErrReceiptNotFound):
		return DepositConfirmError, "节点暂时没有返回这笔交易的收据（可能节点索引滞后，也可能交易已被重组掉），请稍后重试。"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return DepositConfirmError, "链上复核超时，请稍后重试，未做任何改动。"
	}
	return DepositConfirmError, "链上复核失败（" + rpc.SafeError(err) + "），未做任何改动，请稍后重试。"
}

func depositReasonText(reason string, minAmount, maxAmount float64) string {
	switch reason {
	case "":
		return "无原因记录"
	case ledger.ReasonBelowMin:
		return fmt.Sprintf("低于最小入账金额 %g USDT", minAmount)
	case ledger.ReasonAboveMax:
		return fmt.Sprintf("高于单笔上限 %g USDT", maxAmount)
	case ledger.ReasonUnassigned:
		return "收款地址没有分配给用户"
	case ledger.ReasonUnknownAddress:
		return "收款地址不是充值地址"
	case "reorg_duplicate_credit_manual_review":
		return "同一笔交易在重组前已入账，需要人工核对"
	}
	if strings.HasPrefix(reason, "reorg_") {
		return "链重组使记录失效：" + cleanText(reason, 48)
	}
	return cleanText(reason, 64)
}

// watchedTokenDecimals resolves decimals from the same table the scanner and
// sweeper use (watchedTokens), with no fallback for unlisted contracts.
func watchedTokenDecimals(chain, contract string) (int, bool) {
	for _, token := range watchedTokens[chain] {
		if strings.EqualFold(token.Contract, contract) {
			return token.Decimals, true
		}
	}
	return 0, false
}

func canonicalTxHash(hash string) bool {
	if len(hash) != 66 || !strings.HasPrefix(hash, "0x") {
		return false
	}
	for _, r := range hash[2:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// cleanText bounds and sanitises database text before it is logged or echoed.
func cleanText(value string, limit int) string {
	var b strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(value) {
		if count >= limit {
			_, _ = b.WriteString("…")
			break
		}
		if !unicode.IsGraphic(r) {
			r = '?'
		}
		_, _ = b.WriteRune(r)
		count++
	}
	return b.String()
}

// selectDepositRPCPool prefers the service's shared pool for the chain (which
// keeps its discovery provenance and verification policy). A chain that is not
// scanned has no usable shared pool, so a temporary manual-trust pool is built
// from the stored cfg.RPC entry. The shared pool is never mutated.
func selectDepositRPCPool(shared *rpc.Pool, configured string) (*rpc.Pool, error) {
	if shared != nil && len(shared.Endpoints()) > 0 {
		return shared, nil
	}
	if strings.TrimSpace(configured) != "" {
		return rpc.NewPool(configured), nil
	}
	return nil, errDepositNoRPC
}

func (s *Service) depositPool(chain string) (*rpc.Pool, error) {
	s.mu.RLock()
	shared := s.pools[chain]
	configured := s.config.RPC[chain]
	s.mu.RUnlock()
	return selectDepositRPCPool(shared, configured)
}

func (s *Service) newDepositConfirmer(adminID int64, cfg CryptoConfig) *depositConfirmer {
	processor := ledger.New(s.db, s.creditor, newAddressResolver(s.addresses),
		cfg.CreditMinAmount, cfg.CreditMaxAmount, cfg.CreditMultiplier)
	// Manual confirmation settles native deposits too: a missing feed would
	// leave them pending instead of booking them at a guessed price.
	processor.SetNativePricer(s.liveNativePricer())
	return &depositConfirmer{
		db:        s.db,
		processor: processor,
		locker:    pgDepositLocker{db: s.db},
		pool:      s.depositPool,
		adminID:   adminID,
		minCredit: cfg.CreditMinAmount,
		maxCredit: cfg.CreditMaxAmount,
	}
}

// liveNativePricer returns the process-wide feed, or nil when the service has
// not been reloaded yet. A nil pricer fails closed for native deposits.
func (s *Service) liveNativePricer() ledger.NativePricer {
	s.mu.RLock()
	p := s.nativePrice
	s.mu.RUnlock()
	if p == nil {
		return nil
	}
	return p
}

// depositConfirmGate refuses while the wallet security lock is active. A lock
// means an unauthorised outflow was seen, so no new liability is added until an
// operator has recovered it. An unreadable security state fails closed.
func (s *Service) depositConfirmGate(ctx context.Context) error {
	if s.securityPaused.Load() {
		return ErrDepositConfirmSecurityLocked
	}
	if s.wallet == nil {
		return nil
	}
	status, err := s.WalletSecurity(ctx)
	if err != nil {
		slog.Warn("[cryptopay] manual deposit confirmation cannot read wallet security", "cause", rpc.SafeError(err))
		return ErrDepositConfirmUnavailable
	}
	if status.Locked {
		return ErrDepositConfirmSecurityLocked
	}
	return nil
}

// ConfirmDeposits credits the given pending deposits after an on-chain recheck.
// adminID is the authenticated operator; note is free text kept only in a
// bounded log field (the request body, redacted, is also in the audit log).
// The returned error is reserved for whole-request failures; per-id problems
// are in the summary.
func (s *Service) ConfirmDeposits(ctx context.Context, adminID int64, ids []int64, note string) (*DepositConfirmSummary, error) {
	if s == nil || s.db == nil || s.creditor == nil || s.addresses == nil {
		return nil, ErrDepositConfirmUnavailable
	}
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	if adminID <= 0 {
		return nil, ErrDepositConfirmInvalid
	}
	ids, err := NormalizeDepositConfirmIDs(ids)
	if err != nil {
		return nil, err
	}
	// The handler hands down a context with no deadline, so bound everything,
	// including the security-state read, by the batch deadline.
	ctx, cancel := context.WithTimeout(ctx, depositConfirmBatchDeadline)
	defer cancel()
	if err := s.depositConfirmGate(ctx); err != nil {
		return nil, err
	}
	confirmer := s.newDepositConfirmer(adminID, s.Config())
	slog.Info("[cryptopay] manual deposit confirmation requested",
		"admin_id", adminID, "requested", len(ids), "note", cleanText(note, 200))
	summary := confirmer.run(ctx, ids)
	slog.Info("[cryptopay] manual deposit confirmation finished",
		"admin_id", adminID, "requested", len(ids), "credited", summary.Credited, "failed", summary.Failed)
	return summary, nil
}

// pgDepositLocker holds a per-deposit PostgreSQL session advisory lock on a
// dedicated connection for the whole confirmation. A row lock (FOR UPDATE) is
// deliberately not used: the ledger and creditor run on other connections, and
// their UPDATE of the same row would wait on it forever.
type pgDepositLocker struct{ db *sql.DB }

func depositLockKeys(id int64) (int32, int32) {
	folded := uint64(id)
	return depositConfirmLockNamespace, int32((folded ^ (folded >> 32)) & 0x7fffffff)
}

func (l pgDepositLocker) TryLock(ctx context.Context, id int64) (func() error, error) {
	if l.db == nil {
		return nil, errors.New("deposit lock database is not configured")
	}
	if l.db.Stats().MaxOpenConnections == 1 {
		return nil, errors.New("manual deposit confirmation requires at least two database connections (lock and ledger queries)")
	}
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("deposit lock connection: %w", err)
	}
	// Raw with ErrBadConn closes the physical session rather than pooling it, so
	// a lock whose state is uncertain can never be inherited by another caller.
	discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	space, key := depositLockKeys(id)
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, space, key).Scan(&locked); err != nil {
		discard()
		return nil, fmt.Errorf("acquire deposit lock: %w", err)
	}
	if !locked {
		return nil, conn.Close()
	}
	return func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		err := conn.QueryRowContext(cleanup, `SELECT pg_advisory_unlock($1, $2)`, space, key).Scan(&unlocked)
		if err != nil || !unlocked {
			discard()
			if err == nil {
				err = errors.New("deposit advisory lock was not held")
			}
			return fmt.Errorf("release deposit lock: %w", err)
		}
		return conn.Close()
	}, nil
}
