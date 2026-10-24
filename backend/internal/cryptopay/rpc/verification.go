package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"golang.org/x/net/publicsuffix"
)

// Reference nodes are a separate trust boundary from a remotely fetched node
// registry. Two independent reference operators must corroborate funds evidence.
// A registry entry cannot nominate itself as a reference or reduce the quorum.
type verificationPolicy struct {
	chainID    uint64
	anchors    []string
	chainMu    sync.Mutex
	verifiedAt map[string]time.Time
}

// SetIndependentVerification applies a reference policy to automatic pools.
// An empty or undersized reference set is fail-closed, never single-node mode.
// An explicit operator-managed pool can remove the policy via ClearVerification.
func (p *Pool) SetIndependentVerification(chainID uint64, anchors []string) {
	policy := newVerificationPolicy(chainID, anchors)
	p.mu.Lock()
	p.verification = policy
	p.mu.Unlock()
}

func newVerificationPolicy(chainID uint64, anchors []string) *verificationPolicy {
	seen := make(map[string]bool)
	policy := &verificationPolicy{chainID: chainID, verifiedAt: make(map[string]time.Time)}
	for _, endpoint := range anchors {
		group := providerGroup(endpoint)
		if group != "" && !seen[group] {
			seen[group] = true
			policy.anchors = append(policy.anchors, endpoint)
		}
	}
	return policy
}

func (p *Pool) verifyReferenceChain(ctx context.Context, policy *verificationPolicy, endpoint string) error {
	policy.chainMu.Lock()
	checked := policy.verifiedAt[endpoint]
	policy.chainMu.Unlock()
	if time.Since(checked) < 5*time.Minute {
		return nil
	}
	id, err := p.callOne(ctx, endpoint, "eth_chainId", nil)
	if err != nil {
		return err
	}
	number, err := evidenceQuantity(id)
	if err != nil || number != policy.chainID {
		return ErrIndependentEvidence
	}
	policy.chainMu.Lock()
	policy.verifiedAt[endpoint] = time.Now()
	policy.chainMu.Unlock()
	return nil
}

// ConfigureEndpoints changes discovery provenance and its endpoint snapshot
// atomically. A reload can never expose new automatic nodes with an old
// manually trusted (nil) policy, even to concurrent WithEndpoints readers.
func (p *Pool) ConfigureEndpoints(endpoints string, automatic bool, chainID uint64, anchors []string) {
	var policy *verificationPolicy
	if automatic {
		policy = newVerificationPolicy(chainID, anchors)
	}
	var list []string
	for _, endpoint := range strings.Split(endpoints, ",") {
		if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
			list = append(list, endpoint)
		}
	}
	p.mu.Lock()
	p.endpoints, p.verification = list, policy
	p.mu.Unlock()
}

// ClearVerification retains the existing explicit trust placed in manually
// configured endpoints. Automatic discovery always sets a policy again.
func (p *Pool) ClearVerification() {
	p.mu.Lock()
	p.verification = nil
	p.mu.Unlock()
}

// RequiresIndependentVerification reports a pool's immutable snapshot policy.
// Callers must not replace an unavailable independent check with depth alone.
func (p *Pool) RequiresIndependentVerification() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.verification != nil
}

func providerGroup(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	group, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return group
}

func needsIndependentEvidence(method string) bool {
	switch method {
	case "eth_getLogs", "eth_getTransactionReceipt", "eth_getBlockByNumber", "eth_blockNumber":
		return true
	default:
		return false
	}
}

var ErrIndependentEvidence = errors.New("independent RPC confirmation is unavailable or disagrees")

type referenceEvidence struct {
	raw json.RawMessage
	err error
}

