// Package scanner watches configured chains for incoming deposits.
package scanner

import (
	"context"
	"encoding/json"
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
// Recipients are the addresses being watched (lowercased); tokens are the
// contract addresses to filter on.
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
	for start := 0; start < len(recipients); start += batchSize {
		end := start + batchSize
		if end > len(recipients) {
			end = len(recipients)
		}
		// topic0 = Transfer, topic2 = to (any of the watched addresses).
		toTopic := make([]any, 0, end-start)
		for _, addr := range recipients[start:end] {
			toTopic = append(toTopic, padTopic(addr))
		}
		params := getLogsParams{
			FromBlock: fmt.Sprintf("0x%x", fromBlock),
			ToBlock:   fmt.Sprintf("0x%x", toBlock),
			Address:   tokens,
			Topics:    [][]any{{transferTopic}, nil, toTopic},
		}
		if outgoing {
			params.Topics = [][]any{{transferTopic}, toTopic}
		}
		raw, err := s.pool.Call(ctx, "eth_getLogs", []any{params})
		if err != nil {
			return nil, fmt.Errorf("getLogs %s %d-%d: %w", chain, fromBlock, toBlock, err)
		}
		var logs []evmLog
		if err := json.Unmarshal(raw, &logs); err != nil {
			return nil, fmt.Errorf("getLogs decode: %w", err)
		}
		if logs == nil {
			return nil, &rpc.Failure{Kind: "response"}
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
	return out, nil
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
