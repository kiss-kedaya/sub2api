package repository

import (
	"bufio"
	"bytes"
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

// PgDumper implements service.ScopedDBDumper (and service.DBDumper) using pg_dump/psql
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

var _ service.ScopedDBDumper = (*PgDumper)(nil)

// runtimeExcludeTablePattern validates per-run exclude table entries passed via
// DumpWithOptions. Allows an optional exact schema prefix plus a table name that
// may contain '*' (used by built-in partition patterns such as public.usage_logs_*),
// while rejecting schema wildcards, catch-all '*', options, whitespace, quotes,
// or SQL/shell metacharacters.
var runtimeExcludeTablePattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_*]*$`)

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

// pgDumpCommandForExcludes returns the program and arguments for pg_dump.
//
// Without excludeTableData this is the original full dump, argument for
// argument. With it, the dump is a partial snapshot: every table's schema is kept
// but the listed tables' rows are skipped. --clean/--if-exists are deliberately
// dropped there: replaying such a snapshot into a live database must fail on the
// first "already exists" and roll back under --single-transaction, rather than
// DROP the very tables whose data the snapshot does not contain.
func (d *PgDumper) pgDumpCommandForExcludes(excludeTableData []string) (string, []string) {
	args := []string{
		"-h", d.cfg.Host,
		"-p", fmt.Sprintf("%d", d.cfg.Port),
		"-U", d.cfg.User,
		"-d", d.cfg.DBName,
		"--no-owner",
		"--no-acl",
	}
	name := d.pgDumpBinary()

	if len(excludeTableData) == 0 {
		return name, append(args, "--clean", "--if-exists")
	}
	for _, table := range excludeTableData {
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

// Dump executes pg_dump with the deployment-configured backup settings and
// returns a streaming reader of the output.
func (d *PgDumper) Dump(ctx context.Context) (io.ReadCloser, error) {
	return d.dumpWithExcludeTableData(ctx, d.backup.ExcludeTableData)
}

// DumpWithOptions executes pg_dump with per-run dump options. When deployment
// config already sets backup.exclude_table_data, that list remains authoritative
// and cannot be weakened by runtime options.
func (d *PgDumper) DumpWithOptions(ctx context.Context, opts service.BackupDumpOptions) (io.ReadCloser, error) {
	if len(d.backup.ExcludeTableData) > 0 {
		return d.dumpWithExcludeTableData(ctx, d.backup.ExcludeTableData)
	}
	var exclude []string
	for i, raw := range opts.ExcludeTableData {
		table := strings.TrimSpace(raw)
		if table == "" || !runtimeExcludeTablePattern.MatchString(table) {
			return nil, fmt.Errorf("invalid exclude_table_data entry at index %d", i)
		}
		exclude = append(exclude, table)
	}
	return d.dumpWithExcludeTableData(ctx, exclude)
}

func (d *PgDumper) dumpWithExcludeTableData(ctx context.Context, excludeTableData []string) (io.ReadCloser, error) {
	if d.db == nil {
		return nil, errors.New("acquire backup migration lock: nil sql db")
	}
	name, args := d.pgDumpCommandForExcludes(excludeTableData)
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

	cmdCtx, cancelCmd := context.WithCancel(ctx)
	defer cancelCmd()

	commandContext := d.commandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	cmd := commandContext(cmdCtx, "psql", args...)
	if d.cfg.Password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+d.cfg.Password)
	}
	if d.cfg.SSLMode != "" {
		cmd.Env = append(cmd.Environ(), "PGSSLMODE="+d.cfg.SSLMode)
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create psql stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		return fmt.Errorf("start psql: %w", err)
	}

	type restoreStreamResult struct {
		scanErr error
		readErr error
	}
	streamDone := make(chan restoreStreamResult, 1)
	go func() {
		defer close(streamDone)
		onReject := func() {
			// Terminate psql before stdin can ever reach EOF so --single-transaction
			// rolls back instead of committing the already-streamed prefix.
			cancelCmd()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
		scanErr, readErr := streamSafeRestoreSQL(data, stdinPipe, onReject, true)
		if scanErr == nil && readErr == nil {
			// Only signal EOF when the entire stream validated cleanly.
			_ = stdinPipe.Close()
		}
		streamDone <- restoreStreamResult{scanErr: scanErr, readErr: readErr}
	}()

	err = cmd.Wait()
	// Ensure the pipe is closed after psql exits (for early SQL failures or killed runs).
	_ = stdinPipe.Close()
	streamRes := <-streamDone
	if streamRes.scanErr != nil {
		return fmt.Errorf("unsafe backup SQL: %w", streamRes.scanErr)
	}
	if streamRes.readErr != nil {
		return fmt.Errorf("read backup SQL: %w", streamRes.readErr)
	}
	if err != nil {
		detail := pgDumpFailureDetail(output.Bytes(), d.cfg.Password)
		if detail != "" {
			return fmt.Errorf("%v: %s", err, detail)
		}
		return err
	}
	return nil
}

// safeRestoreSQLReader rejects psql client meta-commands (fail-closed outside
// strings, comments, COPY stdin data, and pg_dump's standalone \restrict /
// \unrestrict markers) as well as server-side COPY FROM/TO PROGRAM or file
// transfers and standard_conforming_strings downgrades. Plain pg_dump output
// still streams through unchanged.
func safeRestoreSQLReader(data io.Reader) (io.Reader, <-chan error) {
	return safeRestoreSQLReaderWithCancel(data, nil)
}

func safeRestoreSQLReaderWithCancel(data io.Reader, onReject func()) (*io.PipeReader, <-chan error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		scanErr, readErr := streamSafeRestoreSQL(data, pw, onReject, false)
		if scanErr != nil {
			_ = pw.CloseWithError(scanErr)
			done <- scanErr
			return
		}
		if readErr != nil {
			_ = pw.CloseWithError(readErr)
			done <- readErr
			return
		}
		_ = pw.Close()
		done <- nil
	}()
	return pr, done
}

func streamSafeRestoreSQL(data io.Reader, dst io.Writer, onReject func(), ignoreBrokenPipe bool) (scanErr error, readErr error) {
	r := bufio.NewReader(data)
	scanner := &restoreSQLScanner{}
	firstLine := true
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			scanLine := line
			if firstLine {
				firstLine = false
				scanLine = strings.TrimPrefix(scanLine, "\xef\xbb\xbf")
			}
			if sErr := scanner.scanLine(scanLine); sErr != nil {
				if onReject != nil {
					onReject()
				}
				return sErr, nil
			}
			if _, writeErr := io.WriteString(dst, scanLine); writeErr != nil {
				if ignoreBrokenPipe || errors.Is(writeErr, io.ErrClosedPipe) {
					return nil, nil
				}
				return nil, writeErr
			}
		}
		if err != nil {
			if err != io.EOF {
				if onReject != nil {
					onReject()
				}
				return nil, err
			}
			if finalizeErr := scanner.finalize(); finalizeErr != nil {
				if onReject != nil {
					onReject()
				}
				return finalizeErr, nil
			}
			return nil, nil
		}
	}
}

type restoreSQLLexState int

const (
	restoreLexSQL restoreSQLLexState = iota
	restoreLexBlockComment
	restoreLexSingleQuote
	restoreLexEscapeQuote
	restoreLexDoubleQuote
	restoreLexDollarQuote
	restoreLexCopyData
)

type restoreCopyPhase int

const (
	restoreCopyNone restoreCopyPhase = iota
	restoreCopyInit
	restoreCopyFrom
	restoreCopyFromStdin
	restoreCopyTo
)

type restoreSetConfigState int

const (
	restoreSetConfigNone restoreSetConfigState = iota
	restoreSetConfigExpectOpenParen
	restoreSetConfigExpectFirstArg
	restoreSetConfigExpectComma
)

type restoreSQLScanner struct {
	state                   restoreSQLLexState
	blockCommentDepth       int
	escapeNext              bool
	singleQuoteOddBackslash bool
	afterEscapeQuote        bool
	escapeQuoteSawEOL       bool
	dollarTag               string
	restrictKey             string

	// Current top-level statement state (reset at each top-level ';').
	stmtStarted            bool
	stmtFirstWord          string
	parenDepth             int
	copyPhase              restoreCopyPhase
	pendingCopyIn          bool
	stmtMentionsStdStrings bool
	stmtHasUnicodeIdent    bool
	stmtTokens             []string

	// Capturing current quoted literal/identifier when needed for policy checks.
	captureBuf       strings.Builder
	capturing        bool
	captureTruncated bool
	captureKind      restoreTokenKind

	// Tracks top-level set_config(...) first argument to block obfuscated
	// set_config('standard_conforming_strings', ...) downgrades.
	setConfigState       restoreSetConfigState
	setConfigParen       int
	setConfigUnicodeCall bool
}

type restoreTokenKind int

const (
	restoreTokIdent restoreTokenKind = iota
	restoreTokQuotedIdent
	restoreTokUnicodeQuotedIdent
	restoreTokPlainString
	restoreTokString
	restoreTokOther
)

func (s *restoreSQLScanner) resetStatement() {
	s.stmtStarted = false
	s.stmtFirstWord = ""
	s.parenDepth = 0
	s.copyPhase = restoreCopyNone
	s.stmtMentionsStdStrings = false
	s.stmtHasUnicodeIdent = false
	s.stmtTokens = s.stmtTokens[:0]
	s.setConfigState = restoreSetConfigNone
	s.setConfigParen = 0
	s.setConfigUnicodeCall = false
	s.afterEscapeQuote = false
	s.escapeQuoteSawEOL = false
}

func (s *restoreSQLScanner) finalize() error {
	if s.stmtStarted {
		if err := s.validateStatementEnd(); err != nil {
			return err
		}
	}
	return nil
}

func (s *restoreSQLScanner) scanLine(line string) error {
	if strings.IndexByte(line, 0) >= 0 {
		return errors.New("NUL byte in backup SQL is not allowed")
	}

	if s.state == restoreLexCopyData {
		trimmedEOL := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if trimmedEOL == `\.` {
			s.state = restoreLexSQL
			s.resetStatement()
		}
		return nil
	}

	// PostgreSQL 17.6+ / 18 pg_dump wraps plain dumps in standalone
	// \restrict <alnum-key> ... \unrestrict <same-alnum-key> lines.
	if s.state == restoreLexSQL && !s.stmtStarted && !s.pendingCopyIn {
		if cmd, key, ok := parseStandaloneRestrictCommand(line); ok {
			switch cmd {
			case "restrict":
				if s.restrictKey != "" {
					return errors.New("nested psql meta-command \\restrict is not allowed")
				}
				s.restrictKey = key
				return nil
			case "unrestrict":
				if s.restrictKey == "" || s.restrictKey != key {
					return errors.New("unmatched psql meta-command \\unrestrict is not allowed")
				}
				s.restrictKey = ""
				return nil
			}
		}
	}

	for i := 0; i < len(line); {
		switch s.state {
		case restoreLexBlockComment:
			if i+1 < len(line) && line[i] == '/' && line[i+1] == '*' {
				s.blockCommentDepth++
				i += 2
			} else if i+1 < len(line) && line[i] == '*' && line[i+1] == '/' {
				s.blockCommentDepth--
				i += 2
				if s.blockCommentDepth == 0 {
					s.state = restoreLexSQL
				}
			} else {
				i++
			}

		case restoreLexSingleQuote:
			if line[i] == '\'' {
				// Reject an odd run of backslashes immediately before a quote
				// inside a plain '...' literal: that is the exact sequence
				// where standard_conforming_strings=on vs off diverges in psql.
				if s.singleQuoteOddBackslash {
					return errors.New("backslash before quote in plain string literal is not allowed")
				}
				if i+1 < len(line) && line[i+1] == '\'' {
					if s.capturing {
						s.appendCapture('\'')
					}
					i += 2
				} else {
					s.state = restoreLexSQL
					s.singleQuoteOddBackslash = false
					i++
					if err := s.finishCapturedToken(); err != nil {
						return err
					}
				}
			} else {
				if line[i] == '\\' {
					s.singleQuoteOddBackslash = !s.singleQuoteOddBackslash
				} else {
					s.singleQuoteOddBackslash = false
				}
				if s.capturing {
					s.appendCapture(line[i])
				}
				i++
			}

		case restoreLexEscapeQuote:
			if s.escapeNext {
				s.escapeNext = false
				if s.capturing {
					s.appendCapture(line[i])
				}
				i++
			} else if line[i] == '\\' {
				s.escapeNext = true
				i++
			} else if line[i] == '\'' {
				if i+1 < len(line) && line[i+1] == '\'' {
					if s.capturing {
						s.appendCapture('\'')
					}
					i += 2
				} else {
					s.state = restoreLexSQL
					i++
					if err := s.finishCapturedToken(); err != nil {
						return err
					}
					// In PostgreSQL psqlscan.l, <xe>{quote}{quotecontinue}
					// continues escape-string mode across whitespace/comments
					// containing a CR/LF followed by another single quote.
					s.afterEscapeQuote = true
					s.escapeQuoteSawEOL = false
				}
			} else {
				if s.capturing {
					s.appendCapture(line[i])
				}
				i++
			}

		case restoreLexDoubleQuote:
			if line[i] == '"' {
				if i+1 < len(line) && line[i+1] == '"' {
					if s.capturing {
						s.appendCapture('"')
					}
					i += 2
				} else {
					s.state = restoreLexSQL
					i++
					if err := s.finishCapturedToken(); err != nil {
						return err
					}
				}
			} else {
				if s.capturing {
					s.appendCapture(line[i])
				}
				i++
			}

		case restoreLexDollarQuote:
			if strings.HasPrefix(line[i:], s.dollarTag) {
				i += len(s.dollarTag)
				s.dollarTag = ""
				s.state = restoreLexSQL
				if err := s.finishCapturedToken(); err != nil {
					return err
				}
			} else {
				if s.capturing {
					s.appendCapture(line[i])
				}
				i++
			}

		case restoreLexSQL:
			b := line[i]
			if isASCIIWhitespace(b) {
				if s.afterEscapeQuote && (b == '\n' || b == '\r') {
					s.escapeQuoteSawEOL = true
				}
				i++
				continue
			}
			// Line comment: in PostgreSQL psqlscan.l ("--"[^\r\n]*), either CR
			// or LF ends the comment. Never skip past an embedded bare '\r'.
			if b == '-' && i+1 < len(line) && line[i+1] == '-' {
				i += 2
				for i < len(line) && line[i] != '\r' && line[i] != '\n' {
					i++
				}
				continue
			}
			// Nested block comment.
			if b == '/' && i+1 < len(line) && line[i+1] == '*' {
				s.afterEscapeQuote = false
				s.escapeQuoteSawEOL = false
				s.state = restoreLexBlockComment
				s.blockCommentDepth = 1
				i += 2
				continue
			}
			// Fail-closed: reject ANY psql backslash meta-command in SQL mode,
			// whether at line start, without spaces (\!id, \o|cmd), or inline.
			if b == '\\' {
				cmd := extractPSQLMetaCommandName(line[i:])
				if cmd == "" {
					return errors.New("psql meta-command is not allowed")
				}
				return fmt.Errorf("psql meta-command \\%s is not allowed", cmd)
			}
			// Nothing except whitespace/comments may follow COPY ... FROM stdin;
			// on the same line (psql would execute trailing tokens after \.).
			if s.pendingCopyIn {
				return errors.New("unexpected token after COPY FROM stdin on the same line is not allowed")
			}

			// Escape-string literal continuation across CR/LF (<xe>{quote}{quotecontinue}).
			if b == '\'' && s.afterEscapeQuote && s.escapeQuoteSawEOL {
				s.afterEscapeQuote = false
				s.escapeQuoteSawEOL = false
				i++
				s.state = restoreLexEscapeQuote
				s.escapeNext = false
				s.startCapture(restoreTokString)
				if err := s.onStringStart(); err != nil {
					return err
				}
				continue
			}

			s.afterEscapeQuote = false
			s.escapeQuoteSawEOL = false

			// Identifier / keyword, E'...' escape string, or U&'...' / U&"..." Unicode literal.
			if isPGIdentStart(b) {
				start := i
				i++
				for i < len(line) && isPGIdentCont(line[i]) {
					i++
				}
				ident := line[start:i]
				if (ident == "E" || ident == "e") && i < len(line) && line[i] == '\'' {
					i++
					s.state = restoreLexEscapeQuote
					s.escapeNext = false
					s.startCapture(restoreTokString)
					if err := s.onStringStart(); err != nil {
						return err
					}
					continue
				}
				if (ident == "U" || ident == "u") && i+1 < len(line) && line[i] == '&' {
					if line[i+1] == '"' {
						i += 2
						s.state = restoreLexDoubleQuote
						s.startCapture(restoreTokUnicodeQuotedIdent)
						continue
					}
					if line[i+1] == '\'' {
						i += 2
						s.state = restoreLexSingleQuote
						s.singleQuoteOddBackslash = false
						s.startCapture(restoreTokString)
						if err := s.onStringStart(); err != nil {
							return err
						}
						continue
					}
				}
				if err := s.onSQLToken(restoreTokIdent, strings.ToLower(ident)); err != nil {
					return err
				}
				continue
			}

			if b == '\'' {
				i++
				s.state = restoreLexSingleQuote
				s.singleQuoteOddBackslash = false
				s.startCapture(restoreTokPlainString)
				if err := s.onStringStart(); err != nil {
					return err
				}
				continue
			}

			if b == '"' {
				i++
				s.state = restoreLexDoubleQuote
				s.startCapture(restoreTokQuotedIdent)
				continue
			}

			if b == '$' {
				if tag, ok := parseDollarQuoteTag(line[i:]); ok {
					s.dollarTag = tag
					s.state = restoreLexDollarQuote
					s.startCapture(restoreTokString)
					i += len(tag)
					if err := s.onStringStart(); err != nil {
						return err
					}
					continue
				}
				i++
				if err := s.onSQLToken(restoreTokOther, "$"); err != nil {
					return err
				}
				continue
			}

			if b == '(' {
				i++
				s.parenDepth++
				if err := s.onSQLToken(restoreTokOther, "("); err != nil {
					return err
				}
				continue
			}

			if b == ')' {
				i++
				if s.parenDepth > 0 {
					s.parenDepth--
				}
				if err := s.onSQLToken(restoreTokOther, ")"); err != nil {
					return err
				}
				continue
			}

			if b == ';' {
				i++
				if err := s.validateStatementEnd(); err != nil {
					return err
				}
				isCopyStdin := s.stmtFirstWord == "copy" && s.copyPhase == restoreCopyFromStdin && s.parenDepth == 0
				s.resetStatement()
				if isCopyStdin {
					s.pendingCopyIn = true
				}
				continue
			}

			// Numeric literals (including leading-dot decimals like .5e, hex/oct/bin
			// like 0x1e, underscored numbers like 1_0e, and realfail/numeric_junk
			// like 0e/1.e) without consuming `..` range operators so a trailing `e'`
			// can never diverge from PostgreSQL's psqlscan.l into E'...'.
			if (b >= '0' && b <= '9') || (b == '.' && i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9') {
				start := i
				hasDot := b == '.'
				i++
				for i < len(line) {
					c := line[i]
					if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c >= 0x80 {
						i++
						continue
					}
					if c == '.' && !hasDot && (i+1 >= len(line) || line[i+1] != '.') {
						hasDot = true
						i++
						continue
					}
					break
				}
				if err := s.onSQLToken(restoreTokOther, line[start:i]); err != nil {
					return err
				}
				continue
			}

			i++
			if err := s.onSQLToken(restoreTokOther, string(b)); err != nil {
				return err
			}
		}
	}

	if s.pendingCopyIn {
		if s.state != restoreLexSQL {
			return errors.New("unclosed block comment after COPY FROM stdin is not allowed")
		}
		s.pendingCopyIn = false
		s.state = restoreLexCopyData
	}

	return nil
}