func (p *Pool) verifyIndependentEvidence(ctx context.Context, policy *verificationPolicy, source, method string, params any, observed json.RawMessage) (json.RawMessage, error) {
	if policy.chainID == 0 || len(policy.anchors) < 2 {
		return nil, ErrIndependentEvidence
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	results := make(chan referenceEvidence, len(policy.anchors))
	for _, anchor := range policy.anchors {
		go func(endpoint string) {
			var result referenceEvidence
			// Read exactly this reference, without recursion or registry rotation.
			if err := p.verifyReferenceChain(ctx, policy, endpoint); err != nil {
				result.err = err
			} else if endpoint == source {
				result.raw = observed
			} else {
				result.raw, result.err = p.callOne(ctx, endpoint, method, params)
			}
			results <- result
		}(anchor)
	}
	dynamic := dynamicEvidence(method, params)
	var heights []uint64
	var blocks []json.RawMessage
	matches := 0
	want, err := evidenceIdentity(method, observed)
	if err != nil && !dynamic {
		return nil, ErrIndependentEvidence
	}
	for range policy.anchors {
		select {
		case <-ctx.Done():
			if parent.Err() != nil {
				return nil, ErrIndependentEvidence
			}
			goto collected
		case result := <-results:
			if result.err != nil {
				continue
			}
			if dynamic {
				height, err := evidenceHeight(method, result.raw)
				if err == nil {
					heights, blocks = append(heights, height), append(blocks, result.raw)
				}
				if len(heights) >= 2 {
					// Any two independent witnesses suffice for a conservative
					// height. Waiting for a third outage would consume the
					// entire per-deposit budget despite an available quorum.
					goto collected
				}
				continue
			}
			got, err := evidenceIdentity(method, result.raw)
			if err == nil && got == want {
				matches++
				if matches >= 2 {
					return observed, nil
				}
			}
		}
	}
collected:
	if dynamic && len(heights) >= 2 {
		// Nodes are sampled at different instants. Never require identical
		// latest/safe/finalized heights or trust a registry node's future head.
		// The lower of the completed reference heights has two witnesses.
		indices := make([]int, len(heights))
		for i := range indices {
			indices[i] = i
		}
		sort.Slice(indices, func(i, j int) bool { return heights[indices[i]] > heights[indices[j]] })
		return blocks[indices[1]], nil
	}
	return nil, ErrIndependentEvidence
}

func dynamicEvidence(method string, params any) bool {
	if method == "eth_blockNumber" {
		return true
	}
	if method != "eth_getBlockByNumber" {
		return false
	}
	encoded, err := json.Marshal(params)
	var args []json.RawMessage
	var tag string
	if err != nil || json.Unmarshal(encoded, &args) != nil || len(args) == 0 || json.Unmarshal(args[0], &tag) != nil {
		return false
	}
	return tag == "latest" || tag == "safe" || tag == "finalized"
}

func evidenceQuantity(raw json.RawMessage) (uint64, error) {
	var quantity string
	if json.Unmarshal(raw, &quantity) != nil {
		return 0, ErrIndependentEvidence
	}
	return hexutil.DecodeUint64(quantity)
}

func evidenceHeight(method string, raw json.RawMessage) (uint64, error) {
	if method == "eth_blockNumber" {
		return evidenceQuantity(raw)
	}
	var block struct {
		Number json.RawMessage `json:"number"`
	}
	if json.Unmarshal(raw, &block) != nil {
		return 0, ErrIndependentEvidence
	}
	return evidenceQuantity(block.Number)
}

type evidenceLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	BlockHash   string   `json:"blockHash"`
	TxHash      string   `json:"transactionHash"`
	Index       string   `json:"logIndex"`
	Removed     bool     `json:"removed"`
}

func normalizedLogs(raw json.RawMessage) ([]string, error) {
	var logs []evidenceLog
	if !validLogsResult(raw) || json.Unmarshal(raw, &logs) != nil {
		return nil, ErrIndependentEvidence
	}
	out := make([]string, 0, len(logs))
	for _, entry := range logs {
		if !validEvidenceHex(entry.Address, 20) || !validEvidenceHex(entry.BlockHash, 32) || !validEvidenceHex(entry.TxHash, 32) || len(entry.Topics) > 4 || !strings.HasPrefix(entry.Data, "0x") {
			return nil, ErrIndependentEvidence
		}
		if _, err := hexutil.Decode(entry.Data); err != nil {
			return nil, ErrIndependentEvidence
		}
		if _, err := hexutil.DecodeUint64(entry.BlockNumber); err != nil {
			return nil, ErrIndependentEvidence
		}
		if _, err := hexutil.DecodeUint64(entry.Index); err != nil {
			return nil, ErrIndependentEvidence
		}
		for _, topic := range entry.Topics {
			if !validEvidenceHex(topic, 32) {
				return nil, ErrIndependentEvidence
			}
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return nil, ErrIndependentEvidence
		}
		out = append(out, strings.ToLower(string(encoded)))
	}
	sort.Strings(out)
	return out, nil
}

func validEvidenceHex(value string, size int) bool {
	decoded, err := hexutil.Decode(value)
	return err == nil && len(decoded) == size
}

func evidenceIdentity(method string, raw json.RawMessage) (string, error) {
	var canonical any
	switch method {
	case "eth_getLogs":
		logs, err := normalizedLogs(raw)
		if err != nil {
			return "", err
		}
		canonical = logs
	case "eth_getTransactionReceipt":
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return "null", nil
		}
		var receipt struct {
			Status string          `json:"status"`
			Number string          `json:"blockNumber"`
			Hash   string          `json:"blockHash"`
			Tx     string          `json:"transactionHash"`
			Logs   json.RawMessage `json:"logs"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validEvidenceHex(receipt.Hash, 32) || !validEvidenceHex(receipt.Tx, 32) {
			return "", ErrIndependentEvidence
		}
		status, err := hexutil.DecodeUint64(receipt.Status)
		if err != nil || status > 1 {
			return "", ErrIndependentEvidence
		}
		number, err := hexutil.DecodeUint64(receipt.Number)
		if err != nil {
			return "", ErrIndependentEvidence
		}
		logs, err := normalizedLogs(receipt.Logs)
		if err != nil {
			return "", err
		}
		canonical = []any{status, number, strings.ToLower(receipt.Hash), strings.ToLower(receipt.Tx), logs}
	case "eth_getBlockByNumber":
		var block struct {
			Number string `json:"number"`
			Hash   string `json:"hash"`
		}
		if json.Unmarshal(raw, &block) != nil || !validEvidenceHex(block.Hash, 32) {
			return "", ErrIndependentEvidence
		}
		number, err := hexutil.DecodeUint64(block.Number)
		if err != nil {
			return "", ErrIndependentEvidence
		}
		canonical = []any{number, strings.ToLower(block.Hash)}
	default:
		return "", ErrIndependentEvidence
	}
	encoded, err := json.Marshal(canonical)
	return string(encoded), err
}
