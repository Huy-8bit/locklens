# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through the repository host's private
vulnerability reporting feature (for GitHub: the "Report a vulnerability"
button under the Security tab), not in public issues. Include the LockLens
version (`locklens version`), the PostgreSQL version, and steps to reproduce.

There is no formal response-time commitment; reports are handled on a best
effort basis by the maintainers. Fixes are released for the latest version
only.

## Security model

LockLens is a read-only observer that runs on an operator's machine and
connects to a PostgreSQL server with credentials the operator provides.

What LockLens does to protect you:

- **Read-only.** Connections set `default_transaction_read_only=on`, every
  collection runs in a `READ ONLY` transaction, and there is no code to cancel,
  terminate, or modify anything. Demo code that writes lives in `demo/` and is
  checked not to be imported by the CLI.
- **Credentials.** DSNs and passwords are never written to stdout, stderr,
  logs, or snapshot files. Connection strings that cannot be parsed are never
  echoed, because a malformed string cannot be redacted reliably. Other errors
  pass through a redactor that replaces the exact DSN and password values,
  including URL-encoded forms, rather than relying on pattern matching alone.
- **Query text is opt-in** (`--include-query`) and length-limited.
- **Terminal safety.** Strings chosen by database clients, such as
  `application_name` and query text, are stripped of control characters and
  bidirectional overrides before being printed, so they cannot inject terminal
  escape sequences.
- **TLS** behaviour follows the DSN and libpq conventions; LockLens never
  disables or weakens it.
- **Snapshot files** are written atomically with mode `0600`.
- **Bounded load.** Small connection pool, server-side `statement_timeout` and
  `lock_timeout`, a minimum `watch` interval, and `pg_blocking_pids()` called
  only for waiting backends.

What you are responsible for:

- Prefer `LOCKLENS_DSN`, `PGPASSWORD`, or `~/.pgpass` over `--dsn`: command-line
  arguments are visible in the process list.
- Use the least privileged role that meets your needs: a login role, plus
  `pg_read_all_stats` for complete reports. Never a superuser just for LockLens.
- Treat snapshot files and `--include-query` output as sensitive: they contain
  user, database, and application names, client addresses, and possibly SQL.
- Use `sslmode=verify-full` (with `sslrootcert`) when connecting over untrusted
  networks.

Known limitations relevant to security are listed in
[docs/limitations.md](docs/limitations.md#security-considerations).