func (s *restoreSQLScanner) startCapture(kind restoreTokenKind) {
	s.captureBuf.Reset()
	s.capturing = true
	s.captureTruncated = false
	s.captureKind = kind
}

func (s *restoreSQLScanner) appendCapture(b byte) {
	if s.captureBuf.Len() < 64 {
		_ = s.captureBuf.WriteByte(b)
	} else {
		s.captureTruncated = true
	}
}

func (s *restoreSQLScanner) finishCapturedToken() error {
	if !s.capturing {
		return nil
	}
	text := strings.ToLower(s.captureBuf.String())
	kind := s.captureKind
	truncated := s.captureTruncated
	s.capturing = false
	s.captureTruncated = false
	s.captureBuf.Reset()
	return s.onSQLTokenWithTruncated(kind, text, truncated)
}

func (s *restoreSQLScanner) onStringStart() error {
	if s.stmtFirstWord == "copy" {
		switch s.copyPhase {
		case restoreCopyFrom:
			return errors.New("COPY ... FROM file is not allowed")
		case restoreCopyTo:
			return errors.New("COPY ... TO file is not allowed")
		case restoreCopyFromStdin:
			return errors.New("unexpected token after COPY ... FROM stdin is not allowed")
		}
	}
	return nil
}

func (s *restoreSQLScanner) onSQLToken(kind restoreTokenKind, value string) error {
	return s.onSQLTokenWithTruncated(kind, value, false)
}

