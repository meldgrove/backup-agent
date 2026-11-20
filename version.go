package main

import "os"

// version is the agent's release, such as 0.1.0: the Release workflow builds
// the image with APP_VERSION (Dockerfile). Outside a release it is
// "development".
func version() string { return orDefault(os.Getenv("APP_VERSION"), "development") }

// userAgent names the agent and its version in every request to Backups.
func userAgent(version string) string { return "meldgrove-backup-agent/" + version }
