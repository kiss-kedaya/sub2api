// Package scanner watches configured chains for incoming deposits.
package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// Transfer is one observed token transfer.
type Transfer struct {
	Chain       string
	TxHash      string
	LogIndex    int
	From        string
	To          string
	Token       string // contract address (lowercase)
	Amount      *big.Int
	Decimals    int
	BlockNumber uint64
	BlockHash   string
}

// ERC20 Transfer(address,address,uint256) topic0.
const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// batchSize bounds how many addresses go into one eth_getLogs filter.
const batchSize = 200

// evmFirstRunLookback is how far back the first scan reaches so deposits
// landing just before boot are still caught. Bounded: full-history backfill
// is the reconciliation job's concern, not the live scanner's.
const evmFirstRunLookback = 200

// EVMScanner scans ERC20 transfers for a set of watched addresses.
type EVMScanner struct {
	pool *rpc.Pool
}

func NewEVMScanner(pool *rpc.Pool) *EVMScanner {
	return &EVMScanner{pool: pool}
}

type getLogsParams struct {
	FromBlock string   `json:"fromBlock"`
	ToBlock   string   `json:"toBlock"`
	Address   []string `json:"address"` // token contracts
	Topics    [][]any  `json:"topics"`
}

type evmLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	TxHash      string   `json:"transactionHash"`
	LogIndex    string   `json:"logIndex"`
	BlockHash   string   `json:"blockHash"`
	Removed     bool     `json:"removed"`
}

// ScanRange fetches transfers in [fromBlock, toBlock] for the given token
// contracts and watched recipients, batching recipients by the filter limit.
// On a partial failure it returns transfers from successful batches together
// with the error; cursor callers must not advance, while best-effort observers
// may persist the matches without treating the range as fully scanned.
func (s *EVMScanner) ScanRange(ctx context.Context, chain string, fromBlock, toBlock uint64, tokens []string, recipients []string) ([]Transfer, error) {
	return s.scanRange(ctx, chain, fromBlock, toBlock, tokens, recipients, false)
}

// ScanOutgoingRange watches the sender topic. A to-address-only scan misses
// unauthorized transfers out of deposit and gas wallets entirely.
func (s *EVMScanner) ScanOutgoingRange(ctx context.Context, chain string, fromBlock, toBlock uint64, tokens, senders []string) ([]Transfer, error) {
	return s.scanRange(ctx, chain, fromBlock, toBlock, tokens, senders, true)
}

