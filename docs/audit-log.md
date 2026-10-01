# Audit log schema

Use `--log` to write one JSON object per line. `--log-file <path>` chooses an exact path; otherwise
`--log-dir <dir>` creates a timestamped file. A log can contain tenant response bodies and is
written with restrictive file permissions where the operating system supports them.

The first record is `{"type":"run","phase":"start","schema":1,...}`. Schema 1 keeps the
existing fields and adds fields without renaming them. Every record has `type` and UTC `ts`.
When `--run-id` or `LANDSCAPER_RUN_ID` is set, every record also has `run_id`. When a valid
W3C `TRACEPARENT` is set, every record has `trace_id` and `parent_span_id` from that
context. The caller's parent span is recorded, not a span created by landscaper.

| Type | Fields |
|---|---|
| `run` start | `phase`, `schema`, `command`, `env`, redacted `flags` |
| `http` | `method`, `url`, `duration_ms`, `attempt`, request/response headers and bodies, `status` or `error` |
| `item` | Command-specific artifact/package identifier, operation, status, path, and failure flags |
| `log` | `level`, `msg` |
| `summary` | `items.{ok,failed,skipped}`, `http.{calls,failed,total_ms}`, `duration_ms`, `exit_code` |
| `run` end | `phase`, `status`, optional `detail`, `duration_ms` |

The `summary` record immediately precedes the `run` end record, including when a command
uses an explicit nonzero exit code. Abrupt process termination may leave a start record
without either final record. Consumers should tolerate unknown additive fields and an
incomplete final line.

`--progress=json` emits one NDJSON record on stderr for each completed item. Its fields
are `type=progress`, `ts`, `run_id`, `command`, `done`, `total`, `item`,
`status` (`ok`, `failed`, or `skipped`) and `bytes`. Artifact download discovers
the selected artifact metadata first, so `total` stays fixed while downloads proceed.
The progress stream contains no response bodies or credentials. JSON command output
on stdout remains a single JSON document; the audit-log path notice goes to stderr.

Progress and summary records use the same item outcome rules. Explicit failures,
deployment failures, missing items, blocked package deletes, and guideline violations
count as failed. Existing, cancelled, planned, dry-run, and unsupported items count as
skipped. Other completed items count as ok. Guideline warnings without violations
do not count as failures. Standalone deployment also writes an item record.
Package-only progress uses the package ID, not an empty artifact placeholder.

Outside bulk artifact download, the total can grow as item records are produced.
Only bulk artifact download promises a fixed total known before the first item.
On Windows, protect the log directory with appropriate ACLs; POSIX permission
bits do not establish Windows access controls.
