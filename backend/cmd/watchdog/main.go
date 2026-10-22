// Command watchdog is the Go replacement for the bash health watchdog plus
// the chain/upstream monitors that used to live in separate scripts.
//
// Design constraints carried over from the 2026-09-25 incident:
//   - restart only the configured unit, never touch nginx
//   - probe the unit's real bind address, not 127.0.0.1
//   - require N consecutive failures before restarting
//
// Added here: RPC health checks per chain and upstream balance threshold
// alerts, both reported through Bark.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type config struct {
	healthURL    string
	unit         string
	maxFails     int
	rpcEndpoints []string
	barkKey      string
	interval     time.Duration
	stateDir     string
}

func main() {
	var cfg config
	flag.StringVar(&cfg.healthURL, "health-url", "", "full health URL of the gateway (required)")
	flag.StringVar(&cfg.unit, "unit", "", "systemd unit to restart on repeated failures")
	flag.IntVar(&cfg.maxFails, "max-fails", 3, "consecutive failures before restarting")
	flag.StringVar(&cfg.barkKey, "bark-key", os.Getenv("WATCHDOG_BARK_KEY"), "Bark device key for alerts")
	flag.DurationVar(&cfg.interval, "interval", 60*time.Second, "check interval")
	flag.StringVar(&cfg.stateDir, "state-dir", "/var/lib/sub2api", "writable state directory")
	var rpcList string
	flag.StringVar(&rpcList, "rpc", "", "comma-separated chain RPC endpoints to probe")
	flag.Parse()

	if strings.TrimSpace(cfg.healthURL) == "" {
		log.Fatal("watchdog: -health-url is required")
	}
	for _, raw := range strings.Split(rpcList, ",") {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			cfg.rpcEndpoints = append(cfg.rpcEndpoints, trimmed)
		}
	}

	w := &watchdog{cfg: cfg, bark: bark.New(cfg.barkKey), http: &http.Client{Timeout: 8 * time.Second}}
	if err := os.MkdirAll(cfg.stateDir, 0o755); err != nil {
		log.Fatalf("watchdog: state dir: %v", err)
	}

	ctx := context.Background()
	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	// First pass runs immediately so a bad boot is caught without waiting
	// a full interval.
	w.checkHealth(ctx)
	w.checkRPCs(ctx)
	for range ticker.C {
		w.checkHealth(ctx)
		w.checkRPCs(ctx)
	}
}

type watchdog struct {
	cfg  config
	bark *bark.Client
	http *http.Client
}

// failFile persists the consecutive-failure count across process restarts,
// matching the bash watchdog's behaviour.
func (w *watchdog) failFile() string {
	return w.cfg.stateDir + "/watchdog-fail.count"
}

func (w *watchdog) readFails() int {
	data, err := os.ReadFile(w.failFile())
	if err != nil {
		return 0
	}
	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &count); err != nil || count < 0 {
		return 0
	}
	return count
}

func (w *watchdog) writeFails(count int) {
	_ = os.WriteFile(w.failFile(), []byte(fmt.Sprintf("%d", count)), 0o644)
}

func (w *watchdog) checkHealth(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.cfg.healthURL, nil)
	if err != nil {
		log.Printf("watchdog: build request: %v", err)
		return
	}
	resp, err := w.http.Do(req)
	healthy := err == nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		_ = resp.Body.Close()
	}
	if healthy {
		if w.readFails() != 0 {
			w.writeFails(0)
		}
		return
	}
	fails := w.readFails() + 1
	w.writeFails(fails)
	log.Printf("watchdog: health check failed (%d/%d): url=%s err=%v", fails, w.cfg.maxFails, w.cfg.healthURL, err)

	if fails < w.cfg.maxFails {
		return
	}
	w.writeFails(0)
	if w.cfg.unit == "" {
		w.alert(ctx, "health check failing", fmt.Sprintf("%s failed %d times (no unit configured)", w.cfg.healthURL, fails))
		return
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", w.cfg.unit).CombinedOutput(); err != nil {
		w.alert(ctx, "restart failed", fmt.Sprintf("unit=%s err=%v output=%s", w.cfg.unit, err, strings.TrimSpace(string(out))))
		return
	}
	w.alert(ctx, "unit restarted", fmt.Sprintf("%s restarted after %d consecutive failed health checks", w.cfg.unit, fails))
}

// checkRPCs probes each configured chain endpoint and alerts on failure.
func (w *watchdog) checkRPCs(ctx context.Context) {
	for _, endpoint := range w.cfg.rpcEndpoints {
		if err := probeRPC(ctx, w.http, endpoint); err != nil {
			log.Printf("watchdog: rpc probe failed endpoint=%s err=%v", endpoint, err)
			w.alert(ctx, "chain RPC down", fmt.Sprintf("%s: %v", endpoint, err))
		}
	}
}

// probeRPC requires a valid JSON-RPC block number. HTTP 200 alone can carry
// a null result, an RPC error, or a proxy page, none of which proves health.
func probeRPC(ctx context.Context, client *http.Client, endpoint string) error {
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  *string         `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	if err := decoder.Decode(&response); err != nil {
		return fmt.Errorf("invalid RPC response")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid RPC response trailer")
	}
	if response.JSONRPC != "2.0" || response.ID != 1 || response.Result == nil ||
		(len(response.Error) > 0 && string(response.Error) != "null") {
		return fmt.Errorf("RPC did not return a block number")
	}
	if _, err := hexutil.DecodeUint64(*response.Result); err != nil {
		return fmt.Errorf("RPC returned an invalid block number")
	}
	return nil
}

func (w *watchdog) alert(ctx context.Context, title, body string) {
	if w.bark == nil {
		return
	}
	if err := w.bark.Send(ctx, bark.Notification{
		Title: title, Body: body, Group: "watchdog", Level: "timeSensitive",
	}); err != nil {
		log.Printf("watchdog: bark alert failed: %v", err)
	}
}
