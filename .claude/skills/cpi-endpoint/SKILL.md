---
name: cpi-endpoint
description: Add or change a method on CPIClient that calls the SAP Cloud Integration OData API - reading, creating, updating, deleting, deploying or undeploying packages, integration flows, configurations or runtime artifacts. Use when a landscaper feature needs an API call that does not exist yet in packages/cpiclient.
---

# Adding a CPIClient method

All methods live in `packages/cpiclient/cpiclient.go`. The endpoint reference is
`assets/IntegrationContent.yaml` - **159KB of swagger, grep it, never read it
whole**:

```bash
grep -n "IntegrationRuntimeArtifacts" assets/IntegrationContent.yaml
```

## Template

`UpdateIntegrationDesigntimeArtifact` is the reference. Copy that, **not**
`UploadIntegrationDesigntimeArtifact` - the latter shadows its `json.Marshal`
error and never checks it.

```go
func (s *CPIClient) DoSomething(Id string) error {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "Entity(Id='" + Id + "')")

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPut, url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	//Only for mutating requests
	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Content-Type", "application/json")

	_, _, err = s.doRequest(req)
	if err != nil {
		return err
	}

	return nil
}
```

## Rules

- `s.URL` is the **host only**. Always `"https://" + s.URL + "/api/" + apiVersion + "/"`.
- Always `http.NewRequestWithContext(s.traceCtx, ...)`, never `http.NewRequest`.
- `doRequest` handles auth and returns `(body, headers, error)`, with the raw OData error body as the error for any non-2xx. Do not add status handling of your own.
- GET needs no CSRF token. POST/PUT/DELETE all need `getCSRFToken()` first - that is a second round trip, which is normal here.
- OData string keys are single-quoted inside the URL: `Entity(Id='X',Version='Active')`.

## Reading responses

Collections - append `?$format=json` and unwrap:

```go
data["d"].(map[string]interface{})["results"].([]interface{})
```

Prefer the null-safe helpers in `parse.go` (`jsonString`, `jsonBool`) over bare
type assertions; the older code in `cpiclient.go` panics on null fields.

Single designtime artifact reads are the exception: **no `$format=json`, ATOM
XML**, unmarshalled into an `...XMLEntry` struct. Follow
`ReadIntegrationDesigntimeArtifact` if you add another single-entity read.

For a paginated collection use `$top=500`/`$skip` as in `ReadAllIntegrationPackages`.

## Structs

Add or extend the struct next to the others at the top of `cpiclient.go`. The
`json:"-"` tags control the request body - on `IntegrationDesigntimeArtifact`,
`Version`, `Sender`, `Receiver` and `Configurations` are excluded, so the
version is never sent and the tenant takes it from `Bundle-Version` in the
uploaded archive.

`ArtifactContent` is always a **base64-encoded zip**.

## Watch out

- `getCSRFToken()` panics if the response has no `X-Csrf-Token` header.
- There are no retries, no client timeout and no 429/5xx handling. If a feature needs them, say so rather than silently adding a different error style.
- `Version == "Active"` means the artifact is a **draft** in the tenant.
- OAuth tokens are re-fetched on every request; do not assume any caching exists.

## Finish

`packages/cpiclient` has no tests. Cover a new method indirectly from
`packages/cmd`, following the `httptest.NewTLSServer` stub in
`artifactUpload_test.go`, then `go build ./... && go vet ./... && go test ./packages/...`.
