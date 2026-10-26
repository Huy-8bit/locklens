# Notifications

LockLens sends incidents to Slack, to webhooks, and by email, so nobody has to
keep the UI open to learn that a database is blocked or slow.

## Channels

Administrators add channels under **Settings → Notifications**.

| Type | Destination | Notes |
| --- | --- | --- |
| **Slack** | An incoming webhook URL (`https://hooks.slack.com/services/…`) | A message with the title, severity, instance, rule, times, and up to three observations; the title links to the incident |
| **Webhook** | Any `http` or `https` URL | A JSON `POST` (below), optionally signed |
| **Email** | An SMTP server, sender, and up to 20 recipients | STARTTLS (587) or TLS (465); without TLS only for a local relay, and never with a password |

For each channel:

- **Minimum severity**: info, warning (default), or critical.
- **Environments**: only incidents of instances in these environments
  (empty: every environment).
- **Events**: an incident opens (or opens again after it resolved), becomes
  more severe, or is resolved; and optionally a **reminder** every 30 minutes
  to every day while it stays open and nobody acknowledged it.
- **Enabled**, and **Send test** to check the destination.

The URL, signing secret, SMTP password, and recipients are encrypted with the
master key. The API never returns them: a channel shows only the scheme and
host of its URL, or its recipients and mail server. Editing a channel keeps
the destination unless it is replaced.

## What is sent

The incident's title, severity, state, instance and environment, rule, when
it opened (and resolved, with the resolution), how long it lasted, up to three
observations from its findings, and a link when `LOCKLENS_PUBLIC_URL` is set.
Observations are evidence, not a confirmed cause, and the messages say so.

Messages **never** contain SQL text, connection strings, or credentials.
Strings that come from monitored servers are reduced to one line without
control characters.

### Webhook body

```json
{
  "version": 1,
  "source": "locklens",
  "event": "opened",
  "incident": {
    "event": "opened",
    "incident_id": 42,
    "title": "Blocking chain rooted at pid 254 persists (2 blocked)",
    "severity": "warning",
    "state": "open",
    "instance": "orders-db",
    "environment": "production",
    "rule_id": "blocking.long_chain",
    "opened_at": "2026-10-25T13:33:39Z",
    "duration": "2m10s",
    "observed": ["pid 254 blocked 2 session(s) for 30 s across 3 samples."],
    "url": "https://locklens.example.com/incidents/42"
  },
  "sent_at": "2026-10-25T13:35:49Z"
}
```

`event` is `opened`, `escalated`, `resolved`, `reminder`, or `test`. The
`X-LockLens-Event` header repeats it. With a signing secret,
`X-LockLens-Signature: sha256=<hex>` is the HMAC-SHA256 of the raw body with
that secret; compare it in constant time. Fields are only ever added to
version 1.

## Delivery

- Decisions are made from the incident's current state and the channel's
  delivery history, so each event is sent once, also after a restart or
  when updates arrive close together.
- A failed delivery is retried twice within the same attempt (after 2 s and
  10 s) on network errors, HTTP 429, and 5xx; the event is tried again 5
  minutes later while the incident still calls for it.
- Redirects are not followed. Destinations on link-local, multicast, or
  unspecified addresses (for example a cloud metadata service) are refused;
  private networks are allowed.
- Every attempt is recorded (sent, failed with its error, or silenced) and
  shown under **Recent deliveries** and on the incident page. Errors never
  contain the destination URL.
- Imported incidents are never sent.

## Silences

A silence stops notifications for one instance, one rule, or both, from now
for a chosen time (at most 30 days); incidents are still recorded. Operators
and administrators create silences on the incident page ("Silence this rule on
this instance for 1 hour / 4 hours / 1 day") or under **Settings → Notifications** (Silences),
and can end them early. An event that falls in a silence is recorded once as
silenced and is not sent later; reminders are skipped while silenced.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `LOCKLENS_PUBLIC_URL` | (none) | The address users open LockLens at, for links in messages. The Compose file sets `http://127.0.0.1:$LOCKLENS_HTTP_PORT`. |

Delivery history and ended silences are kept for `LOCKLENS_FINDING_RETENTION`.
