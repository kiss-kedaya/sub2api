package main

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/atrestmigrate"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// runAtRestCommand 执行 `sub2api -atrest-migrate` / `-atrest-verify`：
// 只连 PostgreSQL，不启动应用、Redis、worker，职责单一可审计。
// 写库前提：SUB2API_ATREST_ENABLED=true 且 SUB2API_ATREST_KEY 有效；
// 总开关默认关闭（默认部署不会产生任何新密文）。开启前必须先跑真实网关请求验收。
func runAtRestCommand(migrate, verify bool, tablesRaw string, batchSize int, dryRun bool) {
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if service.CryptoAdminTestMode() && !dryRun {
		log.Fatal("read-only crypto test mode forbids at-rest migration writes")
	}
	tables, err := atrestmigrate.ParseTables(tablesRaw)
	if err != nil {
		log.Fatalf("Invalid -atrest-tables: %v", err)
	}
	connector, err := pq.NewConnector(cfg.Database.DSNWithTimezone(cfg.Timezone))
	if err != nil {
		log.Fatalf("Create database connector: %v", err)
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("Connect database: %v", err)
	}

	result, runErr := atrestmigrate.Run(ctx, db, atrestmigrate.Options{
		Tables:    tables,
		BatchSize: batchSize,
		DryRun:    dryRun || verify,
		Verify:    verify,
	})
	printAtRestResult(result)
	if runErr != nil {
		log.Fatalf("At-rest migration failed: %v", runErr)
	}
	if verify || dryRun {
		return
	}
	broken := int64(0)
	remaining := int64(0)
	for _, table := range result.Tables {
		broken += table.Broken
		remaining += table.RemainingPlaintext
	}
	if broken > 0 {
		log.Fatalf("At-rest migration finished with %d broken rows; inspect logs before continuing", broken)
	}
	if remaining > 0 {
		log.Printf("At-rest migration finished but %d plaintext rows remain (concurrent writes?); re-run to converge", remaining)
		return
	}
	log.Printf("At-rest migration complete: 0 plaintext rows remain (%d migrated, %d already encrypted)",
		totalMigrated(result), totalAlready(result))
}

func printAtRestResult(result *atrestmigrate.Result) {
	if result == nil {
		return
	}
	mode := "migrate"
	if result.Verify {
		mode = "verify"
	} else if result.DryRun {
		mode = "dry-run"
	}
	log.Printf("at-rest %s finished in %s", mode, result.Elapsed.Round(time.Millisecond))
	for _, table := range result.Tables {
		log.Printf("  %-9s scanned=%-6d migrated=%-6d sealed=%-6d already=%-6d remaining_plaintext=%-6d broken=%d",
			table.Table, table.Scanned, table.Migrated, table.Sealed, table.Already, table.RemainingPlaintext, table.Broken)
	}
	if result.DryRun && !result.Verify {
		log.Printf("dry-run only: nothing was written; re-run with -atrest-dry-run=false to apply")
	}
}

func totalMigrated(result *atrestmigrate.Result) int64 {
	var total int64
	if result == nil {
		return 0
	}
	for _, table := range result.Tables {
		total += table.Migrated
	}
	return total
}

func totalAlready(result *atrestmigrate.Result) int64 {
	var total int64
	if result == nil {
		return 0
	}
	for _, table := range result.Tables {
		total += table.Already
	}
	return total
}
