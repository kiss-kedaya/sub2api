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
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// PgDumper implements service.DBDumper using pg_dump/psql
type PgDumper struct {
	cfg            *config.DatabaseConfig
	backup         config.BackupConfig
	db             *sql.DB
	commandContext func(context.Context, string, ...string) *exec.Cmd
	// niceAvailable reports whether pg_dump should run under nice(1). Nil means
	// "on Linux when nice is on PATH"; tests inject a fixed answer.
	niceAvailable func() bool
	// lookPath resolves an executable before pg_dump is started, so a wrong
	// backup.pg_dump_path is reported as such and not as nice's "exit status 127".
	// Nil skips the check; NewPgDumper sets exec.LookPath.
	lookPath func(string) (string, error)
}

// NewPgDumper creates a new PgDumper
func NewPgDumper(cfg *config.Config, db *sql.DB) service.DBDumper {
	return &PgDumper{
		cfg:            &cfg.Database,
		backup:         cfg.Backup,
		db:             db,
		commandContext: exec.CommandContext,
		lookPath:       exec.LookPath,
	}
}

// pgDumpLockWaitTimeout bounds how long a partial dump waits for table locks, so
// a stuck DDL cannot hold the backup (and the migration advisory lock) forever.
const pgDumpLockWaitTimeout = "30s"

// pgDumpCommand returns the program and arguments for pg_dump.
//
// Without backup.exclude_table_data this is the original full dump, argument for
// argument. With it, the dump is a partial snapshot: every table's schema is kept
// but the listed tables' rows are skipped. --clean/--if-exists are deliberately
// dropped there: replaying such a snapshot into a live database must fail on the
// first "already exists" and roll back under --single-transaction, rather than
// DROP the very tables whose data the snapshot does not contain.
func (d *PgDumper) pgDumpCommand() (string, []string) {
	args := []string{
		"-h", d.cfg.Host,
		"-p", fmt.Sprintf("%d", d.cfg.Port),
		"-U", d.cfg.User,
		"-d", d.cfg.DBName,
		"--no-owner",
		"--no-acl",
	}
	name := d.pgDumpBinary()

	if len(d.backup.ExcludeTableData) == 0 {
		return name, append(args, "--clean", "--if-exists")
	}
	for _, table := range d.backup.ExcludeTableData {
		args = append(args, "--exclude-table-data="+table)
	}
	args = append(args, "--lock-wait-timeout="+pgDumpLockWaitTimeout)

	// A scheduled snapshot should not compete with request traffic for CPU.
	if d.useNice() {
		return "nice", append([]string{"-n", "19", name}, args...)
	}
	return name, args
}

// pgDumpBinary is the configured pg_dump executable, or "pg_dump" from PATH.
func (d *PgDumper) pgDumpBinary() string {
	if d.backup.PgDumpPath != "" {
		return d.backup.PgDumpPath
	}
	return "pg_dump"
}

// checkExecutables fails before anything is locked or started when pg_dump (or
// nice, which wraps it in partial mode) cannot be found.
func (d *PgDumper) checkExecutables(command string) error {
	if d.lookPath == nil {
		return nil
	}
	if command == "nice" {
		if _, err := d.lookPath("nice"); err != nil {
			return fmt.Errorf("nice is not available: %w", err)
		}
	}
	if _, err := d.lookPath(d.pgDumpBinary()); err != nil {
		return fmt.Errorf("pg_dump executable %q cannot be run (check backup.pg_dump_path and PATH): %w", d.pgDumpBinary(), err)
	}
	return nil
}

func (d *PgDumper) useNice() bool {
	if d.niceAvailable != nil {
		return d.niceAvailable()
	}
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("nice")
	return err == nil
}

// Dump executes pg_dump and returns a streaming reader of the output
func (d *PgDumper) Dump(ctx context.Context) (io.ReadCloser, error) {
	if d.db == nil {
		return nil, errors.New("acquire backup migration lock: nil sql db")
	}
	name, args := d.pgDumpCommand()
	if err := d.checkExecutables(name); err != nil {
		return nil, err
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

	commandContext := d.commandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	cmd := commandContext(ctx, name, args...)
	if d.cfg.Password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+d.cfg.Password)
	}
	if d.cfg.SSLMode != "" {
		cmd.Env = append(cmd.Environ(), "PGSSLMODE="+d.cfg.SSLMode)
	}
	// Keep the end of pg_dump's stderr: a lock timeout, a permission error or a
	// refused connection otherwise shows up as a bare "exit status 1".
	stderr := &tailBuffer{max: pgDumpStderrTailBytes}
	cmd.Stderr = stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create stdout pipe: %w", err), releaseLock())
	}

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		return nil, errors.Join(fmt.Errorf("start pg_dump: %w", err), releaseLock())
	}

	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd, release: releaseLock, stderr: stderr, password: d.cfg.Password}, nil
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

// pgDumpStderrTailBytes bounds how much of pg_dump's stderr is kept.
const pgDumpStderrTailBytes = 4096

// tailBuffer is an io.Writer that keeps only the last max bytes written to it. It
// is written by the goroutine that os/exec runs for the process and read after
// cmd.Wait has returned, which is when that goroutine is done.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= t.max {
		t.buf = append(t.buf[:0], p[n-t.max:]...)
		return n, nil
	}
	if over := len(t.buf) + n - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

var (
	// postgres:// and postgresql:// connection URIs carry the password in the user info.
	pgConnectionURI = regexp.MustCompile(`(?i)postgres(?:ql)?://\S+`)
	// libpq key/value connection strings: password=..., sslpassword='...'.
	pgPasswordPair = regexp.MustCompile(`(?i)\b(password|passwd|pwd|sslpassword)\s*=\s*('[^']*'|"[^"]*"|\S+)`)
)

// pgDumpFailureDetail turns captured stderr into one line that is safe to put in an
// error message and in a stored backup record: the configured database password,
// connection URIs and password=... pairs are blanked, control characters dropped.
func pgDumpFailureDetail(raw []byte, password string) string {
	text := strings.ToValidUTF8(string(raw), "")
	if password != "" {
		text = strings.ReplaceAll(text, password, "***")
	}
	text = pgConnectionURI.ReplaceAllString(text, "postgres://***")
	text = pgPasswordPair.ReplaceAllString(text, "$1=***")
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, text)

	var lines []string
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " | ")
}

// cmdReadCloser wraps a command stdout pipe and waits for the process on Close
type cmdReadCloser struct {
	io.ReadCloser
	cmd       *exec.Cmd
	release   func() error
	stderr    *tailBuffer
	password  string
	closeOnce sync.Once
	closeErr  error
}

// failureDetail is the sanitized tail of pg_dump's stderr, ready to append to an error.
func (c *cmdReadCloser) failureDetail() string {
	if c.stderr == nil {
		return ""
	}
	detail := pgDumpFailureDetail(c.stderr.buf, c.password)
	if detail == "" {
		return ""
	}
	return " (stderr tail: " + detail + ")"
}

func (c *cmdReadCloser) Close() error {
	c.closeOnce.Do(func() {
		var closeErrs []error
		if err := c.ReadCloser.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close pg_dump stdout: %w", err))
		}
		if err := c.cmd.Wait(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("pg_dump exited with error: %w%s", err, c.failureDetail()))
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
