// Package handler: crypto deposit channel HTTP surface.
//
// User endpoints expose the caller's deposit addresses and deposit history.
// Admin endpoints manage the channel config (including RPC endpoints),
// address status and sweep records.
package handler

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// CryptoHandler serves the built-in crypto deposit channel.
type CryptoHandler struct {
	addresses *addresses.Service
	store     cryptopay.SettingStore
	service   *cryptopay.Service
}

func NewCryptoHandler(addressService *addresses.Service, settings *service.SettingService, cryptoSvc *cryptopay.Service) *CryptoHandler {
	return &CryptoHandler{
		addresses: addressService,
		store:     cryptopay.NewServiceSettingStore(settings),
		service:   cryptoSvc,
	}
}

// GetDepositAddresses returns (or lazily assigns) the caller's addresses.
// GET /api/v1/crypto/deposit-addresses
func (h *CryptoHandler) GetDepositAddresses(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	cfg, err := cryptopay.LoadConfig(c.Request.Context(), h.store)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !cfg.Enabled {
		response.ErrorFrom(c, errCryptoDisabled)
		return
	}
	assigned, err := h.addresses.GetOrAssign(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	chains := make([]gin.H, 0, len(cfg.Chains))
	for _, chain := range cfg.Chains {
		family := addresses.FamilyEVM
		if chain == cryptopay.ChainSolana {
			family = addresses.FamilySolana
		}
		chains = append(chains, gin.H{
			"chain":   chain,
			"address": assigned[family],
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"evm":    assigned[addresses.FamilyEVM],
		"solana": assigned[addresses.FamilySolana],
		"chains": chains,
	}})
}

// AdminGetConfig returns the channel config (admin).
// GET /api/v1/admin/crypto/config
func (h *CryptoHandler) AdminGetConfig(c *gin.Context) {
	cfg, err := cryptopay.LoadConfig(c.Request.Context(), h.store)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": cfg})
}

// AdminUpdateConfig validates and persists the channel config (admin).
// PUT /api/v1/admin/crypto/config
func (h *CryptoHandler) AdminUpdateConfig(c *gin.Context) {
	// Merge over the stored config: a payload that omits keys (an older
	// console, a scripted PUT) must not silently wipe them to zero values.
	existing, err := cryptopay.LoadConfig(c.Request.Context(), h.store)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var patch cryptopay.CryptoConfigPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		response.BadRequest(c, "Invalid config payload")
		return
	}
	merged := patch.Apply(existing)
	if merged.HotIndex != existing.HotIndex {
		response.BadRequest(c, "gas 钱包索引由签名服务独立策略管理，请在签名策略中修改")
		return
	}
	if h.service != nil && merged.SignerSocket != existing.SignerSocket {
		if err := h.service.ValidateSignerChange(c.Request.Context(), merged.SignerSocket); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}
	if err := cryptopay.SaveConfig(c.Request.Context(), h.store, merged); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// A receiving-address change must never grant signer approval. Its
	// independently configured policy decides which targets may receive funds.
	// Apply immediately: rebuild pools and restart the scan worker.
	if h.service != nil {
		h.service.Reload(c.Request.Context())
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true}})
}

// AdminSignerStatus reports whether the signing service is unlocked.
// GET /api/v1/admin/crypto/signer/status
func (h *CryptoHandler) AdminSignerStatus(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	st, err := h.service.SignerStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": st})
}

// AdminUnlockSigner forwards the operator passphrase to the signer.
// POST /api/v1/admin/crypto/signer/unlock
func (h *CryptoHandler) AdminUnlockSigner(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	var body struct {
		Passphrase      string `json:"passphrase"`
		DepositMnemonic string `json:"deposit_mnemonic"`
		HotMnemonic     string `json:"hot_mnemonic"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid request payload")
		return
	}
	if strings.TrimSpace(body.Passphrase) == "" {
		response.BadRequest(c, "passphrase is required")
		return
	}
	if err := h.service.UnlockSigner(c.Request.Context(), body.Passphrase, body.DepositMnemonic, body.HotMnemonic); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"unlocked": true}})
}

// AdminPoolStatus reports remaining unassigned addresses per family.
// GET /api/v1/admin/crypto/pool
func (h *CryptoHandler) AdminPoolStatus(c *gin.Context) {
	if h.addresses == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	st, err := h.addresses.PoolStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": st})
}

// AdminHotWalletStatus reports gas readiness per chain.
// GET /api/v1/admin/crypto/hot-wallet
func (h *CryptoHandler) AdminHotWalletStatus(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	st, err := h.service.HotWalletStatuses(c.Request.Context())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": st})
}

// AdminHotWalletAddress returns the address the hot wallet actually derives
// to, so the operator can paste it into the sweep destination fields instead
// of guessing (a wrong destination sends swept funds into the void).
// GET /api/v1/admin/crypto/hot-wallet/address
func (h *CryptoHandler) AdminHotWalletAddress(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	st, err := h.service.HotAddresses(c.Request.Context())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": st})
}

// csvCell neutralises spreadsheet formula injection: a wallet-controlled
// address starting with = + - @ would otherwise execute on open.
func csvCell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}

// AdminExportDeposits streams the deposit ledger as CSV for reconciliation.
// GET /api/v1/admin/crypto/deposits.csv
func (h *CryptoHandler) AdminExportDeposits(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	// Page through the whole ledger: the reader caps one page at 200, and
	// an export that silently stops at 50 rows cannot be reconciled.
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="crypto-deposits.csv"`)
	// Excel decides charset by BOM; without it Chinese and quotes mojibake.
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"id", "chain", "tx_hash", "log_index", "from", "to", "token", "amount", "status", "created_at"})
	const pageSize = 200
	for offset := 0; ; offset += pageSize {
		records, summary, err := h.service.ListDepositsPaged(c.Request.Context(),
			c.Query("chain"), c.Query("status"), pageSize, offset)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		for _, r := range records {
			_ = w.Write([]string{
				csvCell(strconv.FormatInt(r.ID, 10)), csvCell(r.Chain), csvCell(r.TxHash),
				strconv.Itoa(r.LogIndex), csvCell(r.FromAddress), csvCell(r.ToAddress),
				csvCell(r.Token), csvCell(r.Amount), csvCell(r.Status), csvCell(r.CreatedAt),
			})
		}
		if len(records) == 0 || summary == nil || int64(offset+len(records)) >= summary.Total {
			break
		}
	}
	w.Flush()
}

