// Meldgrove Backup Agent: a sidecar of a self-hosted Meldgrove app. It asks
// Meldgrove Backups over HTTPS for jobs and, for each, sends one tar archive
// of the app's database (pg_dump) and its volumes, in parts. It opens no
// port and restores nothing. See README.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = `Usage: backup-agent [command]
  (none)    ask Meldgrove Backups for jobs and run them
  check     check the settings, the volumes, pg_dump, the database and Backups
  version   print the agent's version`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	switch {
	case len(args) > 1:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	case command == "version":
		fmt.Println(version())
		return 0
	case command == "-h", command == "--help", command == "help":
		fmt.Println(usage)
		return 0
	case command != "" && command != "check":
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}

	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		newLogger(os.Stderr, 0, cfg.Secrets()).Error(err.Error())
		return 1
	}
	log := newLogger(os.Stderr, cfg.LogLevel, cfg.Secrets())
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	agent := NewAgent(cfg, log)
	if command == "check" {
		return agent.Check(ctx, os.Stdout)
	}
	return agent.Run(ctx)
}
