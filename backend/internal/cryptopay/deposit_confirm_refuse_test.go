package cryptopay

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type refusalCase struct {
	name            string
	row             func(s *depositSpec)               // what the database stored
	chain           func(e *confirmEnv, s depositSpec) // what the chain says instead
	status, message string
	// untouchedChain: refused from the row alone, before any RPC call.
	// noReceipt: refused before a receipt was requested.
	untouchedChain, noReceipt bool
}

func TestManualConfirmRefusesWhatTheChainDoesNotConfirm(t *testing.T) {
	otherHash := "0x" + strings.Repeat("99", 32)
	cases := []refusalCase{
		{name: "reverted transaction", status: DepositConfirmRejected, message: "已回滚",
			chain: func(e *confirmEnv, s depositSpec) { e.chain.receipts[s.tx].status = "0x0" }},
		{name: "receipt not indexed yet", status: DepositConfirmError, message: "没有返回这笔交易的收据",
			chain: func(e *confirmEnv, s depositSpec) { e.chain.receipts[s.tx].absent = true }},
		{name: "block hash differs from the canonical block", status: DepositConfirmRejected, message: "不在规范链上",
			chain: func(e *confirmEnv, s depositSpec) { e.chain.canonical[s.block] = otherHash }},
		{name: "node cannot return the canonical block", status: DepositConfirmError, message: "链上复核失败",
			chain: func(e *confirmEnv, s depositSpec) { delete(e.chain.canonical, s.block) }},
		{name: "stored amount differs", status: DepositConfirmRejected, message: "唯一 Transfer",
			row: func(s *depositSpec) { s.amount = "2.000000000000000000" }},
		{name: "stored recipient differs", status: DepositConfirmRejected, message: "唯一 Transfer",
			row: func(s *depositSpec) { s.to = confirmStranger }},
		{name: "stored sender differs", status: DepositConfirmRejected, message: "唯一 Transfer",
			row: func(s *depositSpec) { s.from = confirmStranger }},
		{name: "stored token differs", status: DepositConfirmRejected, message: "唯一 Transfer",
			row: func(s *depositSpec) { s.token = confirmUSDC }},
		{name: "stored block number differs", status: DepositConfirmRejected, message: "换了区块",
			row: func(s *depositSpec) { s.block++ }},
		{name: "stored block hash differs", status: DepositConfirmRejected, message: "换了区块",
			row: func(s *depositSpec) { s.blockHash = otherHash }},
		{name: "stored log index differs", status: DepositConfirmRejected, message: "换了区块",
			row: func(s *depositSpec) { s.index = 8 }},
		{name: "two identical logs make a relocation ambiguous", status: DepositConfirmRejected, message: "唯一 Transfer",
			row: func(s *depositSpec) { s.index = 9 },
			chain: func(e *confirmEnv, s depositSpec) {
				r := e.chain.receipts[s.tx]
				r.logs = append(r.logs, chainTransferLog(s.token, s.from, s.to, tenUSDT, s.block, s.blockHash, s.tx, 8))
			}},
		{name: "receipt request fails", status: DepositConfirmError, message: "status 503",
			chain: func(e *confirmEnv, s depositSpec) { e.chain.httpFail["eth_getTransactionReceipt"] = 503 }},
		{name: "node is on another chain", status: DepositConfirmError, message: "没有通过链 ID 校验的 RPC 节点", noReceipt: true,
			chain: func(e *confirmEnv, s depositSpec) { e.chain.chainID = "0x1" }},
		{name: "amount has more precision than the token", status: DepositConfirmRejected, message: "精度异常", noReceipt: true,
			row: func(s *depositSpec) { s.amount = "10.0000000000000000001" }},
		{name: "zero amount", status: DepositConfirmRejected, message: "精度异常", noReceipt: true,
			row: func(s *depositSpec) { s.amount = "0" }},
		{name: "unparsable amount", status: DepositConfirmRejected, message: "精度异常", noReceipt: true,
			row: func(s *depositSpec) { s.amount = "abc" }},
		{name: "token is not in the trusted list", status: DepositConfirmRejected, message: "不在该链的受信代币清单", untouchedChain: true,
			row: func(s *depositSpec) { s.token = "0x" + strings.Repeat("ab", 20) }},
		{name: "chain is not EVM", status: DepositConfirmRejected, message: "只支持 EVM", untouchedChain: true,
			row: func(s *depositSpec) { s.chain = "solana" }},
		{name: "no stored block hash", status: DepositConfirmRejected, message: "缺少区块号或区块哈希", untouchedChain: true,
			row: func(s *depositSpec) { s.blockHash = "" }},
		{name: "no stored block number", status: DepositConfirmRejected, message: "缺少区块号或区块哈希", untouchedChain: true,
			row: func(s *depositSpec) { s.block = 0 }},
		{name: "tx hash is not canonical lowercase", status: DepositConfirmRejected, message: "不是规范的小写", untouchedChain: true,
			row: func(s *depositSpec) { s.tx = "0x" + strings.ToUpper(s.tx[2:]) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newConfirmEnv(t)
			s := e.spec(1)
			e.publish(s)
			if tc.chain != nil {
				tc.chain(e, s)
			}
			if tc.row != nil {
				tc.row(&s)
			}
			id := e.insert(t, s)
			before := e.snapshot(t)

			result := e.confirm(id)
			require.Equal(t, tc.status, result.Status, result.Message)
			require.Contains(t, result.Message, tc.message)
			require.Nil(t, result.OrderID)
			require.Equal(t, before, e.snapshot(t), "a refused confirmation must leave the database untouched")
			require.Zero(t, e.processor.count(), "the ledger must not be reached")
			if tc.untouchedChain {
				require.Zero(t, e.chain.totalCalls())
			}
			if tc.noReceipt || tc.untouchedChain {
				require.Zero(t, e.chain.callCount("eth_getTransactionReceipt"))
			}
		})
	}
}

