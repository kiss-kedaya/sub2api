package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// Wallet shard setup endpoints.
//
// Threat model: the admin browser generates the vault data key and splits it
// into a 2-of-3 shamir-gf256 set. The server receives only share A (sealed with
// the installation key) plus the re-encrypted vault ciphertext and a public
// commitment. Shares B and C never reach the server. A manual sweep presents
// one operator share; the server combines it with A in memory, decrypts only
// what the operation needs, and zeroes the key immediately afterwards.
const (
	// SignerRiskPhraseZH/EN are downloaded by the UI and checked again on the
	// server: a disabled button is not a security control.
	SignerRiskPhraseZH = "我已离线保存分片，丢失无法找回"
	SignerRiskPhraseEN = "I have saved my shards offline and understand they cannot be recovered"
)

var signerRiskPointsZH = []string{
	"分片 B 和 C 只显示这一次，关闭页面后无法再次查看。",
	"两份分片全部丢失后，即使服务器管理员也无法恢复，网站所有收款地址的母钱包将永久无法动用。",
	"不要截图、不要通过聊天工具传输；请离线抄写或打印后分开保存。",
}

var signerRiskPointsEN = []string{
	"Shares B and C are shown exactly once; they cannot be revealed again after this page closes.",
	"If both shares are lost, nobody - including the server operator - can recover the wallet, and every deposit address becomes permanently inaccessible.",
	"Do not screenshot or send them through chat tools. Write them down offline and store them separately.",
}

type cryptoSignerService interface {
	SignerShardStatus(context.Context) (*wallet.ShardStatus, error)
	SetupSignerShards(context.Context, cryptopay.SignerSetupRequest) (*wallet.ShardStatus, error)
	ResplitSignerShards(context.Context, cryptopay.SignerSetupRequest) (*wallet.ShardStatus, error)
	VerifySignerShare(context.Context, string) error
	UnlockSignerWindow(context.Context, string) (*wallet.ShardStatus, error)
	EnableAutoSweep(context.Context, string) (*wallet.ShardStatus, error)
	DisableAutoSweep(context.Context) (*wallet.ShardStatus, error)
}

func (h *CryptoHandler) signerService() (cryptoSignerService, bool) {
	if h.signer != nil {
		return h.signer, true
	}
	service, ok := any(h.service).(cryptoSignerService)
	return service, ok && h.service != nil
}

// writeCryptoSignerError maps signer sentinels to stable, secret-free codes.
func writeCryptoSignerError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, wallet.ErrSignerShareRequired):
		response.ErrorWithDetails(c, http.StatusConflict, "wallet signer is locked; a recovery shard is required", "CRYPTO_SIGNER_SHARE_REQUIRED", nil)
	case errors.Is(err, wallet.ErrSignerShareInvalid):
		response.ErrorWithDetails(c, http.StatusBadRequest, "recovery shard cannot open this wallet", "CRYPTO_SIGNER_SHARE_INVALID", nil)
	case errors.Is(err, wallet.ErrSignerRateLimited):
		response.ErrorWithDetails(c, http.StatusTooManyRequests, "too many rejected recovery shards; try again later", "CRYPTO_SIGNER_RATE_LIMITED", nil)
	case errors.Is(err, wallet.ErrShardsNotConfigured):
		response.ErrorWithDetails(c, http.StatusConflict, "wallet shards are not configured", "CRYPTO_SIGNER_SHARDS_NOT_CONFIGURED", nil)
	case errors.Is(err, wallet.ErrShardsAlreadyConfigured):
		response.ErrorWithDetails(c, http.StatusConflict, "wallet shards already exist; use the re-split flow", "CRYPTO_SIGNER_SHARDS_EXIST", nil)
	case errors.Is(err, wallet.ErrShardsNotAllowed):
		response.ErrorWithDetails(c, http.StatusConflict, "wallet shard operation is not allowed in the current state", "CRYPTO_SIGNER_STATE_INVALID", nil)
	default:
		return false
	}
	return true
}

func (h *CryptoHandler) requireFreshSignerTOTP(c *gin.Context, code string) bool {
	if h.security == nil {
		response.ErrorFrom(c, service.ErrCryptoTotpUnavailable)
		return false
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return false
	}
	if strings.TrimSpace(code) == "" {
		response.ErrorWithDetails(c, http.StatusBadRequest, "a fresh authenticator code is required", "CRYPTO_SIGNER_TOTP_REQUIRED", nil)
		return false
	}
	if response.ErrorFrom(c, h.security.VerifyRisk(c.Request.Context(), subject.UserID, code)) {
		return false
	}
	return true
}