func (s *restoreSQLScanner) onSQLTokenWithTruncated(kind restoreTokenKind, value string, truncated bool) error {
	if !s.stmtStarted {
		s.stmtStarted = true
		if kind == restoreTokIdent {
			s.stmtFirstWord = value
			if value == "copy" {
				s.copyPhase = restoreCopyInit
			}
		} else {
			s.stmtFirstWord = "<other>"
		}
	}

	if kind == restoreTokUnicodeQuotedIdent {
		s.stmtHasUnicodeIdent = true
	}

	if (kind == restoreTokIdent || kind == restoreTokQuotedIdent) && value == "standard_conforming_strings" {
		s.stmtMentionsStdStrings = true
	}

	if s.stmtFirstWord == "set" || s.stmtFirstWord == "alter" || s.stmtFirstWord == "reset" {
		if len(s.stmtTokens) < 16 {
			s.stmtTokens = append(s.stmtTokens, value)
		}
	}

	// Guard top-level set_config(...) calls so standard_conforming_strings
	// cannot be toggled off to create a lexer differential with psql.
	// Skip relation-name detection in `COPY [schema.]set_config (col) FROM stdin;`.
	isCopyTargetRelation := s.stmtFirstWord == "copy" && s.copyPhase == restoreCopyInit && s.parenDepth == 0
	switch s.setConfigState {
	case restoreSetConfigNone:
		if !isCopyTargetRelation {
			if (kind == restoreTokIdent || kind == restoreTokQuotedIdent) && value == "set_config" {
				s.setConfigState = restoreSetConfigExpectOpenParen
				s.setConfigUnicodeCall = false
			} else if kind == restoreTokUnicodeQuotedIdent {
				s.setConfigState = restoreSetConfigExpectOpenParen
				s.setConfigUnicodeCall = true
			}
		}
	case restoreSetConfigExpectOpenParen:
		if kind == restoreTokOther && value == "(" {
			if s.setConfigUnicodeCall {
				return errors.New("unicode-escaped function call is not allowed")
			}
			s.setConfigState = restoreSetConfigExpectFirstArg
			s.setConfigParen = s.parenDepth
		} else {
			s.setConfigState = restoreSetConfigNone
			s.setConfigUnicodeCall = false
		}
	case restoreSetConfigExpectFirstArg:
		if kind != restoreTokPlainString || truncated {
			return errors.New("dynamic set_config parameter name is not allowed")
		}
		if strings.TrimSpace(value) == "standard_conforming_strings" {
			return errors.New("modifying standard_conforming_strings via set_config is not allowed")
		}
		s.setConfigState = restoreSetConfigExpectComma
	case restoreSetConfigExpectComma:
		if kind == restoreTokOther && value == "," && s.parenDepth == s.setConfigParen {
			s.setConfigState = restoreSetConfigNone
		} else {
			return errors.New("computed set_config parameter name is not allowed")
		}
	}

	if s.stmtFirstWord == "copy" {
		switch s.copyPhase {
		case restoreCopyInit:
			if s.parenDepth == 0 && kind == restoreTokIdent {
				switch value {
				case "from":
					s.copyPhase = restoreCopyFrom
				case "to":
					s.copyPhase = restoreCopyTo
				}
			}
		case restoreCopyFrom:
			if kind == restoreTokIdent && value == "program" {
				return errors.New("COPY ... FROM PROGRAM is not allowed")
			}
			if kind == restoreTokIdent && value == "stdin" {
				s.copyPhase = restoreCopyFromStdin
			} else {
				return errors.New("COPY ... FROM source other than stdin is not allowed")
			}
		case restoreCopyTo:
			if kind == restoreTokIdent && value == "program" {
				return errors.New("COPY ... TO PROGRAM is not allowed")
			}
			return errors.New("COPY ... TO is not allowed")
		case restoreCopyFromStdin:
			return errors.New("unexpected clause after COPY ... FROM stdin is not allowed")
		}
	}

	return nil
}

