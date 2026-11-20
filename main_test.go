package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWrongSettingsLeaveTheAgentIdle(t *testing.T) {
	f := newFakeBackups(t)
	env := map[string]string{"BACKUP_URL": f.server.URL, "BACKUP_TOKEN": testToken, "BACKUP_DATA_DIR": t.TempDir(),
		"DATABASE_URL": "mysql://app:secret-password@db/app"}
	getenv := func(name string) string { return env[name] }
	logs := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() { done <- run(ctx, nil, getenv, io.Discard, logs) }()
	select {
	case code := <-done:
		t.Fatalf("the agent exited with %d on a wrong setting:\n%s", code, logs)
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d after the stop, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the idle agent did not stop")
	}
	got := logs.String()
	if strings.Count(got, "level=ERROR") != 1 || strings.Count(got, "\n") != 1 || !strings.Contains(got, "DATABASE_URL must start with") ||
		!strings.Contains(got, "The agent is idle") || strings.Contains(got, "secret-password") {
		t.Errorf("log:\n%s", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.polls) != 0 {
		t.Errorf("the agent polled with a wrong setting: %d polls", len(f.polls))
	}

	// check explains the setting and exits with 1 at once.
	logs = &lockedBuffer{}
	var out strings.Builder
	if code := run(context.Background(), []string{"check"}, getenv, &out, logs); code != 1 {
		t.Errorf("check exited with %d, want 1", code)
	}
	if !strings.Contains(logs.String(), "DATABASE_URL must start with") || strings.Contains(logs.String(), "secret-password") || out.Len() != 0 {
		t.Errorf("check printed %q and logged:\n%s", out.String(), logs)
	}
}

func TestVersionAndUsage(t *testing.T) {
	var out, errOut strings.Builder
	getenv := func(string) string { return "" }
	if code := run(context.Background(), []string{"version"}, getenv, &out, &errOut); code != 0 || out.String() != version()+"\n" {
		t.Errorf("version: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"help"}, getenv, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "Usage:") {
		t.Errorf("help: exit %d, %q", code, out.String())
	}
	if code := run(context.Background(), []string{"backup"}, getenv, &out, &errOut); code != 2 || !strings.HasPrefix(errOut.String(), "Usage:") {
		t.Errorf("an unknown command: exit %d, %q", code, errOut.String())
	}
}
