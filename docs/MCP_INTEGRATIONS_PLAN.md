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
| `mcp-gitlab` | ❌ Not started (design below) |

The sections below are design notes — useful as reference for the implementations, and as the spec for `mcp-gitlab`.

1. [`mcp-trello`](#1-mcp-trello)
2. [`mcp-gitlab`](#2-mcp-gitlab) — **not yet built**
3. [`mcp-zulip`](#3-mcp-zulip)
4. [`mcp-netbox`](#4-mcp-netbox)
5. [`mcp-promql`](#5-mcp-promql)
6. [`mcp-graphql`](#6-mcp-graphql)

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

> **Decision**: Whether to extract to shared package or duplicate. Duplication is simpler but creates drift. Extraction adds a package import path. Recommend extraction since 4+ servers need it.

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

**Purpose**: Read and write Trello boards, lists, cards, and comments. Enables agents to manage project tasks in Trello.

**Auth**: API key + token appended as query parameters to every request.
- Get at: https://trello.com/power-ups/admin → generate a key, then authorize a token
- Every request: `GET https://api.trello.com/1/path?key=API_KEY&token=TOKEN`

**Base URL**: `https://api.trello.com/1` (configurable for enterprise)

**Rate limits**: 100 requests per 10 seconds per token. No explicit retry logic needed for typical agent use.

**Go dependencies**: Standard library only (`net/http`, `encoding/json`)

### Config Flags

```
-api-key   string   Trello API key (required)
-token     string   Trello API token (required)
-base-url  string   API base URL (default: https://api.trello.com/1)
-timeout   int      HTTP timeout in seconds (default: 30)
-log       string   Log file path (default: stderr)
```

### Tools

---

#### `trello_list_boards`

List all boards the authenticated user has access to.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| filter | string | no | `all`, `open`, `closed`, `starred`, `pinned` (default: `open`) |

**API**: `GET /members/me/boards?fields=id,name,desc,url,closed,starred&filter={filter}`

**Returns**: Formatted list of boards with id, name, description, and URL.

---

#### `trello_get_board`

Get full details of a board including its lists and member count.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| board_id | string | yes | Board ID |

**API**: `GET /boards/{id}?lists=open&members=all&fields=id,name,desc,url,closed`

**Returns**: Board details, list of lists (id, name, position), member count.

---

#### `trello_list_lists`

Get all lists on a board.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| board_id | string | yes | Board ID |
| filter | string | no | `all`, `open`, `closed` (default: `open`) |

**API**: `GET /boards/{id}/lists?filter={filter}`

**Returns**: Lists with id, name, position.

---

#### `trello_list_cards`

List cards. Can scope to a board or a specific list.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| board_id | string | no | Filter by board (one of board_id or list_id required) |
| list_id | string | no | Filter by list (more precise than board_id) |
| filter | string | no | `all`, `open`, `closed`, `visible` (default: `open`) |

**API**:
- Board scope: `GET /boards/{board_id}/cards?filter={filter}&fields=id,name,desc,due,closed,idList,labels`
- List scope: `GET /lists/{list_id}/cards?filter={filter}&fields=id,name,desc,due,closed,labels`

**Returns**: Cards with id, name, short description, due date, list id, labels.

---

#### `trello_get_card`

Get full card details including checklists, members, attachments, and recent comments.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| card_id | string | yes | Card ID |

**API**: `GET /cards/{id}?checklists=all&members=true&actions=commentCard&actions_limit=10&fields=id,name,desc,due,duecomplete,closed,idList,idBoard,labels,url`

**Returns**: Full card details: name, description, due date, checklist items, assigned members, last 10 comments, URL.

---

#### `trello_create_card`

Create a new card in a list.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| list_id | string | yes | Target list ID |
| name | string | yes | Card title |
| desc | string | no | Card description (supports Markdown) |
| due | string | no | Due date in ISO8601 format |
| position | string | no | `top`, `bottom`, or a positive float (default: `bottom`) |
| label_ids | string | no | Comma-separated label IDs to apply |

**API**: `POST /cards` with body: `{idList, name, desc, due, pos, idLabels}`

**Returns**: Created card id, name, URL.

---

#### `trello_update_card`

Update card fields (move to different list, change title, set due date, etc.).

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| card_id | string | yes | Card ID |
| name | string | no | New title |
| desc | string | no | New description |
| due | string | no | New due date (ISO8601) or empty string to clear |
| due_complete | bool | no | Mark due date as complete |
| list_id | string | no | Move card to this list |
| closed | bool | no | Archive (true) or unarchive (false) the card |
| position | string | no | `top`, `bottom`, or a float |

**API**: `PUT /cards/{id}` with changed fields as body

**Returns**: Updated card id, name, current list id.

---

#### `trello_add_comment`

Add a comment to a card.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| card_id | string | yes | Card ID |
| text | string | yes | Comment text (supports Markdown) |

**API**: `POST /cards/{id}/actions/comments` with body: `{text}`

**Returns**: Comment id, creation time, text.

---

#### `trello_search`

Search across Trello for cards, boards, and members.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| query | string | yes | Search query |
| model_types | string | no | `cards`, `boards`, `members`, `all` (default: `cards,boards`) |
| board_ids | string | no | Comma-separated board IDs to scope search |
| cards_limit | int | no | Max card results (default: 10, max: 100) |

**API**: `GET /search?query={q}&modelTypes={types}&cards_fields=id,name,desc,idList,url&boards_fields=id,name,url`

**Returns**: Grouped results by type: cards (with board/list context), boards, members.

---

#### `trello_get_labels`

List all labels defined on a board.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| board_id | string | yes | Board ID |

**API**: `GET /boards/{id}/labels?fields=id,name,color`

**Returns**: Labels with id, name, color.

---

### File Structure

```
internal/mcpserver/trello/
└── server.go

cmd/mcp-trello/
└── main.go
```

### Implementation Notes

- The Trello API uses short numeric-looking IDs (actually 24-char hex). Accept them as strings.
- Card `due` field: the API accepts ISO8601 but returns it in the same format. Empty string clears due date.
- When listing cards for a board, the response can be large for active boards. Consider defaulting to `open` filter.
- `trello_search` returns cards from all boards the user can access; use `board_ids` to narrow when context is known.
- Label IDs are board-specific; always list labels before referencing them by ID.

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

**Purpose**: Full Zulip messaging client for agents. Distinct from the existing `zulip-bridge` (which translates Zulip↔agentd) — this server lets the LLM itself search messages, send to arbitrary streams, manage subscriptions, etc. The bridge is about integration; this MCP server is about programmatic access.

**Auth**: HTTP Basic Auth using bot email + API key on every request.
- Get bot credentials from: Zulip admin → Bots section

**Base URL**: `https://yourorg.zulipchat.com` (configurable, required)

**API path prefix**: `/api/v1/`

**Go dependencies**: Standard library only

### Config Flags

```
-base-url  string   Zulip server URL, e.g. https://yourorg.zulipchat.com (required)
-email     string   Bot email address (required)
-api-key   string   Bot API key (required)
-timeout   int      HTTP timeout in seconds (default: 30)
-log       string   Log file path (default: stderr)
```

### Narrow Format

Zulip's message filtering uses "narrows" — an array of `{operator, operand}` objects serialized as a JSON string in query parameters. The server handles this internally; tool parameters use human-friendly fields.

```json
// Narrow for stream + topic
[{"operator":"stream","operand":"general"},{"operator":"topic","operand":"announcements"}]
```

### Tools

---

#### `zulip_send_message`

Send a message to a stream (channel) or as a direct message.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| type | string | yes | `stream` or `direct` |
| to | string | yes | For stream: stream name. For direct: comma-separated email addresses |
| content | string | yes | Message content (Zulip-flavored Markdown) |
| topic | string | no | Topic name (required when type=stream) |

**API**: `POST /api/v1/messages`

**Returns**: Message ID.

---

#### `zulip_get_messages`

Retrieve messages with flexible filtering.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| stream | string | no | Stream name to filter by |
| topic | string | no | Topic to filter by (requires stream) |
| sender_email | string | no | Filter by sender email |
| search | string | no | Full-text search query |
| anchor | string | no | `newest`, `oldest`, or a message ID (default: `newest`) |
| num_before | int | no | Messages before anchor (default: 20) |
| num_after | int | no | Messages after anchor (default: 0) |

**API**: `GET /api/v1/messages` with constructed narrow array

**Returns**: Messages with id, sender name, stream/topic, content, timestamp.

**Note**: `search` operator is combined with stream/topic narrows when both are provided.

---

#### `zulip_update_message`

Edit a message's content.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| message_id | int | yes | Message ID to edit |
| content | string | no | New content |
| topic | string | no | New topic (stream messages only) |
| propagate_mode | string | no | For topic changes: `change_one`, `change_later`, `change_all` (default: `change_one`) |

**API**: `PATCH /api/v1/messages/{msg_id}`

**Returns**: Success confirmation.

---

#### `zulip_delete_message`

Delete a message. Bot must have permission to delete messages.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| message_id | int | yes | Message ID to delete |

**API**: `DELETE /api/v1/messages/{msg_id}`

**Returns**: Success confirmation.

---

#### `zulip_add_reaction`

Add an emoji reaction to a message.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| message_id | int | yes | Message ID |
| emoji_name | string | yes | Emoji name, e.g. `thumbs_up`, `check`, `eyes` |

**API**: `POST /api/v1/messages/{msg_id}/reactions`

**Returns**: Success confirmation.

---

#### `zulip_remove_reaction`

Remove an emoji reaction from a message.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| message_id | int | yes | Message ID |
| emoji_name | string | yes | Emoji name to remove |

**API**: `DELETE /api/v1/messages/{msg_id}/reactions`

**Returns**: Success confirmation.

---

#### `zulip_list_streams`

List streams (channels) visible to the bot.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| include_public | bool | no | Include public streams (default: true) |
| include_subscribed | bool | no | Include subscribed streams (default: true) |
| include_archived | bool | no | Include archived streams (default: false) |

**API**: `GET /api/v1/streams`

**Returns**: Streams with id, name, description, invite_only (private flag).

---

#### `zulip_get_stream`

Get details about a specific stream.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| stream_name | string | yes | Stream name |

**API**: `GET /api/v1/get_stream_id?stream={name}` then `GET /api/v1/streams/{id}`

**Returns**: Stream id, name, description, invite_only, date_created.

---

#### `zulip_get_topics`

List active topics in a stream.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| stream_name | string | yes | Stream name |

**API**: `GET /api/v1/get_stream_id?stream={name}` then `GET /api/v1/users/me/{stream_id}/topics`

**Returns**: Topics with name and max_id (most recent message ID in that topic).

---

#### `zulip_subscribe`

Subscribe the bot to one or more streams.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| streams | string | yes | Comma-separated stream names to subscribe to |

**API**: `POST /api/v1/users/me/subscriptions`

**Returns**: Which streams were newly subscribed vs already subscribed.

---

#### `zulip_list_users`

List users in the Zulip organization.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| include_bots | bool | no | Include bot accounts (default: false) |
| include_deactivated | bool | no | Include deactivated users (default: false) |

**API**: `GET /api/v1/users`

**Returns**: Users with id, full_name, email, is_bot, is_active.

---

#### `zulip_get_user`

Get details about a specific user.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| user_id | int | no | User ID (one of user_id or email required) |
| email | string | no | User email |

**API**: `GET /api/v1/users/{user_id}` or `GET /api/v1/users/{email}`

**Returns**: User details: id, full_name, email, is_bot, is_active, timezone.

---

### File Structure

```
internal/mcpserver/zulip/
└── server.go

cmd/mcp-zulip/
└── main.go
```

### Implementation Notes

- Zulip narrows are passed as a JSON array serialized to string. Build a helper: `buildNarrow(filters map[string]string) string`.
- The stream_id lookup is needed for topics (`/users/me/{stream_id}/topics`). Cache stream_name → stream_id after first lookup.
- Stream message `to` field takes stream name (as string), not stream ID. Direct messages take user emails as an array.
- Emoji names use underscores: `thumbs_up` not `:thumbs_up:`. Common ones: `check`, `cross_mark`, `eyes`, `writing_hand`, `check_mark`.
- The bot email/API key from config is used for Basic Auth on every request: `base64(email:api_key)`.
- Topic `propagate_mode` for edits: use `change_one` (safe default) unless explicitly editing all messages in a topic.

---

## 4. `mcp-netbox`

**Purpose**: Query Netbox DCIM/IPAM data using its native GraphQL API. Netbox's GraphQL endpoint exposes the full data model; this server provides both a raw query tool and convenience tools for common lookups.

**Auth**: `Authorization: Token <token>` header

**Endpoint**: `https://netbox.example.com/graphql/` (configurable)

**Go dependencies**: Standard library only (GraphQL over HTTP is just a JSON POST)

### GraphQL over HTTP

GraphQL requests are HTTP POST to the endpoint with:
```json
{
  "query": "{ devices { id name ... } }",
  "variables": { "key": "value" },
  "operationName": "OptionalName"
}
```

Response:
```json
{
  "data": { ... },
  "errors": [ { "message": "...", "locations": [...] } ]
}
```

Errors can coexist with partial data. The server should surface both.

### Config Flags

```
-base-url  string   Netbox base URL, e.g. https://netbox.example.com (required)
-token     string   Netbox API token (required)
-timeout   int      HTTP timeout in seconds (default: 30)
-log       string   Log file path (default: stderr)
```

### Tools

---

#### `netbox_query`

Execute an arbitrary GraphQL query against Netbox. Primary tool; all others are convenience wrappers.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| query | string | yes | GraphQL query or mutation string |
| variables | string | no | JSON object of GraphQL variables |

**API**: `POST /graphql/` with `{query, variables}`

**Returns**: Pretty-printed JSON of the `data` field. Lists any `errors` separately.

---

#### `netbox_get_devices`

Convenience query for device lookup with common filters.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| name | string | no | Filter by device name (partial match) |
| site | string | no | Filter by site name or slug |
| rack | string | no | Filter by rack name |
| role | string | no | Filter by device role name or slug |
| status | string | no | `active`, `planned`, `staged`, `failed`, `inventory`, `decommissioning`, `offline` |
| tenant | string | no | Filter by tenant name or slug |
| limit | int | no | Max results (default: 25) |

**Executes query**:
```graphql
query GetDevices($filter: DeviceFilter, $limit: Int) {
  device_list(filters: $filter, limit: $limit) {
    id name status { value }
    site { name }
    rack { name }
    device_role { name }
    device_type { model manufacturer { name } }
    primary_ip4 { address }
    tenant { name }
  }
}
```

**Returns**: Formatted device list with key fields.

---

#### `netbox_get_ip_addresses`

Query IP addresses with optional filters.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| address | string | no | Filter by address or prefix (e.g. `192.168.1.0/24` returns all IPs within) |
| status | string | no | `active`, `reserved`, `deprecated`, `dhcp`, `slaac` |
| device | string | no | Filter by device name |
| interface | string | no | Filter by interface name |
| tenant | string | no | Filter by tenant |
| limit | int | no | Max results (default: 25) |

**Returns**: IPs with address, status, VRF, device/interface assignment, DNS name, tenant.

---

#### `netbox_get_prefixes`

Query IP prefixes and subnets.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| prefix | string | no | Filter by prefix (e.g. `10.0.0.0/8`) |
| status | string | no | `active`, `reserved`, `deprecated`, `container` |
| vrf | string | no | Filter by VRF name |
| site | string | no | Filter by site |
| vlan_id | int | no | Filter by VLAN ID |
| tenant | string | no | Filter by tenant |
| limit | int | no | Max results (default: 25) |

**Returns**: Prefixes with prefix, status, VRF, site, VLAN, tenant, description.

---

#### `netbox_get_sites`

Query sites.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| name | string | no | Filter by site name |
| status | string | no | `active`, `planned`, `retired` |
| region | string | no | Filter by region name |
| tenant | string | no | Filter by tenant |
| limit | int | no | Max results (default: 25) |

**Returns**: Sites with id, name, slug, status, region, address, latitude, longitude, asn.

---

#### `netbox_get_vlans`

Query VLANs.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| vid | int | no | Filter by VLAN ID number |
| name | string | no | Filter by VLAN name |
| site | string | no | Filter by site |
| group | string | no | Filter by VLAN group |
| status | string | no | `active`, `reserved`, `deprecated` |
| tenant | string | no | Filter by tenant |
| limit | int | no | Max results (default: 25) |

**Returns**: VLANs with id (numeric VID), name, status, site, group, tenant, description.

---

#### `netbox_get_interfaces`

Query device interfaces.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| device | string | no | Filter by device name (required unless vm is set) |
| vm | string | no | Filter by virtual machine name |
| name | string | no | Filter by interface name |
| type | string | no | Interface type slug (e.g. `1000base-t`) |
| enabled | bool | no | Filter by enabled status |
| limit | int | no | Max results (default: 50) |

**Returns**: Interfaces with name, type, enabled, MTU, MAC address, IP addresses, connected endpoint.

---

#### `netbox_introspect`

Get GraphQL schema information for a Netbox type. Useful for discovering available fields and building custom queries.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| type_name | string | no | Type name to inspect (e.g. `DeviceType`). If omitted, lists all query root fields. |

**Executes**: Standard GraphQL introspection query filtered to the requested type.

**Returns**: Fields with name, type, and description.

---

### File Structure

```
internal/mcpserver/netbox/
└── server.go

cmd/mcp-netbox/
└── main.go
```

### Implementation Notes

- Netbox GraphQL filter syntax: filters are objects with field names matching the model. Example: `filters: {name: {i_contains: "switch"}, status: {value: "active"}}`.
- The Netbox GraphQL schema uses `_list` suffix for paginated queries: `device_list`, `ip_address_list`, etc.
- Each convenience tool constructs a targeted GraphQL query — don't return entire device objects; select only the fields named in the "Returns" section.
- For `netbox_query`, the raw response is returned as pretty-printed JSON. This gives maximum flexibility.
- `netbox_introspect` uses the standard GraphQL introspection query: `{ __type(name: "TypeName") { fields { name type { name } description } } }`.
- Netbox version: GraphQL schema varies by Netbox version. Target Netbox 3.x. The filter syntax changed significantly between v2 and v3.

---

## 5. `mcp-promql`

**Purpose**: Query Prometheus-compatible monitoring systems using PromQL. Supports both instant and range queries, metric discovery, target inspection, and alert/rule retrieval. Works with Prometheus, Grafana Mimir, Thanos, VictoriaMetrics, and any system implementing the Prometheus HTTP API v1.

**Auth**: Optional — supports bearer token, HTTP Basic Auth, or unauthenticated.

**Base URL**: `http://prometheus:9090` (no `/api/v1` suffix — server appends API paths)

**Go dependencies**: Standard library only

### Config Flags

```
-base-url   string   Prometheus-compatible API base URL (required)
-token      string   Bearer token for authentication (optional)
-username   string   Basic auth username (optional)
-password   string   Basic auth password (optional)
-org-id     string   X-Scope-OrgID header for Grafana Mimir/Cortex multi-tenancy (optional)
-timeout    int      HTTP timeout in seconds (default: 30)
-log        string   Log file path (default: stderr)
```

**Auth precedence**: Bearer token takes priority over basic auth.

### Tools

---

#### `promql_query`

Execute an instant PromQL query.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| query | string | yes | PromQL expression |
| time | string | no | Evaluation timestamp, RFC3339 or Unix timestamp (default: now) |
| timeout | string | no | Query execution timeout, e.g. `30s` (default: server default) |

**API**: `GET /api/v1/query?query={expr}&time={t}`

**Returns**: Result type (`vector`, `scalar`, `matrix`, `string`), and formatted samples with labels and values. For vectors, formats each series as `{label_set} => value`.

---

#### `promql_query_range`

Execute a PromQL range query and return time series data.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| query | string | yes | PromQL expression |
| start | string | yes | Start time, RFC3339 or Unix timestamp |
| end | string | yes | End time, RFC3339 or Unix timestamp |
| step | string | yes | Query resolution step, e.g. `15s`, `1m`, `5m` |
| timeout | string | no | Query execution timeout |

**API**: `GET /api/v1/query_range?query={expr}&start={start}&end={end}&step={step}`

**Returns**: For each series: label set, and data points as `timestamp => value` pairs. Long time series are summarized (show first/last/count of points, min/max/avg values).

**Note**: Range queries can return large amounts of data. Automatically summarize if >100 data points per series.

---

#### `promql_list_metrics`

List all available metric names.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| match | string | no | Filter metric names containing this string |
| limit | int | no | Max results to return (default: 100) |

**API**: `GET /api/v1/label/__name__/values`

**Returns**: Sorted list of metric names, optionally filtered. Shows count of total available metrics.

---

#### `promql_get_labels`

Get all label names present in the time series database.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| match | string | no | Series selector to scope label search, e.g. `{job="prometheus"}` |
| start | string | no | Start time for scope |
| end | string | no | End time for scope |

**API**: `GET /api/v1/labels`

**Returns**: Sorted list of label names.

---

#### `promql_get_label_values`

Get all values for a specific label.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| label | string | yes | Label name |
| match | string | no | Series selector to scope values, e.g. `{job="node_exporter"}` |
| limit | int | no | Max results (default: 100) |

**API**: `GET /api/v1/label/{label}/values`

**Returns**: Sorted list of values for the label.

---

#### `promql_find_series`

Find all time series matching a selector.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| match | string | yes | Series selector, e.g. `{__name__=~"go_.*", job="prometheus"}` |
| start | string | no | Start time |
| end | string | no | End time |
| limit | int | no | Max results (default: 50) |

**API**: `GET /api/v1/series?match[]={selector}`

**Returns**: List of matching label sets (one per series).

---

#### `promql_get_targets`

Get all scrape targets and their current state.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| state | string | no | Filter by state: `active`, `dropped` (default: `active`) |
| job | string | no | Filter by job label value |

**API**: `GET /api/v1/targets`

**Returns**: Targets with job, instance, health (up/down/unknown), last scrape time, last error, labels.

---

#### `promql_get_alerts`

Get currently firing alerts.

**Parameters**: None

**API**: `GET /api/v1/alerts`

**Returns**: Firing alerts with name, labels, annotations, state, active_at timestamp.

---

#### `promql_get_rules`

Get alerting and recording rules.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| type | string | no | `alert` or `record` (default: both) |
| rule_name | string | no | Filter by rule name (partial match) |

**API**: `GET /api/v1/rules`

**Returns**: Rule groups with rules. Alerting rules show expression, duration, labels, annotations, current state. Recording rules show expression and metric name.

---

#### `promql_metric_metadata`

Get metadata (type and help text) for metrics.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| metric | string | no | Metric name (exact). If omitted, returns metadata for all metrics (may be large). |
| limit | int | no | Max metrics to return when no metric name specified (default: 50) |

**API**: `GET /api/v1/metadata?metric={name}`

**Returns**: For each metric: type (counter/gauge/histogram/summary/untyped), help text, unit.

---

### File Structure

```
internal/mcpserver/promql/
└── server.go

cmd/mcp-promql/
└── main.go
```

### Implementation Notes

- The Prometheus HTTP API returns results in an `{"status":"success","data":{...}}` envelope. Always check `status` field; on error, return the `error` and `errorType` fields.
- Instant query result types: `vector` (most common), `scalar`, `matrix` (shouldn't appear for instant queries), `string`.
- Range query data can be very large. Apply automatic summarization for series with many data points.
- Thanos and Mimir add extra headers (`X-Thanos-Tenant`, `X-Scope-OrgID`) — the `-org-id` flag handles the Mimir/Cortex case.
- VictoriaMetrics is fully compatible with the Prometheus HTTP API at v1.
- Time formats: Prometheus accepts Unix timestamps (float) and RFC3339. The server should accept both and normalize to Unix for the API.
- PromQL is complex; the server should not validate PromQL syntax — pass it through and return API errors verbatim.

---

## 6. `mcp-graphql`

**Purpose**: Generic GraphQL client for any endpoint. Useful for APIs with GraphQL interfaces where no dedicated MCP server exists. Supports arbitrary queries, mutations, schema introspection, and type inspection.

**Auth**: Via configurable headers — supports any auth scheme (Bearer, Basic, API key headers, etc.).

**Go dependencies**: Standard library only

### Config Flags

```
-endpoint           string   GraphQL endpoint URL (required)
-header             string   HTTP header in "Name: Value" format; repeat for multiple headers
-timeout            int      HTTP timeout in seconds (default: 30)
-allow-mutations    bool     Enable graphql_mutate tool (default: true)
-allow-introspect   bool     Enable graphql_introspect and graphql_describe_type tools (default: true)
-max-response-bytes int      Max response size in bytes (default: 5MB)
-log                string   Log file path (default: stderr)
```

The `-header` flag is repeatable: `-header "Authorization: Bearer token" -header "X-Api-Key: key"`.

Multiple header instances are supported by using a string slice in the flag parsing (see implementation notes).

### Security

- Private IP blocking (same as `mcp-fetch`/`mcp-http`) — prevents SSRF
- Response size limit (configurable, default 5MB — larger than other servers since GraphQL responses can be big)
- Mutations can be disabled via flag for read-only deployments
- Introspection can be disabled if the server disables it (fail gracefully) or for security hardening

### Tools

---

#### `graphql_query`

Execute a GraphQL query.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| query | string | yes | GraphQL query document |
| variables | string | no | JSON object of query variables |
| operation_name | string | no | Operation name (for documents with multiple operations) |

**API**: `POST {endpoint}` with `{"query":..., "variables":..., "operationName":...}`

**Returns**: Pretty-printed `data` field. If `errors` are present, lists them alongside data (partial responses are valid in GraphQL).

---

#### `graphql_mutate`

Execute a GraphQL mutation. Disabled if `-allow-mutations=false`.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| mutation | string | yes | GraphQL mutation document (must start with `mutation`) |
| variables | string | no | JSON object of mutation variables |
| operation_name | string | no | Operation name |

**Note**: The server validates that the document starts with `mutation` keyword (after stripping comments and whitespace) to prevent accidentally running a query through the mutation tool.

**Returns**: Same format as `graphql_query`.

---

#### `graphql_introspect`

Run a full schema introspection query and return the type system.

**Parameters**: None

**Executes**: Standard GraphQL introspection query (`__schema { types { ... } }`). Returns the full schema document formatted as a human-readable type listing.

**Returns**: All named types with their fields (excluding built-in GraphQL scalar/introspection types). Grouped by type kind (OBJECT, INPUT_OBJECT, ENUM, INTERFACE, UNION, SCALAR).

**Note**: Disabled if `-allow-introspect=false`.

---

#### `graphql_describe_type`

Get detailed information about a specific GraphQL type.

**Parameters**:
| Name | Type | Required | Description |
|---|---|---|---|
| type_name | string | yes | Type name to describe (case-sensitive) |

**Executes**: `{ __type(name: "TypeName") { kind name description fields { name type { name kind ofType { name kind } } description isDeprecated } enumValues { name description } } }`

**Returns**: Type kind, description, all fields with types and descriptions, enum values if applicable, interface implementations.

**Note**: Disabled if `-allow-introspect=false`.

---

### File Structure

```
internal/mcpserver/graphql/
└── server.go

cmd/mcp-graphql/
└── main.go
```

### Implementation Notes

- The `-header` flag uses a custom `flag.Value` implementation to support multiple values: define a `stringSlice` type with `String()` and `Set()` methods.
- Parse headers from `"Name: Value"` format. Split on the first `: ` only (values may contain `:`).
- For `graphql_mutate`, validate the operation type by parsing the start of the document. The check can be simple: trim leading whitespace/comments, check if the first non-comment keyword is `mutation`.
- GraphQL `errors` field: a response can have both `data` and `errors` (partial success). Display errors as warnings, not failures, unless `data` is null.
- The introspection query result is large. Filter out built-in types (starting with `__` and the 5 scalar types: String, Int, Float, Boolean, ID) before returning.
- Variables: accept as a JSON string in the parameter. The server parses and re-serializes to validate before sending.
- Response size: GraphQL APIs can return very large responses (especially introspection). Default to 5MB limit, configurable.

---

## Implementation Order and Dependencies

### Recommended Build Order

1. **`mcp-graphql`** (first) — Establishes the GraphQL-over-HTTP pattern used by `mcp-netbox`. No external API dependencies to test against.

2. **`mcp-netbox`** (second) — Uses GraphQL client pattern from above. Good test if you have a Netbox instance.

3. **`mcp-promql`** (third) — Pure REST, no dependencies on other new servers. Easy to test locally with any Prometheus instance.

4. **`mcp-zulip`** (fourth) — Pure REST with Basic Auth. Easy to test; complements the existing Zulip bridge.

5. **`mcp-trello`** (fifth) — REST with query-param auth. Straightforward.

6. **`mcp-gitlab`** (last) — Most complex due to number of tools and resource types. Build after patterns are established.

### Shared Code Opportunity

Before starting, consider extracting from `mcp-fetch`/`mcp-http` into `internal/mcpserver/httpclient/`:
- `NewSecureClient(timeout time.Duration) *http.Client` — client with private IP blocking dial context
- This package is optional; servers can also copy the pattern inline

The convenience vs. overhead tradeoff: with 6 new servers all making HTTP calls, the shared package pays for itself.

---

## Makefile Additions

Add these build targets to the Makefile:

```makefile
build-mcp-trello:
	$(GOBUILD) -o bin/debug/mcp-trello ./cmd/mcp-trello

build-mcp-gitlab:
	$(GOBUILD) -o bin/debug/mcp-gitlab ./cmd/mcp-gitlab

build-mcp-zulip:
	$(GOBUILD) -o bin/debug/mcp-zulip ./cmd/mcp-zulip

build-mcp-netbox:
	$(GOBUILD) -o bin/debug/mcp-netbox ./cmd/mcp-netbox

build-mcp-promql:
	$(GOBUILD) -o bin/debug/mcp-promql ./cmd/mcp-promql

build-mcp-graphql:
	$(GOBUILD) -o bin/debug/mcp-graphql ./cmd/mcp-graphql
```

And update the `build-mcp-all` target to include these.

---

## Config Examples

These servers will typically be configured in `~/.config/stoa/config.yaml`:

```yaml
mcp:
  servers:
    - name: trello
      command: /path/to/bin/mcp-trello
      args: ["-api-key", "your-key", "-token", "your-token"]

    - name: gitlab
      command: /path/to/bin/mcp-gitlab
      args: ["-token", "glpat-...", "-default-project", "mygroup/myproject"]

    - name: zulip
      command: /path/to/bin/mcp-zulip
      args: ["-base-url", "https://yourorg.zulipchat.com", "-email", "bot@yourorg.com", "-api-key", "your-api-key"]

    - name: netbox
      command: /path/to/bin/mcp-netbox
      args: ["-base-url", "https://netbox.example.com", "-token", "your-token"]

    - name: prometheus
      command: /path/to/bin/mcp-promql
      args: ["-base-url", "http://prometheus:9090"]

    - name: internal-api
      command: /path/to/bin/mcp-graphql
      args:
        - "-endpoint"
        - "https://api.example.com/graphql"
        - "-header"
        - "Authorization: Bearer your-token"
```

---

## Tool Count Summary

| Server | Tools | Difficulty |
|---|---|---|
| `mcp-trello` | 10 | Medium |
| `mcp-gitlab` | 16 | High (most resource types) |
| `mcp-zulip` | 11 | Medium |
| `mcp-netbox` | 8 | Medium (GraphQL adds complexity) |
| `mcp-promql` | 9 | Low-Medium (REST only, no writes) |
| `mcp-graphql` | 4 | Low (generic pass-through) |
| **Total** | **58** | |
