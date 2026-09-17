# packages/cpiclient

SAP Cloud Integration OData client. Everything lives in `cpiclient.go` (~900
lines); `parse.go` holds null-safe JSON helpers; `artifact.go` is an empty stub.

`const apiVersion = "v1"`. `CPIClient.URL` is the **host only** - every call
builds `"https://" + s.URL + "/api/" + apiVersion + "/" + <entity>`.

## Auth

`NewCPIBasicAuthClient(username, password, tokenURL, url, verbose)` picks the
mode in `setAuth`: empty `TokenURL` means HTTP Basic, otherwise OAuth2 client
credentials with login/password used as ClientID/ClientSecret.

- **A fresh OAuth token is fetched on every single request** - no caching. `log.Fatalf` on failure.
- **A CSRF token is fetched before every mutating request** via `getCSRFToken()`, an extra round trip each time. It reads `headers["X-Csrf-Token"][0]` and **panics if the header is absent**.
- A cookie jar is created in the constructor because the CSRF token is paired with the `__Host-csrf-client-id` cookie.

## doRequest

Sets auth, sends, reads the body, reduces the status code to its leading digit
and returns `fmt.Errorf("%s", body)` - the raw OData error body - for anything
that is not 2xx.

**No retries, no timeout on the `http.Client`, no 429 or 5xx handling.** Callers
in `packages/cmd` almost always `log.Fatalln` the result.

## Response shapes

- Collection reads are JSON: `GET ...?$format=json`, unwrapped as `data["d"]["results"]`.
- `ReadIntegrationDesigntimeArtifact(Id, Version)` is the exception - **no `$format=json`, parsed as ATOM XML** into `IntegrationDesigntimeArtifactXMLEntry`.
- `parse.go` (`jsonString`, `jsonBool`, `parseIntegrationPackage`, `ReadAllIntegrationPackages` with `$top`/`$skip` paging) is null-safe. `cpiclient.go` uses unchecked type assertions and can panic on null fields - `CopyIntegrationPackageFromDiscover` especially.

## Artifact content

`IntegrationDesigntimeArtifact.ArtifactContent` is a **base64-encoded zip**.
Download is `.../$value` returning raw bytes; upload sends the base64 string.
The struct's `json:"-"` tags decide the request body: `Version`, `Sender`,
`Receiver` and `Configurations` are excluded, so a POST body is
`{Id, PackageId, Name, Description, ArtifactContent}` and **the version is not
sent** - the tenant takes it from `Bundle-Version` in the archive.

`Version == "Active"` means the artifact is a **draft** in the tenant.

## Creating vs updating

- `UploadIntegrationDesigntimeArtifact` - `POST IntegrationDesigntimeArtifacts`, for a new artifact.
- `UpdateIntegrationDesigntimeArtifact` - `PUT IntegrationDesigntimeArtifacts(Id='X',Version='Active')`, keeps history and configuration. **Prefer this** over the delete-then-recreate that `packageMove.go` and `artifactUpgrade.go` still do.

Deploy is fire and forget: `DeployIntegrationDesigntimeArtifact` discards the
returned task id and nothing polls the status. Read it back with
`ReadIntegrationRuntimeArtifact(Id)` (`Status`, `Version`, `DeployedBy`).

Copy the shape of `UpdateIntegrationDesigntimeArtifact` for new methods, not
`UploadIntegrationDesigntimeArtifact` - the latter shadows its `json.Marshal`
error and never checks it.