func TestManualConfirmNeverReachesChainOrLedgerForRowsThatAreNotPending(t *testing.T) {
	for _, tc := range []struct{ status, reason, message string }{
		{"ignored", "below_min", "低于最小入账金额"},
		{"ignored", "address_unassigned", "没有分配给用户"},
		{"reverted", "reorg_noncanonical_block", "链重组使记录失效"},
		{"reverted", "reorg_duplicate_credit_manual_review", "需要人工核对"},
	} {
		t.Run(tc.status+"/"+tc.reason, func(t *testing.T) {
			e := newConfirmEnv(t)
			s := e.spec(1)
			s.status, s.reason = tc.status, tc.reason
			e.publish(s)
			id := e.insert(t, s)
			before := e.snapshot(t)

			result := e.confirm(id)
			require.Equal(t, DepositConfirmRejected, result.Status, result.Message)
			require.Contains(t, result.Message, tc.status)
			require.Contains(t, result.Message, tc.message)
			require.Equal(t, before, e.snapshot(t))
			require.Zero(t, e.chain.totalCalls())
			require.Zero(t, e.processor.count())
		})
	}
}

func TestManualConfirmReportsAlreadyCreditedRowsWithTheirOrderAndMissingRowsAsRejected(t *testing.T) {
	e := newConfirmEnv(t)
	s := e.spec(1)
	s.status = "credited"
	id := e.insert(t, s)
	_, err := e.db.ExecContext(t.Context(), `UPDATE deposit_transactions SET order_id = 42 WHERE id = $1`, id)
	require.NoError(t, err)

	done := e.confirm(id)
	require.Equal(t, DepositConfirmAlreadyCredited, done.Status, done.Message)
	require.NotNil(t, done.OrderID)
	require.EqualValues(t, 42, *done.OrderID)

	missing := e.confirm(id + 1000)
	require.Equal(t, DepositConfirmRejected, missing.Status)
	require.Contains(t, missing.Message, "记录不存在")
	require.Zero(t, e.chain.totalCalls())
	require.Zero(t, e.processor.count())
}
