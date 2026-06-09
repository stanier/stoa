# MCP Integration Servers — Design Reference

Design and implementation notes for the third-party API integration servers. See [ROADMAP.md](../ROADMAP.md) for the MCP server priority list and architecture principles.

**Implementation status:**

| Server | Status |
|--------|--------|
| `mcp-trello` | ✅ Implemented (`cmd/mcp-trello`) |
| `mcp-zulip` | ✅ Implemented (`cmd/mcp-zulip`) |
| `mcp-netbox` | ✅ Implemented (`cmd/mcp-netbox`) |
| `mcp-promql` | ✅ Implemented (`cmd/mcp-promql`) |
| `mcp-graphql` | ✅ Implemented (`cmd/mcp-graphql`) |
| `mcp-gitlab` | ❌ Not started (spec below) |

The detailed spec for `mcp-gitlab` is below. The implemented servers have their own sections as stubs pointing at the code.

---

## Shared Infrastructure

Before implementing individual servers, identify shared code to extract or replicate from the existing HTTP-based servers (`mcp-fetch`, `mcp-http`).

### Shared HTTP Client Pattern

Both `mcp-fetch` and `mcp-http` implement a custom `DialContext` that blocks private IPs (SSRF prevention). All new servers making outbound HTTP calls should replicate this pattern.

**Shared concerns across new servers**:
- Private IP blocking (RFC1918 + loopback + link-local)
- Configurable timeout (default 30s)
- User-Agent header
- Response size limits where appropriate

**Candidate location**: `internal/mcpserver/httpclient/` — a small shared package with a `NewSecureClient()` constructor that all integration servers can import. This avoids copy-pasting the dial context logic.

### Auth Helpers

Several servers use similar auth patterns. Shared types would help:
- **Bearer token**: Set `Authorization: Bearer <token>` header
- **HTTP Basic**: Set `Authorization: Basic <base64(user:pass)>`
- **Custom token**: Set `Authorization: Token <token>` (Netbox style)
- **Query-param auth**: Append `key=...&token=...` to all URLs (Trello)

### Response Formatting

All servers return `textResult(string)` or `errorResult(string)`. For API responses, a consistent pattern for formatting JSON results as readable text is useful. Consider a `formatJSON(v any) string` helper using `json.MarshalIndent`.

---

## 1. `mcp-trello`

Implemented. See [`internal/mcpserver/trello/server.go`](../internal/mcpserver/trello/server.go) and [`cmd/mcp-trello/main.go`](../cmd/mcp-trello/main.go).

---

## 2. `mcp-gitlab`

**Purpose**: Interact with GitLab projects, issues, merge requests, pipelines, and repository content. Supports both gitlab.com and self-hosted instances.

**Auth**: Personal access token or project access token sent as `PRIVATE-TOKEN: <token>` header (GitLab REST API style). Bearer token is also supported (`Authorization: Bearer <token>`) but the header style is canonical.

**Base URL**: `https://gitlab.com/api/v4` (configurable for self-hosted)

**Rate limits**: ~2,000 requests/minute for REST API. Pagination via `X-Next-Page` header and `page`/`per_page` query params.

**Go dependencies**: Standard library only

### Config Flags

```
-base-url         string   GitLab API base URL (default: https://gitlab.com/api/v4)
-token            string   Personal or project access token (required)
-default-project  string   Default project ID or URL-encoded namespace/path
-timeout          int      HTTP timeout in seconds (default: 30)
-log              string   Log file path (default: stderr)
```

### Project References

GitLab accepts project references as either:
- Integer project ID: `12345`
- URL-encoded namespace path: `mygroup%2Fmyproject`

All tools that take `project_id` accept either form. The server URL-encodes path-style references before embedding in URL paths.

### Tools

---

#### `gitlab_list_projects`

Search for accessible projects.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| search | string | no | Filter by name/path search term |
| owned | bool | no | Only projects owned by caller (default: false) |
| membership | bool | no | Only projects where caller is a member (default: false) |
| starred | bool | no | Only starred projects (default: false) |
| order_by | string | no | `id`, `name`, `path`, `created_at`, `updated_at`, `last_activity_at` (default: `last_activity_at`) |
| per_page | int | no | Results per page (default: 20, max: 100) |
| page | int | no | Page number (default: 1) |

**API**: `GET /projects?search=...&owned=...&membership=...`

**Returns**: Projects with id, name, path_with_namespace, description, web_url, last_activity_at.

---

#### `gitlab_get_project`

Get full project details.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |

**API**: `GET /projects/{id}`

**Returns**: Project details including id, name, description, web_url, default_branch, open_issues_count, fork_count, star_count, visibility.

---

#### `gitlab_list_issues`

List issues for a project.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| state | string | no | `opened`, `closed`, `all` (default: `opened`) |
| labels | string | no | Comma-separated label names |
| assignee | string | no | Username of assignee |
| search | string | no | Search in title and description |
| milestone | string | no | Milestone title |
| order_by | string | no | `created_at`, `updated_at`, `priority` (default: `created_at`) |
| sort | string | no | `asc` or `desc` (default: `desc`) |
| per_page | int | no | Results per page (default: 20, max: 100) |

