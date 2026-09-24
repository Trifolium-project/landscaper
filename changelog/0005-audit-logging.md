# Audit log of a run and of every call to the tenant (`--log`)

Branch: `feature/logging`

## Context

landscaper changes production integration tenants — it uploads flows, moves
packages between environments, rewrites configuration and deploys. Nothing was
recorded. Each command printed a `text/tabwriter` table to stdout and the tenant's
own error bodies survived only as the text of a `log.Fatalln` message, so once the
terminal scrolled there was no way to answer "what did this run actually do".

There was no file logging anywhere in the repository. `CPIClient.VerboseLog`
(`cpiclient.go:46`) looked like the seed of one, but it was dead code: the only two
constructor calls, `landscape.go:270` and `artifactUpload_test.go:305`, hardcoded
`false` and no flag set it. It was also unusable as written — see below.

This adds an opt-in JSON Lines audit trail: the command and its parameters, every
request to the tenant with the tenant's answer, the outcome of each artifact, and
how the run ended.

## Decisions (confirmed with user)

| Question | Decision |
|---|---|
| Format | **JSON Lines**, one object per line. The tenant's OData answers are already JSON, so they nest instead of being flattened into prose |
| Layout | **One file per run**, `logs/landscaper-YYYYMMDD-HHMMSS.log`. No rotation, no unbounded growth, one run is one self-contained artifact |
| Default | **Off.** `--log` enables it, `--log-dir` relocates it (default `logs`) |
| Response detail | **Full text bodies**, untruncated — an OData error message is the thing you need when a transport fails |
| Never written | Credentials, and binary or base64 archive payloads, regardless of the above |

Three rules follow from the code rather than from preference:

**Redaction is mandatory, and the existing verbose path violated it.** `setAuth`
runs at `cpiclient.go:168`, *before* the `log.Println(req)` at `:171`, so
`VerboseLog` printed the fully populated `Authorization` header. With Basic
authentication that header is `base64(user:password)` — reversible, so it is the
tenant password in all but name. Teeing the standard logger into a file (below)
would have promoted a latent stderr leak into a persisted one, so **both
`VerboseLog` blocks were deleted** as part of this change.

**The logger must be unbuffered.** `log.Fatalln` is the universal exit path — 59
sites in `packages/cmd` — and it calls `os.Exit`, which runs no deferred function
and flushes no `bufio.Writer`. A buffered logger would lose precisely the records
describing the failure.

**Failure must be recorded, not inferred.** Without something written before
`os.Exit`, a fatal error and a `kill -9` are indistinguishable: both are a run with
a missing end record. That ambiguity is unacceptable in an audit log.

## Files

### New: `packages/auditlog/auditlog.go`

A leaf package with no project imports, at the same level as `packages/iflow`, so
`cpiclient`, `landscape` and `cmd` can all use it. It deliberately does **not**
import `log`: the standard logger holds its own mutex across the write it makes to
the tee, so logging from inside a write would deadlock the process rather than
recurse.

 - `New(dir)` creates the directory and opens the file `O_CREATE|O_WRONLY|O_APPEND` with mode `0600`. Append matters because the name has second granularity — two runs started in the same second share a name, and without `O_APPEND` the second would truncate the first. The mode matters because the file holds complete tenant responses.
 - **Every method is nil-receiver safe.** A disabled logger *is* a nil `*Logger`, which is what lets each call site be a plain method call with no "if logging is on" guard, and what keeps the existing tests working when `initConfig` never runs.
 - `write` marshals one record per line and loops until the whole line is out. `os.File.Write` does not loop, and a record carrying a full response body is large enough for a short write to matter; a partial line would corrupt the stream for every later reader. A write failure is reported once, straight to stderr with `fmt.Fprintf`, never through `log`.
 - `RunStart` / `RunEnd` / `Item` / `Message` write the four record types. `RunEnd` is guarded so only the first call is written, so a fatal spotted by the tee cannot race a command's normal completion.
 - `StartHTTP` / `SetResponse` / `SetError` / `Flush` accumulate one call. `HTTPCall` is also nil-safe.
 - `RedactHeaders` replaces the value of `Authorization`, `X-Csrf-Token`, `Cookie` and `Set-Cookie`, **keeping the key** — an auditor needs to see that authentication was sent. `RedactFlags` does the same for parameter names suggesting a credential; no command defines one today, this is so that adding one is not a leak.
 - `CaptureRequestBody` reads the body through `req.GetBody()`, which `http.NewRequest` populates for the `bytes.Buffer` bodies this client uses. It scans only the first 4 KB for `"ArtifactContent"` and, on finding it, records the prefix plus a redaction marker. `json.Marshal` emits struct fields in declaration order and the `json:"-"` tags leave `{Id, PackageId, Name, Description, ArtifactContent}`, so the key always lands within a few hundred bytes and the multi-megabyte tail is never read.
 - `isTextual` trusts a declared media type and falls back to `utf8.Valid` plus a NUL-byte check when there is none. The `$value` download returns a zip through the same code path as every JSON reply.
 - `LogWriter` returns the writer for `log.SetOutput`. It splits on newlines and emits one record per line rather than assuming one `Write` is one record, and it **swallows write errors on purpose**: `io.MultiWriter` abandons the remaining writers after the first failure, so a closed stdout (`landscaper ... | head`) would otherwise cost the audit record.
 - `fatalOnStack` walks `runtime.Callers` for a `log.Fatal*` frame. When the standard logger is on its way to `os.Exit`, the tee writes the message *and* the `run` end record while the process is still alive.

