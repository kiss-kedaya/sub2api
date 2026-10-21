package main

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCryptoReviewStartupEntry(t *testing.T) {
	if mode := os.Getenv("CRYPTO_STARTUP_TEST_CHILD"); mode != "" {
		flag.CommandLine = flag.NewFlagSet("crypto-review-startup", flag.ExitOnError)
		os.Args = []string{os.Args[0]}
		if mode != "missing-install" {
			os.Args = append(os.Args, "-"+mode)
		}
		main()
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"setup", "migrate-only", "missing-install"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(executable, "-test.run=^TestCryptoReviewStartupEntry$")
			cmd.Env = append(os.Environ(), "CRYPTO_STARTUP_TEST_CHILD="+mode,
				"CRYPTO_ADMIN_TEST_MODE=true", "SKIP_SETUP=false", "AUTO_SETUP=true", "DATA_DIR="+t.TempDir())
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "read-only crypto test mode") {
				t.Fatalf("expected process to refuse unsafe startup mode %s before setup or database access: err=%v output=%s", mode, err, output)
			}
		})
	}
}