func requireSignerRiskPhrase(c *gin.Context, phrase string) bool {
	cleaned := strings.Join(strings.Fields(phrase), "")
	if cleaned == strings.Join(strings.Fields(SignerRiskPhraseZH), "") ||
		cleaned == strings.Join(strings.Fields(SignerRiskPhraseEN), "") {
		return true
	}
	response.ErrorWithDetails(c, http.StatusBadRequest, "risk acknowledgement phrase does not match", "CRYPTO_SIGNER_RISK_ACK_REQUIRED", nil)
	return false
}

// AdminSignerRiskNotice returns the blocking-dialog copy. Keeping it on the
// server makes the acknowledged phrase authoritative and i18n-consistent.
func (h *CryptoHandler) AdminSignerRiskNotice(c *gin.Context) {
	cryptoVaultHeaders(c)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"points":       gin.H{"zh": signerRiskPointsZH, "en": signerRiskPointsEN},
		"phrases":      gin.H{"zh": SignerRiskPhraseZH, "en": SignerRiskPhraseEN},
		"display_once": true,
		"schemes":      []gin.H{{"id": "shamir-gf256", "threshold": 2, "total": 3, "share_prefix": "S2V1-"}},
	}})
}

// AdminSignerStatus reports the shard configuration and lock state.
func (h *CryptoHandler) AdminSignerStatus(c *gin.Context) {
	cryptoVaultHeaders(c)
	signer, ok := h.signerService()
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "wallet signer unavailable")
		return
	}
	status, err := signer.SignerShardStatus(cryptoAuditContext(c))
	if writeCryptoSignerError(c, err) {
		return
	}
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": status})
}

type signerSetupRequest struct {
	TotpCode     string `json:"totp_code"`
	RiskPhrase   string `json:"risk_phrase"`
	DataKey      string `json:"data_key"`
	ShareA       string `json:"share_a"`
	ConfirmShare string `json:"confirm_share"`
	DKCommitment string `json:"dk_commitment"`
}

func decodeSignerSetup(c *gin.Context, request *signerSetupRequest) bool {
	return decodeCryptoRequest(c, request, 8<<10, "invalid signer setup request")
}

func (request signerSetupRequest) serviceRequest() (cryptopay.SignerSetupRequest, bool) {
	dataKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.DataKey))
	if err != nil || len(dataKey) != 32 {
		clear(dataKey)
		return cryptopay.SignerSetupRequest{}, false
	}
	shareA := strings.TrimSpace(request.ShareA)
	confirm := strings.TrimSpace(request.ConfirmShare)
	commitment := strings.TrimSpace(request.DKCommitment)
	if shareA == "" || confirm == "" || commitment == "" {
		clear(dataKey)
		return cryptopay.SignerSetupRequest{}, false
	}
	return cryptopay.SignerSetupRequest{
		DataKey: dataKey, ShareA: shareA, ConfirmShare: confirm,
		Commitment: commitment, RiskAcknowledged: true,
	}, true
}

// AdminSignerSetup migrates the vault to the browser-generated data key and
// stores share A. The browser must re-send one new share as proof that it was
// saved before the vault is enabled.
func (h *CryptoHandler) AdminSignerSetup(c *gin.Context) {
	cryptoVaultHeaders(c)
	signer, ok := h.signerService()
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "wallet signer unavailable")
		return
	}
	var request signerSetupRequest
	if !decodeSignerSetup(c, &request) {
		return
	}
	defer func() { request.DataKey, request.ShareA, request.ConfirmShare = "", "", "" }()
	if !h.requireFreshSignerTOTP(c, request.TotpCode) {
		return
	}
	if !requireSignerRiskPhrase(c, request.RiskPhrase) {
		return
	}
	serviceRequest, valid := request.serviceRequest()
	if !valid {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid signer setup material", "CRYPTO_SIGNER_SHARE_INVALID", nil)
		return
	}
	defer clear(serviceRequest.DataKey)
	status, err := signer.SetupSignerShards(cryptoAuditContext(c), serviceRequest)
	if writeCryptoSignerError(c, err) {
		return
	}
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": status})
}

