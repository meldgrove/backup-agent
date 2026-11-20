package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// dumpError is a failed pg_dump: its message comes from pg_dump's last
// error lines.
type dumpError struct{ message string }

func (e *dumpError) Error() string { return e.message }

// runPgDump writes the database to file with
// pg_dump --format=custom --compress=0 --dbname=DATABASE_URL. The password
// goes in PGPASSWORD instead of the URL, so it shows in no process list.
// schemaOnly dumps only the schema (the check command).
func runPgDump(ctx context.Context, bin, databaseURL, file string, schemaOnly bool) error {
	dbname, password := splitPassword(databaseURL)
	args := []string{"--format=custom", "--compress=0", "--no-password", "--file=" + file}
	if schemaOnly {
		args = append(args, "--schema-only")
	}
	args = append(args, "--dbname="+dbname)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = dumpEnvironment(os.Environ(), password)
	stderr := &tail{max: 16 << 10}
	cmd.Stderr = stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		detail := pgDumpError(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return &dumpError{message: "pg_dump failed: " + detail}
	}
	return nil
}

// splitPassword takes the password out of a PostgreSQL URL, from its user
// part or its password parameter. libpq URLs that Go's parser refuses (several
// hosts, such as postgresql://app:pw@db1:5433,db2/app) are split by hand, so
// that the password never reaches pg_dump's arguments.
func splitPassword(databaseURL string) (string, string) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return splitPasswordByHand(databaseURL)
	}
	password := ""
	if u.User != nil {
		if p, ok := u.User.Password(); ok {
			password = p
			u.User = url.User(u.User.Username())
		}
	}
	if query := u.Query(); query.Has("password") {
		if password == "" {
			password = query.Get("password")
		}
		query.Del("password")
		u.RawQuery = query.Encode()
	}
	return u.String(), password
}

// splitPasswordByHand removes the password from "scheme://user:password@hosts/…"
// and from a "password=" parameter, by position.
func splitPasswordByHand(databaseURL string) (string, string) {
	scheme, rest, ok := strings.Cut(databaseURL, "://")
	if !ok {
		return databaseURL, ""
	}
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	password := ""
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		user, secret, hasSecret := strings.Cut(authority[:at], ":")
		if hasSecret {
			password = unescape(secret)
			authority = user + authority[at:]
		}
	}
	if path, query, hasQuery := strings.Cut(tail, "?"); hasQuery {
		kept := []string{}
		for _, pair := range strings.Split(query, "&") {
			if name, value, _ := strings.Cut(pair, "="); name == "password" {
				if password == "" {
					password = unescape(value)
				}
				continue
			}
			kept = append(kept, pair)
		}
		tail = path
		if len(kept) > 0 {
			tail += "?" + strings.Join(kept, "&")
		}
	}
	return scheme + "://" + authority + tail, password
}

func unescape(value string) string {
	if decoded, err := url.QueryUnescape(value); err == nil {
		return decoded
	}
	return value
}

// dumpEnvironment is the agent's environment without its secrets, and with
// the database password as PGPASSWORD when the URL has one.
func dumpEnvironment(environment []string, password string) []string {
	var env []string
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name == "BACKUP_TOKEN" || name == "DATABASE_URL" || (password != "" && name == "PGPASSWORD") {
			continue
		}
		env = append(env, entry)
	}
	if password != "" {
		env = append(env, "PGPASSWORD="+password)
	}
	return env
}

// pgDumpError is the last "pg_dump: error:" line of stderr with the detail
// and hint lines after it, or else the last line.
func pgDumpError(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	start := len(lines) - 1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "pg_dump: error:") {
			start = i
			break
		}
	}
	var parts []string
	for _, line := range lines[start:] {
		line = strings.TrimPrefix(line, "pg_dump: ")
		line = strings.TrimPrefix(line, "error: ")
		parts = append(parts, line)
	}
	return strings.Join(parts, "; ")
}

// tail keeps the last max bytes written to it.
type tail struct {
	max  int
	data []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.data = append(t.data, p...)
	if len(t.data) > t.max {
		t.data = append([]byte(nil), t.data[len(t.data)-t.max:]...)
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.data) }

// dumpServerVersion reads the version of the database server, such as
// "18.1", from the header of a pg_dump archive in custom format; empty when
// it cannot.
func dumpServerVersion(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	version, _ := parseDumpHeader(io.LimitReader(f, 64<<10))
	return version
}

var serverVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*[A-Za-z0-9]*$`)

// parseDumpHeader follows ReadHead in PostgreSQL's pg_backup_archiver.c:
// "PGDMP", the archive version (1.10 and newer store the server version),
// int and offset sizes, the format, the compression, the creation time,
// the database name, then the server's version string.
func parseDumpHeader(r io.Reader) (string, error) {
	in := bufio.NewReader(r)
	fail := errors.New("not a pg_dump archive in custom format of version 1.10 or newer")
	magic := make([]byte, 5)
	if _, err := io.ReadFull(in, magic); err != nil || string(magic) != "PGDMP" {
		return "", fail
	}
	b := make([]byte, 6)
	if _, err := io.ReadFull(in, b); err != nil {
		return "", fail
	}
	major, minor, intSize := int(b[0]), int(b[1]), int(b[3])
	version := major*100 + minor // b[2] is the revision; b[4] the offset size
	if version < 110 || intSize < 1 || intSize > 8 || b[5] != 1 {
		return "", fail // b[5]: 1 is the custom format
	}
	readInt := func() (int64, error) {
		buf := make([]byte, 1+intSize)
		if _, err := io.ReadFull(in, buf); err != nil {
			return 0, err
		}
		padded := make([]byte, 8)
		copy(padded, buf[1:])
		n := int64(binary.LittleEndian.Uint64(padded))
		if buf[0] != 0 {
			n = -n
		}
		return n, nil
	}
	readString := func() (string, error) {
		n, err := readInt()
		if err != nil || n > 4096 {
			return "", fail
		}
		if n < 0 {
			return "", nil
		}
		s := make([]byte, n)
		_, err = io.ReadFull(in, s)
		return string(s), err
	}
	if version >= 115 {
		if _, err := in.ReadByte(); err != nil { // compression algorithm
			return "", fail
		}
	} else if _, err := readInt(); err != nil { // compression level
		return "", fail
	}
	for range 7 { // creation time: seconds … isdst
		if _, err := readInt(); err != nil {
			return "", fail
		}
	}
	if _, err := readString(); err != nil { // database name
		return "", fail
	}
	remote, err := readString()
	if err != nil {
		return "", fail
	}
	fields := strings.Fields(remote) // "18.1 (Debian 18.1-1.pgdg13+2)"
	if len(fields) == 0 || !serverVersionPattern.MatchString(fields[0]) {
		return "", fail
	}
	return fields[0], nil
}