func (s *restoreSQLScanner) validateStatementEnd() error {
	if s.stmtFirstWord == "copy" {
		if s.copyPhase != restoreCopyFromStdin || s.parenDepth != 0 {
			return errors.New("unsupported COPY statement is not allowed")
		}
	}
	if s.stmtFirstWord == "do" {
		return errors.New("anonymous DO block in backup SQL is not allowed")
	}
	if s.stmtFirstWord == "discard" {
		return errors.New("DISCARD statement in backup SQL is not allowed")
	}
	if s.stmtHasUnicodeIdent && (s.stmtFirstWord == "set" || s.stmtFirstWord == "alter" || s.stmtFirstWord == "reset") {
		return errors.New("unicode-escaped identifier in SET/ALTER/RESET is not allowed")
	}
	if s.stmtFirstWord == "reset" && len(s.stmtTokens) >= 2 {
		if s.stmtTokens[1] == "all" || s.stmtTokens[1] == "standard_conforming_strings" {
			return errors.New("RESET of standard_conforming_strings is not allowed")
		}
	}
	if s.stmtMentionsStdStrings && (s.stmtFirstWord == "set" || s.stmtFirstWord == "alter") {
		if !isSafeSetStandardConformingStringsOn(s.stmtTokens) {
			return errors.New("disabling standard_conforming_strings is not allowed")
		}
	}
	return nil
}

