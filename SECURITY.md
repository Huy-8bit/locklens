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

LockLens is a read-only observer. The CLI runs on an operator's machine; the
server (`locklens server`) runs as a single-administrator web application that
stores encrypted connection strings and collected data in its own PostgreSQL
database. The full description is in [docs/security.md](docs/security.md).

In short:

- **Read-only.** Connections set `default_transaction_read_only=on`, every
  collection runs in a `READ ONLY` transaction with statement, lock, and idle
  timeouts, and there is no code to cancel, terminate, or modify anything on a
  monitored server. Code that writes (demo and lab) lives in `demo/` and is
  checked not to be imported by `locklens`.
- **Credentials.** DSNs are encrypted at rest with AES-256-GCM and a master key
  from the environment, never returned by the API, and never written to logs,
  exports, snapshots, or metric labels. Errors pass through a redactor that
  removes exact secret values; unparsable DSNs are never echoed.
- **Web application.** Loopback bind by default, bcrypt-hashed administrator
  password, server-side sessions in HttpOnly `SameSite=Strict` cookies, CSRF
  tokens plus Origin checks, a strict Content Security Policy, request size and
  range limits, and generic error messages.
- **SQL text** is opt-in in the CLI (`--include-query`); the server collects
  it by default per target (it can be switched off) and excludes it from
  exports unless explicitly requested. Table rows are readable only through a
  separate, opt-in, SELECT-only role.
- **Output safety.** Strings chosen by database clients are neutralised before
  they reach a terminal and escaped in HTML.

What you are responsible for:

- Keep the server on loopback or behind TLS (set `LOCKLENS_COOKIE_SECURE=true`).
- Generate a real `LOCKLENS_MASTER_KEY` and keep it out of version control and
  separate from store backups.
- Use least-privileged monitoring roles (`pg_read_all_stats`, never a
  superuser) and TLS (`sslmode=verify-full`) over untrusted networks.
- Prefer `LOCKLENS_DSN`, `PGPASSWORD`, or `~/.pgpass` over `--dsn`: command-line
  arguments are visible in the process list.
- Treat snapshot files, exports, and store backups as sensitive operational
  data.
- Never reuse the Docker lab's fake credentials and key.

Known limitations relevant to security are listed in
[docs/limitations.md](docs/limitations.md#security-considerations).
