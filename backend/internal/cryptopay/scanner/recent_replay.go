package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

const recentReplayPrefix = "recent-replay:"
const recentReplayBudget = 5 * time.Second
const recentReplayBatchBlocks = uint64(50)
const recentReplayTransfersPerPass = 64

// Both the fixed upper boundary and the intra-block checkpoint survive worker
// replacement. Reuse the existing namespaced cursor row; no schema or balance
// changes are needed. Next is the first unfinished block, After its last
// persisted log (-1 means no log completed in that block).
type recentReplayState struct {
	Through uint64 `json:"through"`
	Next    uint64 `json:"next"`
	After   int    `json:"after"`
	Hash    string `json:"hash,omitempty"`
}

func (w *Worker) prepareRecentReplay(ctx context.Context, chain string, through, maxRange uint64, recipients []string) (*recentReplayState, error) {
	if through == 0 || len(recipients) == 0 {
		return nil, nil
	}
	w.replayMu.Lock()
	throttled := time.Since(w.replayedAt[chain]) < 20*time.Second
	w.replayMu.Unlock()
	if throttled {
		return nil, nil
	}
	var encoded string
	var updated time.Time
	err := w.db.QueryRowContext(ctx,
		`SELECT last_signature, updated_at FROM crypto_scan_cursors WHERE chain = $1`, recentReplayPrefix+chain).Scan(&encoded, &updated)
	state := &recentReplayState{}
	if err == nil {
		if json.Unmarshal([]byte(encoded), state) != nil || state.Next == 0 || state.Through == 0 ||
			state.Through >= math.MaxInt64 || state.Next > state.Through+1 || state.After < -1 ||
			(state.After >= 0 && !validHash(state.Hash)) {
			return nil, errors.New("invalid durable recent replay checkpoint")
		}
		if state.Next <= state.Through {
			return state, nil // Never replace an unfinished window with a later one.
		}
		if time.Since(updated) < 20*time.Second {
			return nil, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load recent replay checkpoint: %w", err)
	}
	span := min(maxRange, uint64(2000))
	if span == 0 {
		span = 2000
	}
	// If forward scanning got far ahead while replay recovered, walk the
	// intervening ranges instead of jumping a late deposit out of coverage.
	previous := state.Through
	if previous > 0 && previous < through {
		through = min(through, previous+span)
	}
	if through >= math.MaxInt64 {
		return nil, errors.New("recent replay block is out of range")
	}
	start := through - min(through, span) + 1
	if previous > 0 && previous < through {
		start = previous - min(previous, span) + 1
	}
	state = &recentReplayState{Through: through, Next: start, After: -1}
	if err := w.saveRecentReplay(ctx, chain, state); err != nil {
		return nil, err
	}
	return state, nil
}

func (w *Worker) saveRecentReplay(ctx context.Context, chain string, state *recentReplayState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = w.db.ExecContext(ctx,
		`INSERT INTO crypto_scan_cursors (chain, last_block, last_signature, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (chain) DO UPDATE SET last_block = EXCLUDED.last_block,
		 last_signature = EXCLUDED.last_signature, updated_at = NOW()`,
		recentReplayPrefix+chain, int64(state.Next-1), string(encoded))
	if err != nil {
		return fmt.Errorf("save recent replay checkpoint: %w", err)
	}
	return nil
}

// replayRecentRange always resumes the saved window, independently of the
// forward cursor and its deadline. Checkpoint each persisted transfer, including
// within a dense single block, so every bounded pass makes cumulative progress.
func (w *Worker) replayRecentRange(ctx context.Context, scanner *EVMScanner, pool *rpc.Pool, chain ChainConfig, state *recentReplayState, maxRange uint64, recipients []string) error {
	ctx, cancel := context.WithTimeout(ctx, recentReplayBudget)
	defer cancel()
	if state.After >= 0 {
		err := verifyCanonicalBlock(ctx, pool, &ReceiptCheck{BlockNumber: state.Next, BlockHash: state.Hash})
		if errors.Is(err, ErrNonCanonicalBlock) {
			state.After, state.Hash = -1, "" // A replacement block must replay all logs.
			if err := w.saveRecentReplay(ctx, chain.Chain, state); err != nil {
				return err
			}
		} else if err != nil {
			return fmt.Errorf("recent replay resume block: %w", err)
		}
	}
	completed := 0
	for batch := 0; batch < maxScanBatchesPerChain && state.Next <= state.Through; batch++ {
		span := min(maxRange, recentReplayBatchBlocks)
		if span == 0 {
			span = recentReplayBatchBlocks
		}
		to := state.Next + min(span-1, state.Through-state.Next)
		if w.securityRange != nil {
			if err := w.securityRange(ctx, scanner, pool, chain, state.Next, to); err != nil {
				return err
			}
		}
		transfers, err := scanner.ScanRange(ctx, chain.Chain, state.Next, to, chain.Tokens, recipients)
		if err != nil {
			return fmt.Errorf("recent replay logs: %w", err)
		}
		sort.Slice(transfers, func(i, j int) bool {
			if transfers[i].BlockNumber != transfers[j].BlockNumber {
				return transfers[i].BlockNumber < transfers[j].BlockNumber
			}
			return transfers[i].LogIndex < transfers[j].LogIndex
		})
		for _, transfer := range transfers {
			if transfer.BlockNumber < state.Next || transfer.BlockNumber > to {
				continue
			}
			if transfer.BlockNumber == state.Next && transfer.LogIndex <= state.After && strings.EqualFold(transfer.BlockHash, state.Hash) {
				continue
			}
			decimals, watched := watchedDecimals(chain, transfer.Token)
			if !watched {
				continue
			}
			if transfer.LogIndex < 0 || !validHash(transfer.BlockHash) {
				return &rpc.Failure{Kind: "response"}
			}
			if err := w.processTransfer(ctx, pool, chain, transfer, decimals); err != nil {
				return fmt.Errorf("recent replay transfer: %w", err)
			}
			state.Next, state.After, state.Hash = transfer.BlockNumber, transfer.LogIndex, transfer.BlockHash
			if err := w.saveRecentReplay(ctx, chain.Chain, state); err != nil {
				return err
			}
			completed++
			if completed >= recentReplayTransfersPerPass {
				return nil // A durable yield, not a completed window or an error.
			}
		}
		state.Next, state.After, state.Hash = to+1, -1, ""
		if err := w.saveRecentReplay(ctx, chain.Chain, state); err != nil {
			return err
		}
	}
	if state.Next > state.Through {
		w.replayMu.Lock()
		if w.replayedAt == nil {
			w.replayedAt = make(map[string]time.Time)
		}
		w.replayedAt[chain.Chain] = time.Now()
		w.replayMu.Unlock()
	}
	return nil
}
