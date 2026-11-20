package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Check tests what a backup needs and prints one line per item: the
// volumes, the temporary folder, pg_dump, the database and Backups. It
// returns 1 when anything is wrong. It runs no job: it polls with
// "check": true, and fails a job that a Backups hands out anyway.
func (a *Agent) Check(ctx context.Context, out io.Writer) int {
	problems := 0
	line := func(label, text string) {
		if label == "problem" {
			problems++
		}
		fmt.Fprintf(out, "%-8s %s\n", label, redactSecrets(text, a.secrets))
	}
	f := fmt.Sprintf
	fmt.Fprintf(out, "Meldgrove Backup Agent %s\n", a.cfg.Version)
	line("ok", "The settings are valid.")

	if volumes, err := listVolumes(a.cfg.DataDir); err != nil {
		line("problem", f("Volumes: %v", err))
	} else if len(volumes) == 0 {
		line("note", f("Volumes: none in %s; the backups hold no files.", a.cfg.DataDir))
	} else {
		line("ok", f("Volumes in %s: %s", a.cfg.DataDir, strings.Join(volumes, ", ")))
	}

	tmpOK := false
	if file, err := os.CreateTemp(a.cfg.TmpDir, "backup-check-*"); err != nil {
		line("problem", f("Temporary folder %s: not writable: %v", a.cfg.TmpDir, err))
	} else {
		file.Close()
		os.Remove(file.Name())
		tmpOK = true
		var stat syscall.Statfs_t
		if syscall.Statfs(a.cfg.TmpDir, &stat) == nil {
			free := float64(int64(stat.Bavail)*int64(stat.Bsize)) / 1e9
			line("ok", f("Temporary folder %s: writable, %.1f GB free; it needs room for one dump.", a.cfg.TmpDir, free))
		} else {
			line("ok", f("Temporary folder %s: writable.", a.cfg.TmpDir))
		}
	}

	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	output, err := exec.CommandContext(versionCtx, a.pgDump, "--version").Output()
	cancel()
	pgDumpOK := err == nil
	if pgDumpOK {
		line("ok", strings.TrimSpace(string(output)))
	} else {
		line("problem", f("pg_dump does not run: %v", err))
	}

	switch {
	case a.cfg.DatabaseURL == "":
		line("note", "Database: none, DATABASE_URL is empty; the backups hold no database.")
	case !tmpOK || !pgDumpOK:
		line("problem", "Database: not checked, pg_dump or the temporary folder does not work.")
	default:
		line(a.checkDatabase(ctx))
	}

	if a.cfg.URL == "" {
		line("note", "Backups: BACKUP_URL is empty; the agent stays idle.")
	} else {
		line(a.checkBackups(ctx))
	}
	if problems > 0 {
		return 1
	}
	return 0
}

// checkDatabase dumps only the schema, into the temporary folder.
func (a *Agent) checkDatabase(ctx context.Context) (string, string) {
	file, err := os.CreateTemp(a.cfg.TmpDir, "backup-check-*.dump")
	if err != nil {
		return "problem", fmt.Sprintf("Database: %v", err)
	}
	file.Close()
	defer os.Remove(file.Name())
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := runPgDump(ctx, a.pgDump, a.cfg.DatabaseURL, file.Name(), true); err != nil {
		return "problem", "Database: " + failMessage(err.Error(), a.secrets)
	}
	if version := dumpServerVersion(file.Name()); version != "" {
		return "ok", fmt.Sprintf("Database: answers (PostgreSQL %s).", version)
	}
	return "ok", "Database: answers."
}

// checkBackups polls once.
func (a *Agent) checkBackups(ctx context.Context) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	answer, err := a.client.Poll(ctx, a.pollRequest(true))
	if err != nil {
		return "problem", fmt.Sprintf("Backups at %s: %v", a.cfg.URL, err)
	}
	text := fmt.Sprintf("Backups: answers, as the source %q; the agent asks every %d s.", answer.Source.Name, answer.PollSeconds)
	if answer.Job != nil {
		// A Backups that does not know "check" handed out a job; the next
		// planned run backs up.
		_ = a.client.Fail(ctx, answer.Job.ID, "The check command of the agent took this job; it runs no backups.")
		text += " It handed out a job, which the check reported as failed."
	}
	return "ok", text
}
