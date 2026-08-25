package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultPprofAddress = "127.0.0.1:6060"

type pprofServer struct {
	server *http.Server
}

func startPprofServerFromEnv() (*pprofServer, error) {
	if !parsePprofBool(os.Getenv("PPROF_ENABLED")) {
		return nil, nil
	}

	address := strings.TrimSpace(os.Getenv("PPROF_ADDR"))
	if address == "" {
		address = defaultPprofAddress
	}
	if err := validateLoopbackAddress(address); err != nil {
		return nil, err
	}

	// CPU/heap/goroutine profiling stays low overhead by default. Contention
	// profiling is opt-in because it instruments synchronization on every hot
	// request and can distort a saturated production instance.
	blockRate := parsePprofInt(os.Getenv("PPROF_BLOCK_RATE"), 0)
	mutexFraction := parsePprofInt(os.Getenv("PPROF_MUTEX_FRACTION"), 0)
	runtime.SetBlockProfileRate(blockRate)
	runtime.SetMutexProfileFraction(mutexFraction)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle("/debug/pprof/"+name, pprof.Handler(name))
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(0)
		return nil, fmt.Errorf("listen for pprof on %s: %w", address, err)
	}
	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	p := &pprofServer{server: server}
	go func() {
		log.Printf("pprof diagnostics listening on %s (loopback only)", address)
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("pprof diagnostics stopped unexpectedly: %v", serveErr)
		}
	}()
	return p, nil
}

func (p *pprofServer) Shutdown(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	runtime.SetBlockProfileRate(0)
	runtime.SetMutexProfileFraction(0)
	return p.server.Shutdown(ctx)
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid PPROF_ADDR %q: %w", address, err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("PPROF_ADDR must use a loopback host, got %q", host)
	}
	return nil
}

func parsePprofBool(raw string) bool {
	enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
	return err == nil && enabled
}

func parsePprofInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