// isSafeSetStandardConformingStringsOn permits only the canonical pg_dump
// setting `SET [SESSION|LOCAL] standard_conforming_strings (=|TO) on`.
func isSafeSetStandardConformingStringsOn(tokens []string) bool {
	if len(tokens) < 4 || tokens[0] != "set" {
		return false
	}
	idx := 1
	if tokens[idx] == "session" || tokens[idx] == "local" {
		idx++
	}
	if len(tokens) != idx+3 {
		return false
	}
	if tokens[idx] != "standard_conforming_strings" {
		return false
	}
	if tokens[idx+1] != "=" && tokens[idx+1] != "to" {
		return false
	}
	switch tokens[idx+2] {
	case "on", "true", "1":
		return true
	default:
		return false
	}
}

func parseStandaloneRestrictCommand(line string) (cmd, key string, ok bool) {
	trimmed := strings.Trim(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), " \t")
	if !strings.HasPrefix(trimmed, `\`) {
		return "", "", false
	}
	rest := trimmed[1:]
	switch {
	case strings.HasPrefix(rest, "restrict"):
		cmd = "restrict"
		rest = rest[len("restrict"):]
	case strings.HasPrefix(rest, "unrestrict"):
		cmd = "unrestrict"
		rest = rest[len("unrestrict"):]
	default:
		return "", "", false
	}
	if len(rest) == 0 || (rest[0] != ' ' && rest[0] != '\t') {
		return "", "", false
	}
	key = strings.TrimLeft(rest, " \t")
	if len(key) == 0 || len(key) > 128 {
		return "", "", false
	}
	for i := 0; i < len(key); i++ {
		if !isRestrictKeyChar(key[i]) {
			return "", "", false
		}
	}
	return cmd, key, true
}

func isRestrictKeyChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

func extractPSQLMetaCommandName(s string) string {
	if len(s) < 2 || s[0] != '\\' {
		return ""
	}
	if s[1] == '!' || s[1] == '?' || s[1] == '\\' {
		return s[1:2]
	}
	end := 1
	for end < len(s) {
		b := s[end]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' {
			end++
		} else {
			break
		}
	}
	if end > 1 {
		return strings.ToLower(s[1:end])
	}
	return s[1:2]
}

func parseDollarQuoteTag(s string) (string, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	if s[1] == '$' {
		return "$$", true
	}
	if !isPGIdentStart(s[1]) {
		return "", false
	}
	for i := 2; i < len(s); i++ {
		if s[i] == '$' {
			return s[:i+1], true
		}
		if !isPGDollarTagCont(s[i]) {
			return "", false
		}
	}
	return "", false
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == '\v'
}

func isPGIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || b >= 0x80
}

func isPGIdentCont(b byte) bool {
	return isPGIdentStart(b) || (b >= '0' && b <= '9') || b == '$'
}

func isPGDollarTagCont(b byte) bool {
	return isPGIdentStart(b) || (b >= '0' && b <= '9')
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