func (s *EVMScanner) scanRange(ctx context.Context, chain string, fromBlock, toBlock uint64, tokens []string, recipients []string, outgoing bool) ([]Transfer, error) {
	if len(recipients) == 0 || len(tokens) == 0 || toBlock < fromBlock {
		return nil, nil
	}
	recipientSet := make(map[string]struct{}, len(recipients))
	for _, r := range recipients {
		recipientSet[strings.ToLower(r)] = struct{}{}
	}

	var out []Transfer
	var scanErrs []error
	// One budget per scanRange: a dense or slow range may be subdivided, but a
	// node failing every request can never turn one pass into a request storm.
	budget := maxGetLogsSplitRequests
	for start := 0; start < len(recipients); start += batchSize {
		end := start + batchSize
		if end > len(recipients) {
			end = len(recipients)
		}
		logs, err := s.getLogsAdaptive(ctx, chain, fromBlock, toBlock, tokens, recipients[start:end], outgoing, start/batchSize, &budget)
		if err != nil {
			scanErrs = append(scanErrs, err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		for _, log := range logs {
			if log.Removed || len(log.Topics) < 3 {
				continue
			}
			to := topicToAddress(log.Topics[2])
			from := topicToAddress(log.Topics[1])
			watchedAddress := to
			if outgoing {
				watchedAddress = from
			}
			if _, ok := recipientSet[strings.ToLower(watchedAddress)]; !ok {
				continue
			}
			amount, ok := new(big.Int).SetString(strings.TrimPrefix(log.Data, "0x"), 16)
			if !ok {
				continue
			}
			blockNumber := parseHexUint(log.BlockNumber)
			logIndex := int(parseHexUint(log.LogIndex))
			out = append(out, Transfer{
				Chain:       chain,
				TxHash:      strings.ToLower(log.TxHash),
				LogIndex:    logIndex,
				From:        from,
				To:          to,
				Token:       strings.ToLower(log.Address),
				Amount:      amount,
				BlockNumber: blockNumber,
				BlockHash:   log.BlockHash,
			})
		}
	}
	return out, errors.Join(scanErrs...)
}

// maxGetLogsSplitRequests caps the extra eth_getLogs requests one scanRange may
// spend subdividing failed queries. Public BSC nodes time out or answer "query
// exceeds max results" on dense ranges; halving recovers those, while the cap
// keeps a node that rejects everything from being hammered every pass.
const maxGetLogsSplitRequests = 64

// minSplitAddresses stops halving the address filter. A single-block query with
// this few topics that still fails is an endpoint problem, not query size.
const minSplitAddresses = 25

// LogsError locates the eth_getLogs query that still failed after adaptive
// splitting. Every field is numeric or a configured chain name and the wrapped
// cause comes from rpc.Pool, so it never adds an endpoint or node-supplied text.
type LogsError struct {
	Chain              string
	FromBlock, ToBlock uint64
	Batch              int // index of the address batch (batchSize each) in the scanned list
	Addresses          int // addresses in the failing, possibly halved, filter
	Err                error
}

func (e *LogsError) Error() string {
	return fmt.Sprintf("getLogs %s %d-%d batch %d (%d addresses): %v", e.Chain, e.FromBlock, e.ToBlock, e.Batch, e.Addresses, e.Err)
}

func (e *LogsError) Unwrap() error { return e.Err }

// getLogsAdaptive returns logs only when the complete [from,to] x addresses
// space was covered. Recoverable remote query rejections are retried as two
// halves: the block range, then the address filter. Rate limits are not split.
// Splits are disjoint, so no log repeats.
func (s *EVMScanner) getLogsAdaptive(ctx context.Context, chain string, from, to uint64, tokens, addresses []string, outgoing bool, batch int, budget *int) ([]evmLog, error) {
	logs, err := s.getLogs(ctx, from, to, tokens, addresses, outgoing)
	if err == nil {
		return logs, nil
	}
	var failure *rpc.Failure
	canSplitFailure := errors.As(err, &failure) &&
		(failure.Kind == "response" || (failure.Kind == "remote" && failure.RPCCode != 429))
	canSplit := ctx.Err() == nil && canSplitFailure && *budget >= 2 &&
		(to > from || len(addresses) > minSplitAddresses)
	if !canSplit {
		return nil, &LogsError{Chain: chain, FromBlock: from, ToBlock: to, Batch: batch, Addresses: len(addresses), Err: err}
	}
	*budget -= 2 // reserve both halves before either can spend more
	if to > from {
		mid := from + (to-from)/2
		left, leftErr := s.getLogsAdaptive(ctx, chain, from, mid, tokens, addresses, outgoing, batch, budget)
		if leftErr != nil {
			return nil, leftErr
		}
		right, rightErr := s.getLogsAdaptive(ctx, chain, mid+1, to, tokens, addresses, outgoing, batch, budget)
		if rightErr != nil {
			return nil, rightErr
		}
		return append(left, right...), nil
	}
	half := len(addresses) / 2
	left, leftErr := s.getLogsAdaptive(ctx, chain, from, to, tokens, addresses[:half], outgoing, batch, budget)
	if leftErr != nil {
		return nil, leftErr
	}
	right, rightErr := s.getLogsAdaptive(ctx, chain, from, to, tokens, addresses[half:], outgoing, batch, budget)
	if rightErr != nil {
		return nil, rightErr
	}
	return append(left, right...), nil
}

// getLogs issues exactly one eth_getLogs filter for the given addresses.
func (s *EVMScanner) getLogs(ctx context.Context, from, to uint64, tokens, addresses []string, outgoing bool) ([]evmLog, error) {
	// topic0 = Transfer, topic2 = to (any of the watched addresses); outflows
	// filter topic1 = from instead.
	topic := make([]any, 0, len(addresses))
	for _, addr := range addresses {
		topic = append(topic, padTopic(addr))
	}
	params := getLogsParams{
		FromBlock: fmt.Sprintf("0x%x", from),
		ToBlock:   fmt.Sprintf("0x%x", to),
		Address:   tokens,
		Topics:    [][]any{{transferTopic}, nil, topic},
	}
	if outgoing {
		params.Topics = [][]any{{transferTopic}, topic}
	}
	raw, err := s.pool.Call(ctx, "eth_getLogs", []any{params})
	if err != nil {
		return nil, err
	}
	var logs []evmLog
	if err := json.Unmarshal(raw, &logs); err != nil {
		return nil, fmt.Errorf("getLogs decode: %w", err)
	}
	if logs == nil {
		return nil, &rpc.Failure{Kind: "response"}
	}
	return logs, nil
}

// padTopic left-pads a 20-byte address to a 32-byte topic.
func padTopic(address string) string {
	trimmed := strings.TrimPrefix(strings.ToLower(address), "0x")
	return "0x" + strings.Repeat("0", 64-len(trimmed)) + trimmed
}

// topicToAddress extracts a 20-byte address from a 32-byte topic.
func topicToAddress(topic string) string {
	trimmed := strings.TrimPrefix(topic, "0x")
	if len(trimmed) < 40 {
		return "0x" + trimmed
	}
	return "0x" + trimmed[len(trimmed)-40:]
}

func parseHexUint(hex string) uint64 {
	hex = strings.TrimPrefix(hex, "0x")
	if hex == "" {
		return 0
	}
	var value uint64
	_, _ = fmt.Sscanf(hex, "%x", &value)
	return value
}