### `packages/cpiclient/cpiclient.go`

A `Logger` field and `SetLogger`, rather than a constructor change: a nil logger is
the zero value, so `landscape.go:270` and `artifactUpload_test.go:305` compile
untouched, and logging is cross-cutting rather than part of how to authenticate.

`doRequest` is the single choke point — `s.Client.Do` appears exactly once, at
`:174`. The record is opened before the call and flushed from a `defer`, so it
survives the `log.Fatal` at `:190` and the panic that `getCSRFToken` raises on any
non-2xx token fetch. `SetResponse` captures `resp.StatusCode`, which nothing else
preserves: a non-2xx returns `fmt.Errorf("%s", body)` and the code is lost.

Both `VerboseLog` dump blocks were removed, for the reason given above.

### `packages/landscape/landscape.go`

`SetLogger` walks `Systems` and sets the logger on each `System.Client`.
`Environment.System` points at the same `*System` values, so one walk reaches every
client a command can use — including the two clients `packageMove` and
`artifactUpload` use in a single run.

### `packages/cmd/root.go`

 - `--log` and `--log-dir` as persistent flags, pointer style like `environment` and `pkg`.
 - `startAuditLog` runs from `initConfig` **between `godotenv.Load()` and `landscape.NewLandscape`** — the only window before the clients are built. Failure is `log.Fatalln`: the user asked for a record of what this run would do to a tenant, and performing the operation without one is worse than not performing it. That placement also captures `NewLandscape`'s own errors and guarantees no request happens unlogged.
 - `log.SetOutput(auditLogger.LogWriter(os.Stderr))` records all 59 `log.Fatalln` sites, plus `cpiclient.go:190` and `:233`, **with no per-command changes**, while leaving today's stderr output byte for byte unchanged.
 - `recordRunStart` runs from `rootCmd.PersistentPreRun`, not `initConfig`, which has no access to the command or to `Flags().Visit`. Only flags that were actually set are recorded.
 - `Execute` closes the run: `ok`, `failed` on a returned error, or `panic` from a `recover` that re-panics afterwards. A panic unwinds, unlike `os.Exit`.

`initConfig` does not run for `--help`, for an unknown flag or for an
arg-validation failure, so none of those create a log file.

### `artifactDownload.go`, `artifactUpload.go`, `packageMove.go`, `configUpdate.go`

`item` records for the commands that change a tenant or produce per-item outcomes,
reusing each command's existing status vocabulary verbatim so the log and the
printed table cannot disagree. Read-only commands need none — their `http` records
already say everything.

In `configUpdate.go` the record is written around the call whose error the command
**ignores** (`:136`), so a rejected parameter becomes visible for the first time.

## Verification

```bash
go build ./... && go vet ./... && go test ./packages/...
```

`packages/auditlog/auditlog_test.go` covers redaction of all four header names
(key kept, value replaced), flag redaction, `ArtifactContent` omission with the
2 MB tail left unread, ordinary JSON bodies recorded in full, `isTextual` across
eight media-type cases including an undeclared zip, full OData error bodies,
binary responses reduced to a size, a nil `*Logger` accepting every method, one
valid JSON object per line even for a message containing newlines, `RunEnd`
written once, `0600` permissions, a second logger not truncating the first, and
`New` rejecting an unusable directory.

