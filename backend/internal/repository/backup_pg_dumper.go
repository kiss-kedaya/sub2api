package repository

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// PgDumper implements service.DBDumper using pg_dump/psql
type PgDumper struct {
	cfg            *config.DatabaseConfig
	db             *sql.DB
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

// NewPgDumper creates a new PgDumper
func NewPgDumper(cfg *config.Config, db *sql.DB) service.DBDumper {
	return &PgDumper{
		cfg:            &cfg.Database,
		db:             db,
		commandContext: exec.CommandContext,
	}
}

// Dump executes pg_dump and returns a streaming reader of the output
func (d *PgDumper) Dump(ctx context.Context) (io.ReadCloser, error) {
	if d.db == nil {
		return nil, errors.New("acquire backup migration lock: nil sql db")
	}
	lockConn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire backup migration lock connection: %w", err)
	}
	if err := pgAdvisoryLock(ctx, lockConn); err != nil {
		// Lock acquisition can fail after PostgreSQL accepted the request. Discard
		// the session because ownership is ambiguous and advisory locks are session-scoped.
		discardSQLConnection(lockConn)
		return nil, fmt.Errorf("acquire backup migration lock: %w", err)
	}
	releaseLock := func() error {
		return releaseBackupMigrationLock(lockConn)
	}

	args := []string{
		"-h", d.cfg.Host,
		"-p", fmt.Sprintf("%d", d.cfg.Port),
		"-U", d.cfg.User,
		"-d", d.cfg.DBName,
		"--no-owner",
		"--no-acl",
		"--clean",
		"--if-exists",
	}

	commandContext := d.commandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	cmd := commandContext(ctx, "pg_dump", args...)
	if d.cfg.Password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+d.cfg.Password)
	}
	if d.cfg.SSLMode != "" {
		cmd.Env = append(cmd.Environ(), "PGSSLMODE="+d.cfg.SSLMode)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create stdout pipe: %w", err), releaseLock())
	}

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		return nil, errors.Join(fmt.Errorf("start pg_dump: %w", err), releaseLock())
	}

	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd, release: releaseLock}, nil
}

func releaseBackupMigrationLock(conn *sql.Conn) error {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pgAdvisoryUnlock(unlockCtx, conn); err != nil {
		// A failed unlock must not return a possibly lock-owning session to the pool.
		discardSQLConnection(conn)
		return fmt.Errorf("release backup migration lock: %w", err)
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("close backup migration lock connection: %w", err)
	}
	return nil
}

func discardSQLConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// Restore executes psql to restore from a streaming reader
func (d *PgDumper) Restore(ctx context.Context, data io.Reader) error {
	args := []string{
		"-h", d.cfg.Host,
		"-p", fmt.Sprintf("%d", d.cfg.Port),
		"-U", d.cfg.User,
		"-d", d.cfg.DBName,
		"--no-psqlrc",
		"--single-transaction",
		"--set=ON_ERROR_STOP=1",
	}

	cmd := exec.CommandContext(ctx, "psql", args...)
	if d.cfg.Password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+d.cfg.Password)
	}
	if d.cfg.SSLMode != "" {
		cmd.Env = append(cmd.Environ(), "PGSSLMODE="+d.cfg.SSLMode)
	}

	safeData, scanDone := safeRestoreSQLReader(data)
	cmd.Stdin = safeData

	output, err := cmd.CombinedOutput()
	if scanErr := <-scanDone; scanErr != nil {
		return fmt.Errorf("unsafe backup SQL: %w", scanErr)
	}
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(output))
	}
	return nil
}

// safeRestoreSQLReader rejects psql client commands that can execute a shell
// command or access files on the API host. Plain pg_dump output still streams
// through unchanged, including COPY data and dump control markers.
func safeRestoreSQLReader(data io.Reader) (io.Reader, <-chan error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		r := bufio.NewReader(data)
		inCopyData := false
		for {
			line, err := r.ReadString('\n')
			if len(line) > 0 {
				if !inCopyData {
					if command := unsafeRestorePSQLCommand(line); command != "" {
						reason := fmt.Errorf("psql meta-command \\%s is not allowed", command)
						_ = pw.CloseWithError(reason)
						done <- reason
						return
					}
					inCopyData = isRestoreCopyStart(line)
				} else if strings.TrimSpace(line) == `\.` {
					inCopyData = false
				}
				if _, writeErr := io.WriteString(pw, line); writeErr != nil {
					done <- writeErr
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					_ = pw.CloseWithError(err)
					done <- err
					return
				}
				_ = pw.Close()
				done <- nil
				return
			}
		}
	}()
	return pr, done
}

func unsafeRestorePSQLCommand(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '\\' {
		return ""
	}
	fields := strings.Fields(trimmed[1:])
	if len(fields) == 0 {
		return ""
	}
	command := strings.ToLower(fields[0])
	switch command {
	case "!", "shell", "include", "ir", "copy", "o", "out", "write", "edit", "g", "gx", "gexec":
		return command
	default:
		return ""
	}
}

func isRestoreCopyStart(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(normalized, "copy ") && strings.HasSuffix(normalized, " from stdin;")
}

// cmdReadCloser wraps a command stdout pipe and waits for the process on Close
type cmdReadCloser struct {
	io.ReadCloser
	cmd       *exec.Cmd
	release   func() error
	closeOnce sync.Once
	closeErr  error
}

func (c *cmdReadCloser) Close() error {
	c.closeOnce.Do(func() {
		var closeErrs []error
		if err := c.ReadCloser.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close pg_dump stdout: %w", err))
		}
		if err := c.cmd.Wait(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("pg_dump exited with error: %w", err))
		}
		if c.release != nil {
			if err := c.release(); err != nil {
				closeErrs = append(closeErrs, err)
			}
		}
		c.closeErr = errors.Join(closeErrs...)
	})
	return c.closeErr
}
