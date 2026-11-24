# AGENTS.md

Meldgrove Backup Agent is the sidecar that backs up one Meldgrove app for
Meldgrove Backups (`meldgrove/backups`). When Backups hands out a job, it
streams a tar archive of the app's `pg_dump` and its volumes to Backups, in
parts, over HTTPS. `README.md` is the user documentation and the public
contract of the protocol.

## Layout

```
main.go         commands: run (no argument), check, dump-database, version
config.go       the settings (environment), the volumes below BACKUP_DATA_DIR
agent.go        the poll loop; a job from pg_dump to /complete or /fail
client.go       the protocol over HTTP: poll, parts with retries, progress, complete, fail
archive.go      the tar stream: manifest.json, db.dump, volumes/<name>/…
parts.go        cuts the stream into parts, with SHA-256 per part and in total
dump.go         runs pg_dump; its error lines; the server version from the dump
                header; the dump-database command (pg_dump to stdout)
check.go        the check command
redact.go       keeps secrets out of logs and failure messages; the logger
version.go      the version (APP_VERSION of the image) and the User-Agent
*_test.go       unit tests; fake_test.go is a fake Backups (httptest)
test/run.sh     end to end: the image, PostgreSQL 18, test/fake_backups.py
```

## Rules

- **The protocol is a contract.** A change goes into `README.md`
  ("Protocol"), this agent and `meldgrove/backups` together.
- **No secrets in logs or messages:** never `BACKUP_TOKEN`, `DATABASE_URL`
  or a password. Log only through the logger of `newLogger`; messages for
  `/fail` pass `failMessage`. The password reaches `pg_dump` in
  `PGPASSWORD`, never in its arguments.
- **The agent only reads.** Volumes are mounted read-only; no restore, no
  port, no program but `pg_dump`.
- **Stream.** Never load a whole file or the whole stream; one part is in
  memory.
- **Standard library only**, one `package main`. Go 1.27.
- Change the tests with the behaviour; `test/run.sh` covers what needs the
  image.

## Commands

```sh
go test ./...                     # Go 1.27, or:
docker run --rm -v "$PWD":/src -w /src golang:1.27-alpine go test ./...
gofmt -l . && go vet ./...
test/run.sh                       # end to end (Docker, Python 3, jq)
```

Releases: Actions → Release → Run workflow (shared Meldgrove workflow; see
`README.md`). No file holds the version.

## Writing

Concise, plain US English; short sentences. The license is not chosen yet:
add no license file or text.