// AdminSweepPlan returns the current dry-run sweep plan.
// GET /api/v1/admin/crypto/sweep/plan
func (h *CryptoHandler) AdminSweepPlan(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	plan, err := h.service.BuildSweepPlan(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": plan})
}

// AdminRunSweep executes the sweep. ?dry_run=0 moves funds; the default is
// a dry run so a stray click can never move money.
// POST /api/v1/admin/crypto/sweep/run
func (h *CryptoHandler) AdminRunSweep(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	dryRun := c.DefaultQuery("dry_run", "1") != "0"
	// A real sweep without a configured destination is operator error, not a
	// server fault: say what is missing instead of surfacing a 500.
	if !dryRun {
		if err := h.service.SweepReadiness(); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}
	summary, err := h.service.RunSweep(c.Request.Context(), dryRun)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": summary})
}

// AdminSweepRecords lists sweep history.
// GET /api/v1/admin/crypto/sweep/records
func (h *CryptoHandler) AdminSweepRecords(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	records, err := h.service.ListSweepRecords(c.Request.Context(), c.Query("chain"), 100)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": records})
}

// AdminListDeposits lists observed deposits for reconciliation.
// GET /api/v1/admin/crypto/deposits
func (h *CryptoHandler) AdminListDeposits(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	deposits, summary, err := h.service.ListDepositsPaged(c.Request.Context(),
		c.Query("chain"), c.Query("status"), pageSize, (page-1)*pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": deposits, "summary": summary}})
}

// AdminListAddresses lists assignments with optional family/status filters.
// GET /api/v1/admin/crypto/addresses
func (h *CryptoHandler) AdminListAddresses(c *gin.Context) {
	// user_id is optional now: without it the console shows every address
	// with aggregated credited/swept totals (the operator's main view).
	var userID int64
	if raw := c.Query("user_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			response.BadRequest(c, "invalid user_id")
			return
		}
		userID = parsed
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	family := c.Query("family")
	list, total, err := h.addresses.AdminList(c.Request.Context(), userID, family, pageSize, (page-1)*pageSize,
		c.Query("sort_by"), c.Query("sort_dir"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Summary covers the whole filtered set; a page-only total would
	// misreport once the operator pages past the first screen.
	summary, err := h.addresses.AdminSummary(c.Request.Context(), userID, family)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": list, "total": total, "summary": summary}})
}

// AdminSetAddressStatus enables/disables an assignment.
// POST /api/v1/admin/crypto/addresses/:id/status
func (h *CryptoHandler) AdminSetAddressStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid status payload")
		return
	}
	if err := h.addresses.SetStatus(c.Request.Context(), id, body.Status); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"updated": true}})
}

var errCryptoDisabled = &cryptoDisabledError{}

type cryptoDisabledError struct{}

func (e *cryptoDisabledError) Error() string { return "crypto deposit channel is disabled" }

// AdminTestRPC probes one RPC endpoint: it must answer eth_chainId (and the
// chain's block number) within a short timeout. The console uses this to
// validate endpoints before saving them into a chain's rotation list.
// POST /api/v1/admin/crypto/rpc/test
func (h *CryptoHandler) AdminTestRPC(c *gin.Context) {
	var body struct {
		Chain    string `json:"chain"`
		Endpoint string `json:"endpoint"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid test payload")
		return
	}
	result := cryptopay.ProbeRPCEndpoint(c.Request.Context(), body.Chain, body.Endpoint)
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// AdminChainlistCandidates fetches candidate RPC endpoints for a chain from
// the public chainlist.org registry. The console shows them for one-click
// adoption; saving still goes through the normal config PUT.
// GET /api/v1/admin/crypto/rpc/candidates?chain=bsc
func (h *CryptoHandler) AdminChainlistCandidates(c *gin.Context) {
	chain := c.Query("chain")
	if strings.TrimSpace(chain) == "" {
		response.BadRequest(c, "chain is required")
		return
	}
	result, err := cryptopay.FetchChainlistCandidates(c.Request.Context(), chain)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// AdminAutoPickRPC fetches the chain's public endpoints, probes them in
// parallel and returns the fastest healthy ones, skipping endpoints already
// configured. The console writes the returned list straight into the form.
// POST /api/v1/admin/crypto/rpc/auto-pick
func (h *CryptoHandler) AdminAutoPickRPC(c *gin.Context) {
	var body struct {
		Chain   string   `json:"chain"`
		Want    int      `json:"want"`
		Exclude []string `json:"exclude"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid auto-pick payload")
		return
	}
	if strings.TrimSpace(body.Chain) == "" {
		response.BadRequest(c, "chain is required")
		return
	}
	// Probing dozens of public endpoints takes a while; give it room without
	// letting a hung endpoint pin the request forever.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	candidates, err := cryptopay.FetchChainlistCandidates(ctx, body.Chain)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	picked := cryptopay.ProbeBestRPCEndpoints(ctx, body.Chain, candidates, body.Exclude, body.Want)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"picked": picked, "probed": len(candidates)}})
}
