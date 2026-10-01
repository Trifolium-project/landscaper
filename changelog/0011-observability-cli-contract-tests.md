# 0011 - Command-level observability contracts

Follow-up to proposal 0070 and changelog/0010, implemented on 2026-10-01.

The new subprocess fixture executes the complete Cobra CLI, including config
initialization, flags, audit logging, command handlers, and actual process exits.
Each child has fresh command globals, temporary config/work directories, fake
credentials, and a local TLS tenant. It does not read the developer's .env or
user configuration and makes no live SAP calls. Only the test child trusts the
local server's test certificate.

23 command scenarios cover:

- artifact get JSON output with both --output=json and --output json;
- package copy, already-existing packages, and missing Discover packages;
- package delete dry-run output and progress;
- guideline violations and their non-zero exit;
- bulk artifact download with one successful, failed, and existing item;
- standalone deploy and upload --deploy --wait with STARTED, ERROR, and timeout;
- default human output and progress with audit logging disabled;
- root/subcommand help listing log-file, run-id, progress, and log-dir.

The tests check exact nested log paths, no extra log directory, JSON-only
stdout, stderr audit notices, correlation on every audit record, run-ID flag
precedence over environment, HTTP durations/attempts, stable download totals,
progress without response bodies/credentials, and summary/end records even
after a fatal download exit. Summary counts and HTTP totals match the records,
and progress outcomes match the item summary.

The shared ItemOutcome helper now handles positive guideline violation counts
after JSON decoding as float64 or json.Number, as well as native integer values.
Before this fix, re-reading a guideline item could classify it as successful
even though the writer's in-memory integer count correctly classified it as failed.
Regression checks cover zero and positive counts with both JSON decoding modes.

Verification: all six Go package suites pass on Windows; build and vet pass.
git diff --check passes. The rebuilt local executable is in the sibling
integration-flow-generation/runs/_tools folder and is not a release.

Follow-up Linux verification: all six package suites, vet, and build pass in
golang:1.26.8 with the current source mounted read-only. POSIX mode assertions
and the 23 CLI scenarios run on Linux. The temporary container is removed;
no Linux build output is added to the repository.

Still open: release tag/version work. These mocked command
checks do not replace live tenant verification. Go-native OTLP stays deferred.
No release tag, commit, or push was made.
