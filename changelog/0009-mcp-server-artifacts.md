# MCP Server artifacts through the UI session

Branch: `feature/mcp-server-artifact`

## Context

SAP Integration Suite has an artifact type "MCP Server" (Integration Package ->
Add -> MCP Server, runtime profile Integration Cell). The trial tenant holds a
working server `petstore` (18 tools) in package `MCPservers`, next to
`Northwind` and an empty `id`. landscaper did not see any of them:
`artifact list --pkg MCPservers` printed an empty table and `artifact download`
downloaded nothing, both because `IntegrationDesigntimeArtifacts` of the package
is `{"d":{"results":[]}}`. MCP servers could not be versioned in git.

## What the tenant does

Probed on the trial tenant on 2026-10-19, read only.

The OData API has no MCP servers at all:

| Call | Observed |
|---|---|
| `GET /api/v1/$metadata` | About 140 entity sets, 35 function imports. None about MCP, no `APIDefinitions`, no deploy function for the type |
| Navigation of `IntegrationPackage` | iFlows, value and message mappings, script collections, data, message and fault message types, service interfaces, custom tags |
| `IntegrationPackages('MCPservers')/IntegrationDesigntimeArtifacts` | `{"d":{"results":[]}}` |
| `IntegrationDesigntimeArtifacts(Id='petstore',Version='active')` | `501` "No message reference given" |
| `IntegrationRuntimeArtifacts` / `('petstore')` | Empty / `404`. Integration Cell deployments are not in the v1 runtime API |
| SAP Help, Business Accelerator Hub, community | No MCP server design time or deploy API documented |

The web UI uses its own API on the **design time host**
(`<subaccount>.integrationsuite-trial01.cfapps...`, not the `it-cpitrial` API
host). It was found from the requests the UI sends and then called from Go:

| Call | Observed |
|---|---|
| any call with the OAuth client credentials token | `200` **`text/html`**: the login page. Same on the API host under `/itspaces`; `/api/1.0/...` on the API host is `404` |
| any call with the cookie header of a logged-in browser | Works. Needs `__VCAP_ID__` (sticky session) besides `JSESSIONID` |
| `GET /odata/1.0/workspace.svc/ContentEntities.ContentPackages('<id>')/Artifacts?$format=json` | Artifacts of every type. MCP servers: `Type: MCPSERVER`, `Name` = id = `Bundle-SymbolicName`, `DisplayName` = `Bundle-Name`, `reg_id`, `Version` (`Draft` for unsaved changes). Unknown package `404` "Entity not found" |
| `GET /api/1.0/contentpackage/<pkg reg_id>/artifacts?$resourceinfo=true` | Same servers, but **an empty list** without `x-navcontext-header: Integration` and `$resourceinfo=true`. Not used |
| `GET /odata/1.0/workspace.svc/ContentEntities.ContentPackages('<id>')?$format=json` | `reg_id` of the package |
| `GET /api/1.0/workspace/<pkg reg_id>/artifacts/<reg_id>/entities/<reg_id>?export=true` | The bundle zip, `application/octet-stream`, identical to the UI download except the timestamp in `metainfo.prop`. **`Accept: application/octet-stream`, `application/zip` or none: empty `500`**; `*/*` and `application/json` work |
| `GET /odata/1.0/catalog.svc/Artifacts(Name='petstore',Type='MCPSERVER')/$value` | `500` `PACKAGE_UNAVAILABLE`, although the list advertises it as `media_src` |
| `GET .../entities/<reg_id>/mcpartifacts/<name>` | The editor model (BPMN, tools with schemas, OpenAPI, manifest). `<name>` is the display name |
| `POST /api/1.0/workspace/<pkg reg_id>/mcpartifacts/` | Create from an OpenAPI definition (captured from the UI, not called) |

The bundle carries `src/main/resources/parameters.prop` and `.propdef`, so MCP
servers can have externalized parameters, unlike what the proposal assumed.

## Verified on the tenant

- `artifact upload` of the downloaded `petstore` to Dev: `updated`, deployed, `STARTED`. Downloaded again, the
  bundle is the same: server card and source mapping equal as JSON, all 130 flow properties equal, the manifest
  identical; only the order of the properties and the export timestamps differ.
- The same folder to QA: package `MCPserversQA` and server `petstoreQA` created, saved and deployed, `STARTED`.
  Downloaded, it has name `petstore1QA`, path `/mcp-server-qa` from the landscape file, the same 18 tools with the
  same schemas, 4 resources, 3 templates and the prompt. Undeployed again with `artifact undeploy`.

## Decision

Calling an undocumented UI API with a session copied from the browser was
chosen over giving up ("no API") and over scripting the IdP login with a
personal password. It is interactive only - the session expires after about 30
minutes - and the README says so.

## Implementation

- `cpiclient/mcpservers.go`: `SetUISession`, `HasUISession`, `ReadMCPServers`,
  `ReadPackageRegistryId`, `DownloadMCPServer`, all through `doUIRequest`.
  `doUIRequest` sends the cookie and **no `Authorization`** - the Basic
  password must not travel to a second host - through an `http.Client` of its
  own without a cookie jar, so that a `Set-Cookie` cannot add a second
  `JSESSIONID`. A `text/html` answer, `401` or `403` is `ErrUISessionExpired`.
  The call goes through the audit log, where `Cookie` was already redacted.