**API**: `GET /projects/{id}/issues?state=...&labels=...`

**Returns**: Issues with iid, title, state, labels, assignees, created_at, updated_at, web_url.

---

#### `gitlab_get_issue`

Get a single issue with full details and notes.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| issue_iid | int | yes | Issue internal ID (the number shown in the UI) |

**API**: `GET /projects/{id}/issues/{iid}`

**Returns**: Full issue: title, description, state, labels, assignees, milestone, created_at, updated_at, closed_at, web_url.

---

#### `gitlab_create_issue`

Create a new issue.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| title | string | yes | Issue title |
| description | string | no | Issue body (Markdown) |
| labels | string | no | Comma-separated label names |
| assignee_ids | string | no | Comma-separated user IDs to assign |
| milestone_id | int | no | Milestone ID |
| due_date | string | no | Due date (YYYY-MM-DD) |

**API**: `POST /projects/{id}/issues`

**Returns**: Created issue iid, title, web_url.

---

#### `gitlab_update_issue`

Update an existing issue.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| issue_iid | int | yes | Issue internal ID |
| title | string | no | New title |
| description | string | no | New description |
| state_event | string | no | `close` or `reopen` |
| labels | string | no | New comma-separated label list (replaces all labels) |
| add_labels | string | no | Labels to add without removing existing |
| remove_labels | string | no | Labels to remove |
| assignee_ids | string | no | New assignee user IDs (empty string clears assignees) |
| milestone_id | int | no | New milestone ID (0 clears milestone) |
| due_date | string | no | New due date (YYYY-MM-DD) |

**API**: `PUT /projects/{id}/issues/{iid}`

**Returns**: Updated issue iid, title, state, web_url.

---

#### `gitlab_add_issue_note`

Add a comment to an issue.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| issue_iid | int | yes | Issue internal ID |
| body | string | yes | Comment text (Markdown) |

**API**: `POST /projects/{id}/issues/{iid}/notes`

**Returns**: Note id, body, created_at, author username.

---

#### `gitlab_list_mrs`

List merge requests for a project.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| state | string | no | `opened`, `closed`, `locked`, `merged`, `all` (default: `opened`) |
| source_branch | string | no | Filter by source branch |
| target_branch | string | no | Filter by target branch |
| author_username | string | no | Filter by author |
| assignee_id | int | no | Filter by assignee user ID |
| labels | string | no | Comma-separated label filter |
| search | string | no | Search in title and description |
| per_page | int | no | Results per page (default: 20, max: 100) |

**API**: `GET /projects/{id}/merge_requests?state=...`

**Returns**: MRs with iid, title, state, source_branch, target_branch, author, labels, web_url.

---

#### `gitlab_get_mr`

Get a single merge request with full details.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| mr_iid | int | yes | MR internal ID |

**API**: `GET /projects/{id}/merge_requests/{iid}`

**Returns**: Full MR: title, description, state, source_branch, target_branch, author, reviewers, labels, created_at, merged_at, web_url, pipeline status.

---

#### `gitlab_create_mr`

Create a new merge request.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| source_branch | string | yes | Branch to merge from |
| target_branch | string | yes | Branch to merge into |
| title | string | yes | MR title |
| description | string | no | MR description (Markdown) |
| labels | string | no | Comma-separated label names |
| assignee_ids | string | no | Comma-separated user IDs |
| reviewer_ids | string | no | Comma-separated reviewer user IDs |
| remove_source_branch | bool | no | Delete source branch on merge (default: false) |
| draft | bool | no | Create as draft MR (default: false) |

**API**: `POST /projects/{id}/merge_requests`

**Returns**: Created MR iid, title, web_url.

---

#### `gitlab_update_mr`

Update an existing merge request.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| mr_iid | int | yes | MR internal ID |
| title | string | no | New title |
| description | string | no | New description |
| state_event | string | no | `close` or `reopen` |
| target_branch | string | no | New target branch |
| labels | string | no | Replace all labels |
| add_labels | string | no | Labels to add |
| remove_labels | string | no | Labels to remove |
| assignee_ids | string | no | New assignees (empty clears) |
| reviewer_ids | string | no | New reviewers |
| draft | bool | no | Toggle draft state |

**API**: `PUT /projects/{id}/merge_requests/{iid}`

**Returns**: Updated MR iid, title, state, web_url.

---

#### `gitlab_add_mr_note`

Add a comment to a merge request.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| mr_iid | int | yes | MR internal ID |
| body | string | yes | Comment text (Markdown) |

**API**: `POST /projects/{id}/merge_requests/{iid}/notes`

**Returns**: Note id, body, created_at, author username.

---

#### `gitlab_list_pipelines`

List pipelines for a project.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| status | string | no | `created`, `waiting_for_resource`, `preparing`, `pending`, `running`, `success`, `failed`, `canceled`, `skipped`, `manual`, `scheduled` |
| ref | string | no | Filter by branch/tag name |
| username | string | no | Filter by triggering user |
| per_page | int | no | Results per page (default: 20, max: 100) |