// AdminSignerVerifyShare proves one recovery share belongs to this wallet.
func (h *CryptoHandler) AdminSignerVerifyShare(c *gin.Context) {
	cryptoVaultHeaders(c)
	signer, ok := h.signerService()
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "wallet signer unavailable")
		return
	}
	var request struct {
		Share string `json:"share"`
	}
	if !decodeCryptoRequest(c, &request, 8<<10, "invalid share verification request") {
		return
	}
	defer func() { request.Share = "" }()
	if err := signer.VerifySignerShare(cryptoAuditContext(c), request.Share); writeCryptoSignerError(c, err) {
		return
	} else if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"valid": true}})
}

// AdminSignerUnlock verifies a recovery share and keeps the data key resident
// for the bounded 15-minute lease, so the operator can unlock once and then run
// the sweep from the normal run button. It requires a freshly typed code (the
// 5-minute grant is not enough) because it turns signing power on.
func (h *CryptoHandler) AdminSignerUnlock(c *gin.Context) {
	cryptoVaultHeaders(c)
	signer, ok := h.signerService()
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "wallet signer unavailable")
		return
	}
	var request struct {
		TotpCode string `json:"totp_code"`
		Share    string `json:"share"`
	}
	if !decodeCryptoRequest(c, &request, 8<<10, "invalid signer unlock request") {
		return
	}
	defer func() { request.Share = "" }()
	if !h.requireFreshSignerTOTP(c, request.TotpCode) {
		return
	}
	status, err := signer.UnlockSignerWindow(cryptoAuditContext(c), strings.TrimSpace(request.Share))
	if writeCryptoSignerError(c, err) {
		return
	}
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": status})
}

// AdminSignerResplit replaces share A after a browser-side re-split. The old
// automatic-sweep share is invalidated.
func (h *CryptoHandler) AdminSignerResplit(c *gin.Context) {
	cryptoVaultHeaders(c)
	signer, ok := h.signerService()
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "wallet signer unavailable")
		return
	}
	var request signerSetupRequest
	if !decodeSignerSetup(c, &request) {
		return
	}
	defer func() { request.DataKey, request.ShareA, request.ConfirmShare = "", "", "" }()
	if !h.requireFreshSignerTOTP(c, request.TotpCode) {
		return
	}
	if !requireSignerRiskPhrase(c, request.RiskPhrase) {
		return
	}
	serviceRequest, valid := request.serviceRequest()
	if !valid {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid re-split material", "CRYPTO_SIGNER_SHARE_INVALID", nil)
		return
	}
	defer clear(serviceRequest.DataKey)
	status, err := signer.ResplitSignerShards(cryptoAuditContext(c), serviceRequest)
	if writeCryptoSignerError(c, err) {
		return
	}
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": status})
}

// AdminSignerAutoSweep enables/disables the server-held automatic-sweep share.
// Enabling requires a fresh authenticator code, the risk phrase and an explicit
// acknowledge flag - the server holds A and B, so the vault can then be opened
// without a human present.
func (h *CryptoHandler) AdminSignerAutoSweep(c *gin.Context) {
	cryptoVaultHeaders(c)
	signer, ok := h.signerService()
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "wallet signer unavailable")
		return
	}
	var request struct {
		Enabled     bool   `json:"enabled"`
		TotpCode    string `json:"totp_code"`
		RiskPhrase  string `json:"risk_phrase"`
		Share       string `json:"share"`
		Acknowledge bool   `json:"acknowledge_risk"`
	}
	if !decodeCryptoRequest(c, &request, 8<<10, "invalid automatic sweep request") {
		return
	}
	defer func() { request.Share = "" }()
	if !h.requireFreshSignerTOTP(c, request.TotpCode) {
		return
	}
	middleware2.SetAuditExtra(c, map[string]any{"auto_sweep_enabled": request.Enabled})
	if !request.Enabled {
		status, err := signer.DisableAutoSweep(cryptoAuditContext(c))
		if writeCryptoSignerError(c, err) {
			return
		}
		if response.ErrorFrom(c, err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": status})
		return
	}
	if !request.Acknowledge {
		response.ErrorWithDetails(c, http.StatusBadRequest, "automatic sweeps require explicit risk acknowledgement", "CRYPTO_SIGNER_RISK_ACK_REQUIRED", nil)
		return
	}
	if !requireSignerRiskPhrase(c, request.RiskPhrase) {
		return
	}
	status, err := signer.EnableAutoSweep(cryptoAuditContext(c), request.Share)
	if writeCryptoSignerError(c, err) {
		return
	}
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": status})
}