- `landscape.yaml`: optional `uiHost` (literal or variable, a URL from the
  address bar is reduced to the host) and `uiCookie` (variable only) per system.
- `artifact list`: MCP servers after the iFlows, a new last column `Type`.
  Without a UI session an empty package gets a hint on stderr naming the
  missing configuration. `--only-deployed` leaves MCP servers out.
- `artifact download`: MCP servers next to the iFlows of the same package, in
  `<output>/<package>/<id>/`, checked for `SAP-BundleType: MCPSERVER` before
  extraction. A failing MCP list is a failed row, the iFlows still download.
  `--artifacts` and `--download-all` work unchanged, as an MCP server is an
  artifact of its package.
- `init` is unchanged: it declares an artifact only for its parameters, and the
  parameters of an MCP server are not read.

Without `uiHost`/`uiCookie` no UI call is made and the commands behave as
before, apart from the `Type` column and the stderr hint.

## Write side

Captured from the UI on 2026-10-19, then called from Go on 2026-10-20.

| Call | Observed |
|---|---|
| Import of a zip | **Does not exist** for MCP servers |
| `POST /api/1.0/workspace/<pkg reg>/mcpartifacts/` | Creates a server from name, id, `mcpPath`, tool list and OpenAPI; tool schemas are derived by the tenant. **Accepts** the name `petstore1 QA` |
| `PUT .../mcpartifacts/<id>` with the model | The save. `MCP_ARTIFACT_PATH_EMPTY` without `mcpPath`, which the GET of the model does not return. `MCP_ARTIFACT_NAME_VALIDATION_FAILED` for a name with a space: "only letters, numbers, and underscores". **Without the edit lock always `500 ARTIFACT_UPDATE_FAILED` "Update artifact in content store failed"** - with or without a real change, for an old and for a new server. `PUT ...?lockinfo=true&webdav=LOCK` only answers the lock info and locks nothing, `POST` of it is `405` |
| `PUT /api/1.0/workspace/<pkg reg>/artifacts/<reg>?webdav=LOCK` | Takes the edit lock (`isLocked` true); the save then answers `200` with the saved model. `?webdav=UNLOCK` releases it. Found in the capture of the UI deleting a server, which locks the same way |
| `PUT ...?action=DEPLOY&runtimeLocation=integrationcell&virtualHost=default`, `{}` | `200` with `taskId` and the model |
| `GET /api/1.0/deploystatus/<task>?bundleType=MCPSERVER&symbolicName=<reg>` | `DEPLOYING` -> `SUCCESS` for the UI. For `petstoreQA` (created with the name `petstore1 QA`): **`FAIL`** |
| Integration Cell list (`IntegrationComponentsListCommand`) | That failed `petstoreQA` stays `deployState STORED`, `semanticState STARTING` for good |
| `DeleteContentCommand` | Undeploys; the server leaves the list within seconds |
| `X-CSRF-Token: Fetch` on `GET /api/1.0/user` | Hands out the token for the calls above |

Implementation: `packages/mcpserver` (pure) reads the bundle - manifest, server card, source mapping, OpenAPI and the
`ifl:property` values of the flow by element id - and writes it onto the editor model. Applying an unchanged export
onto the model it was exported from leaves the flow part identical; the only top level differences are `mcpPath`,
`shortText` and `description`, which the GET leaves out and the UI sends. The collaboration is `Collaboration_1` in
the flow and `Collobaration_1` in the model. `artifact upload` hands MCP folders to `uploadMCPServer`, `artifact
deploy`/`undeploy` fall back to MCP when the v1 API does not know the id and the system has a UI session. `--wait`
polls the **deployment task**, not the runtime list, because only the task reports a failure. `mcpPath` and
`targetURL` are the configuration of an environment, any other key is refused.

## Not done

- Runtime error text of a failed deployment: the task has none, the reason is in Monitor.
- Delete, by decision. A server created by a failed upload stays until it is removed in the UI.
- `--bump`, `--set-version` and the version check of `artifact pack` for MCP servers; zip archives of MCP servers.

## Tests

`cmd/mcpServers_test.go` extends the stub tenant with the UI endpoints, using
the recorded artifact list (host and user replaced): download of iFlows and
servers together, selection by id, the expired session, no UI call without a
session, no `Authorization` and a single `JSESSIONID` on UI calls, no cookie in
the audit log. `cpiclient/mcpservers_test.go` covers the list parser, the host
normalisation, the login page detection and the recorded Integration Cell list.
`mcpserver/bundle_test.go` covers reading a bundle, the flow properties, the
environment overrides, applying it onto a model and the create payload. The cmd
stub also holds models, creates, deployments and deploy tasks: create then
save, update in a suffixed environment with `mcpPath` and `targetURL`, a refused
parameter, deploy with `--wait`, a `FAIL` task reported as ERROR, undeploy.
