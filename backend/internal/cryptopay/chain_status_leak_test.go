package cryptopay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Every kind of failure and every place a secret can sit in a URL (path, query,
// userinfo, fragment, port), against a healthy database and a failing one that
// puts a connection string in its error. Neither the response nor the logs may
// contain any of it.
func TestChainStatusNeverExposesNodeURLsKeysOrDriverErrors(t *testing.T) {
	logs := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const key = "SECRETAPIKEY123"
	bscOK := "https://rpc.ankr.com/bsc/" + key
	bsc5xx := "https://bsc-mainnet.blockpi.network/v1/rpc/" + key + "?token=" + key
	bscRPCErr := "https://user:" + key + "@bsc.drpc.org:8443/path/" + key + "#" + key
	bscGarbage := "https://bsc.nodereal.io/v1/" + key
	ethWrong := "https://eth.llamarpc.com/" + key
	polyOff := "https://polygon-rpc.com/" + key

	cfg := DefaultCryptoConfig()
	cfg.Chains = []string{ChainBSC, ChainEthereum}
	cfg.RPC = map[string]string{
		ChainBSC: strings.Join([]string{bscOK, bsc5xx, bscRPCErr, bscGarbage}, ","), ChainEthereum: ethWrong, ChainPolygon: polyOff,
	}
	pools := map[string]*rpc.Pool{
		ChainBSC: poolOf(bscOK, bsc5xx, bscRPCErr, bscGarbage), ChainEthereum: poolOf(ethWrong), ChainPolygon: poolOf(polyOff),
	}
	nodes := map[string]*fakeNode{
		bscOK:      newFakeNode(t, fakeNodeOpts{chainID: 56, head: 1000}),
		bsc5xx:     newFakeNode(t, fakeNodeOpts{status: 500}),
		bscRPCErr:  newFakeNode(t, fakeNodeOpts{rpcError: true}),
		bscGarbage: newFakeNode(t, fakeNodeOpts{body: "<html>SECRETBODY</html>"}),
		ethWrong:   newFakeNode(t, fakeNodeOpts{chainID: 56, head: 5}),
		polyOff:    newFakeNode(t, fakeNodeOpts{chainID: 137, head: 5000}),
	}
	run := func(cursorErr error) string {
		h := newStatusHarness(t, cfg, pools, nodes)
		if cursorErr != nil {
			h.expectCursorsError(cursorErr)
		} else {
			now := h.clock.Now()
			h.expectCursors(cursorRow{ChainBSC, 990, now.Add(-5 * time.Second)},
				cursorRow{ChainEthereum, 50, now.Add(-time.Hour)}, cursorRow{ChainPolygon, 10, now.Add(-time.Hour)})
		}
		h.expectPending(map[string]int64{ChainBSC: 1})
		rep, err := h.svc.ChainStatus(context.Background())
		require.NoError(t, err)
		raw, err := json.Marshal(map[string]any{"data": rep})
		require.NoError(t, err)
		return string(raw)
	}
	healthy := run(nil)
	failing := run(errors.New("pq: password authentication failed host=db.internal.example port=5432 password=DBPASSWORD"))

	all := healthy + failing + logs.String()
	for _, secret := range []string{
		key, "SECRETBODY", "SECRETRPC", "DBPASSWORD", "db.internal.example", "port=5432",
		"https://", "http://", ":8443", "/v1/", "/bsc/", "token=", "user:", "@", "127.0.0.1", "#" + key,
	} {
		require.NotContains(t, all, secret)
	}
	// And the page is still useful: hosts, labels and fixed error words are there.
	for _, want := range []string{
		`"label":"ankr#0"`, `"host":"rpc.ankr.com"`, `"label":"blockpi#1"`, `"label":"drpc#2"`,
		`"host":"bsc.drpc.org"`, `"label":"nodereal#3"`, `"label":"llamarpc#0"`, `"label":"polygon-rpc#0"`,
		`"error_kind":"http_5xx"`, `"error_kind":"rpc_error"`, `"error_kind":"bad_response"`, `"error_kind":"wrong_chain"`,
	} {
		require.Contains(t, healthy, want)
	}
	require.Contains(t, failing, `"kind":"db_error"`)
}

// The page must never write, reconfigure, trigger a scan or move a cursor.
func TestChainStatusSourceStaysReadOnly(t *testing.T) {
	files, err := filepath.Glob("chain_status_*.go")
	require.NoError(t, err)
	forbidden := []string{
		"ExecContext", ".Exec(", "BeginTx", ".Begin(", "INSERT", "UPDATE ", "DELETE ", "TRUNCATE",
		"SaveConfig", "SetCryptoSetting", ".Reload(", "RunScan", "RunOnce", ".Save(", "SetEndpoints", "ConfigureEndpoints",
	}
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		scanned++
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, token := range forbidden {
				require.NotContains(t, line, token, "%s:%d must stay read-only", f, i+1)
			}
		}
	}
	require.GreaterOrEqual(t, scanned, 8, "the guard must actually see the implementation files")
}