**API**: `GET /projects/{id}/pipelines`

**Returns**: Pipelines with id, status, ref, sha, created_at, updated_at, web_url.

---

#### `gitlab_get_pipeline`

Get pipeline details and job summary.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| pipeline_id | int | yes | Pipeline ID |

**API**: `GET /projects/{id}/pipelines/{pipeline_id}` and `GET /projects/{id}/pipelines/{pipeline_id}/jobs`

**Returns**: Pipeline status, ref, sha, duration, web_url, and list of jobs with name, status, stage, duration.

---

#### `gitlab_retry_pipeline`

Retry all failed jobs in a pipeline.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| pipeline_id | int | yes | Pipeline ID |

**API**: `POST /projects/{id}/pipelines/{pipeline_id}/retry`

**Returns**: New pipeline id, status, web_url.

---

#### `gitlab_list_branches`

List branches in a repository.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| search | string | no | Filter by branch name |
| per_page | int | no | Results per page (default: 20, max: 100) |

**API**: `GET /projects/{id}/repository/branches`

**Returns**: Branches with name, commit sha, protected status, last commit message and date.

---

#### `gitlab_get_file`

Read a file from the repository.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| file_path | string | yes | File path within repository |
| ref | string | no | Branch, tag, or commit SHA (default: project default branch) |

**API**: `GET /projects/{id}/repository/files/{url-encoded-path}?ref={ref}`

**Returns**: File name, path, encoding, content (decoded from base64), last commit message and SHA.

**Note**: GitLab returns file content as base64. The server decodes this before returning.

---

#### `gitlab_list_commits`

List commits in a repository.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| ref_name | string | no | Branch, tag, or commit range |
| path | string | no | Filter commits touching this file path |
| since | string | no | Only commits after this date (ISO8601) |
| until | string | no | Only commits before this date (ISO8601) |
| per_page | int | no | Results per page (default: 20, max: 100) |

**API**: `GET /projects/{id}/repository/commits`

**Returns**: Commits with id (short SHA), title, author name, committed_date, web_url.

---

#### `gitlab_search`

Search within a project across issues, MRs, commits, code, or milestones.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| project_id | string | yes | Project ID or namespace/path |
| scope | string | yes | `issues`, `merge_requests`, `milestones`, `notes`, `wiki_blobs`, `commits`, `blobs`, `users` |
| search | string | yes | Search query |
| per_page | int | no | Results per page (default: 10, max: 100) |

**API**: `GET /projects/{id}/search?scope={scope}&search={query}`

**Returns**: Matched items formatted for the given scope.

---

### File Structure

```
internal/mcpserver/gitlab/
└── server.go

cmd/mcp-gitlab/
└── main.go
```

### Implementation Notes

- Project paths with `/` must be URL-encoded as `%2F` when embedded in URL paths. Implement a `encodeProjectRef(s string) string` helper.
- GitLab uses `iid` (internal/project-scoped ID) vs `id` (global ID). Issues and MRs are referenced by `iid` in the UI and API paths.
- File content from `GET /repository/files/` is base64-encoded. Always decode before returning.
- Pagination: GitLab returns `X-Next-Page` and `X-Total-Pages` headers. For list tools, just fetch page 1 by default and document how to paginate.
- Pipeline jobs API is a separate call from pipeline details; `gitlab_get_pipeline` should merge both for a useful single response.
- The `state_event` field for issues/MRs is the proper way to close/reopen — not setting `state` directly.

---

## 3. `mcp-zulip`

Implemented. See [`internal/mcpserver/zulip/server.go`](../internal/mcpserver/zulip/server.go) and [`cmd/mcp-zulip/main.go`](../cmd/mcp-zulip/main.go).

---

## 4. `mcp-netbox`

Implemented. See [`internal/mcpserver/netbox/server.go`](../internal/mcpserver/netbox/server.go) and [`cmd/mcp-netbox/main.go`](../cmd/mcp-netbox/main.go).

---

## 5. `mcp-promql`

Implemented. See [`internal/mcpserver/promql/server.go`](../internal/mcpserver/promql/server.go) and [`cmd/mcp-promql/main.go`](../cmd/mcp-promql/main.go).

---

## 6. `mcp-graphql`

Implemented. See [`internal/mcpserver/graphql/server.go`](../internal/mcpserver/graphql/server.go) and [`cmd/mcp-graphql/main.go`](../cmd/mcp-graphql/main.go).

---

## Makefile

Add build target for `mcp-gitlab` once implemented:

```makefile
build-mcp-gitlab:
	$(GOBUILD) -o bin/debug/mcp-gitlab ./cmd/mcp-gitlab
```

And include it in the `build-mcp-all` target.

---

## Config Example

```yaml
mcp:
  servers:
    - name: gitlab
      command: /path/to/bin/mcp-gitlab
      args: ["-token", "glpat-...", "-default-project", "mygroup/myproject"]
```