One test was wrong before it was right: it built the upload payload with
`map[string]string`, whose keys `json.Marshal` sorts alphabetically, putting
`ArtifactContent` first — the opposite of the struct declaration order the client
actually sends. It now marshals a struct, and a second case asserts the payload is
omitted even if it were to arrive first.

`packages/cmd/auditLog_test.go` drives the real client against the existing
`stubTenant`, which is the only place credentials actually flow. **The assertion
the feature stands on reads the raw file bytes, not parsed records**, so a
credential arriving through a path nobody modelled is still caught: the sentinel
password, its Basic header encoding and the CSRF token must all be absent while
the string `Authorization` is present. Also covered: `http` records carrying a
status code, the downloaded archive never appearing (no `PK\x03\x04` in the file)
and being marked `binary` with a size, and a nil logger leaving the download
working and writing nothing.

Mutation checked: removing the redaction branch from `RedactHeaders` fails
`TestAuditLogNeverContainsCredentials` with "the Basic header value leaked into
the audit log", and fails `TestRedactHeaders` on four headers.

Manually, against the tenant of `conf/landscape.yaml`:

```bash
./landscaper artifact download --packages TestHarnessPreparation --output /tmp/dl --force --log
jq -r '.type' logs/landscaper-*.log | sort | uniq -c     # 7 http, 6 item, 2 run
```

A deliberate failure produced the full five-record shape, with the status code and
the tenant's explanation that the client's error discards:

```
{"type":"run","phase":"start","command":"landscaper artifact download","flags":{...}}
{"type":"http","method":"GET","status":404,"response_body":{"text":"{\"error\":{\"code\":\"Not Found\",...}}"}}
{"type":"item","operation":"download","status":"failed: package cannot be read: ...","failed":true}
{"type":"log","level":"fatal","msg":"1 of 1 artifacts could not be downloaded"}
{"type":"run","phase":"end","status":"failed","duration_ms":845}
```

The `run` end record is written by the tee *after* the fatal message and *before*
`os.Exit`, which is what makes a failure distinguishable from a killed process.

Checked on the real log: the client secret, the bearer token and the zip magic all
appear 0 times, and `request_headers.Authorization` is `<redacted>`. `--help` and a
run without `--log` create no `logs/` directory; `--log-dir` relocates the file and
leaves `logs/` untouched.

`testing/11-logging.sh` (22 assertions, registered in `LOCAL_SCRIPTS`) scripts the
same ground without a tenant, using `artifact pack`. It validates JSON Lines with
`python3`, not `jq` — nothing else in `testing/` assumes `jq` is installed.

## Implementation notes

 - **The OAuth token fetch is not recorded, and this tenant uses OAuth.** `conf/landscape.yaml` sets `tokenURL`, so `setAuth` (`cpiclient.go:226-238`) calls `clientcredentials.Config.Token()` on *every* request through its own HTTP client, bypassing `doRequest`. The log therefore shows roughly half the real network traffic. Capturing it means injecting `oauth2.HTTPClient` into the context, and would then require special-casing that exchange, since the request form carries the client secret and the response carries the access token — both `application/x-www-form-urlencoded` and `application/json`, i.e. "text" under the rules above. Deliberately out of scope; the log must not be mistaken for a complete network trace.
 - The tenant's own `ModifiedBy` field contains the service account id, so the OAuth **client id** does appear in recorded response bodies. That is an identity rather than a credential — the client secret never appears — and it is exactly the provenance an audit log should keep.
 - Every mutating operation produces **two** `http` records: `getCSRFToken` (`cpiclient.go:242`) makes a full round trip before the real call.
 - A child command defining its own `PersistentPreRun` would override `rootCmd`'s and silently lose the `run` start record — cobra runs only the closest one. None do today.
 - `packageMove.go:99` wraps its body in `defer recover()` and exits **0** on a panic. Such a run is recorded as `panic` only if the panic escapes that recover; otherwise it ends as `ok`.
 - `item` records for `artifactDownload` and `artifactUpload` are written from the `flush` closure, so they are emitted as the table is printed rather than as each artifact completes. That keeps one insertion point per command and guarantees every row is recorded exactly once.
 - No new module dependency: `encoding/json`, `mime`, `runtime`, `sync`, `unicode/utf8` are all standard library.
