// Command secrets-decrypt is the rollback tool for the at-rest credential
// encryption migration. It rewrites every encrypted account credential, proxy
// password and credential-bearing setting back to plaintext so an older binary
// can serve the deployment again.
//
// Run it with the service stopped, right before starting the rolled-back
// binary. The installation key (TOTP_ENCRYPTION_KEY or the persisted
// security_secrets value) must still be available: losing it makes the
// encrypted credentials unrecoverable.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func main() {
	apply := flag.Bool("apply", false, "Rewrite encrypted rows back to plaintext (default is a dry-run readiness check)")
	timeout := flag.Duration("timeout", 30*time.Minute, "Overall deadline for the rollback")
	flag.Parse()

	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if !cfg.Totp.HasPersistentEncryptionKey() {
		log.Fatalf("installation key is unavailable; restore TOTP_ENCRYPTION_KEY before rolling back")
	}

	client, sqlDB, err := repository.InitEnt(cfg)
	if err != nil {
		log.Fatalf("failed to init db: %v", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			log.Printf("failed to close db: %v", closeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	vault := service.NewSecretVault(cfg)
	if !vault.Available() {
		log.Fatalf("installation key is unusable; refusing to roll back")
	}
	if !*apply {
		log.Println("dry-run: key is available; re-run with -apply while the server is stopped to decrypt rows")
		return
	}
	if err := repository.RunSecretsEncryptionDecrypt(ctx, sqlDB, vault); err != nil {
		log.Fatalf("secrets rollback failed: %v", err)
	}
	log.Println("secrets rollback finished; start the previous binary now")
}
