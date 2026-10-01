# Audit observability

Added `--run-id` / `LANDSCAPER_RUN_ID` and W3C `TRACEPARENT` correlation to every
audit record, an exact `--log-file`, an end-of-run summary, and JSON progress on
stderr. HTTP and run durations already existed on develop and remain part of the
versioned schema. See [the schema](../docs/audit-log.md).

The new fields are additive. The normal human output and exit codes are unchanged;
the audit-log path notice is moved to stderr for `--output=json`.
