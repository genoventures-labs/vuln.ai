# GLM Toolserver (G-LM) — TALOS + Agent How-To

`glm-toolserver` is the canonical HTTP tool backend for TALOS and other agents. It exposes an OpenAPI contract (`/openapi.json`) plus both **client tool endpoints** and **admin lifecycle endpoints** (keys + plugins/ToolGen).

## Base URLs

### External (recommended for TALOS/remote agents)
- `GLM_TOOLSERVER_BASE_URL=https://chat.thynaptic.com`
  - Alias: `GLM_TOOL_BASE_URL` (some clients use this name)

### Local (recommended for server-side ops)
- `http://127.0.0.1:8081`

Public probes:
- `GET /healthz`
- `GET /version`
- `GET /openapi.json`
- `GET /docs` (Swagger UI)

## Auth model

### Client auth (ALL `/tools/*` endpoints)
Every tool call MUST include both headers:
- `Authorization: Bearer <GLM_API_KEY>`
- `X-Client-Id: <GLM_CLIENT_ID>`

### Admin auth (ALL `/admin/*` endpoints)
Admin calls use:
- `Authorization: Bearer <GLM_ADMIN_TOKEN>`

By default admin endpoints are localhost-only unless explicitly enabled:
- `GLM_ADMIN_ALLOW_REMOTE=true`

Security rule for TALOS/agents: **never print, log, or echo** `GLM_ADMIN_TOKEN` or any `api_key`.

## TALOS: how to use this toolserver

### 1) Configure environment
Set these in TALOS runtime (NOT in prompts):

```bash
export GLM_TOOLSERVER_BASE_URL='https://chat.thynaptic.com'
export GLM_CLIENT_ID='client-...'
export GLM_API_KEY='...'
```

Optional admin capabilities (only for privileged operators / CI / provisioning bots):

```bash
export GLM_ADMIN_TOKEN='...'
export GLM_ADMIN_ALLOW_REMOTE='true'  # server-side setting; not needed client-side
```

### 2) Discover tools from OpenAPI
TALOS should treat `/openapi.json` as the source of truth for available tools and their JSON schemas.

Example:
```bash
curl -sS "$GLM_TOOLSERVER_BASE_URL/openapi.json" | head
```

### 3) Invoke tools
Tools are under `/tools/*` and accept JSON.

Example (Brave-backed web search):
```bash
curl -sS \
  -H "Authorization: Bearer $GLM_API_KEY" \
  -H "X-Client-Id: $GLM_CLIENT_ID" \
  -H 'Content-Type: application/json' \
  -d '{"query":"site:github.com llama3 tool calling","max_results":3}' \
  "$GLM_TOOLSERVER_BASE_URL/tools/web_search"
```

## Agent playbook: provisioning client keys
Full details: `HOWTO_KEY_PROVISIONING.md`.

Endpoints:
- `GET /admin/clients` (list IDs; no secrets)
- `POST /admin/clients` (create; returns `api_key` once)
- `POST /admin/clients/{client_id}/rotate` (rotate; returns `api_key` once)
- `DELETE /admin/clients/{client_id}` (revoke)

## ToolGen / Plugins (Go adapters)

Plugins are server-generated Go executables, persisted on the VPS, and invoked via a stable tool endpoint.

### Requesting a new tool to be made (TALOS / agent workflow)
If TALOS needs a capability that is not already exposed under `/tools/*`, it should request a plugin be generated via the ToolGen admin API.

**Request body schema** for `POST /admin/plugins`:
- `name` (string): lowercase `[a-z0-9_-]`, max 64 chars (stable tool name)
- `description` (string): short human description
- `requirement` (string): precise instructions for the tool behavior

**Write requirements like this** (agents):
- Define expected input JSON fields and their types
- Define output JSON shape (must be exactly one JSON object)
- Include any external API constraints (hostnames, auth env vars, timeouts)
- Keep it small and deterministic; avoid “do everything” specs

Example tool request:
```bash
curl -sS \
  -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"github_issue_summary",
    "description":"Summarize a GitHub issue thread",
    "publisher":"talos",
    "requirement":"Input: {\\"repo\\":string,\\"issue_number\\":number}. Output: {\\"ok\\":true,\\"summary\\":string,\\"key_points\\":[string]}." 
  }' \
  "$GLM_TOOLSERVER_BASE_URL/admin/plugins"
```

Then: poll job → (auto-enable may happen) → invoke at `POST /tools/plugins/<name>`.

#### Auto-enable rules
If either of these server env vars are set, generated plugins can be auto-enabled after a successful build:
- `GLM_TRUSTED_PLUGIN_PUBLISHERS` (comma-separated publisher ids)
- `GLM_TRUSTED_PLUGIN_PREFIXES` (comma-separated name prefixes)

If the request includes `publisher` and it matches a trusted publisher, the plugin is auto-enabled.
If the plugin `name` starts with a trusted prefix, it is auto-enabled.

Request field:
- `publisher` (optional string): the publisher id used for trusted auto-enable.

### Admin: plugin lifecycle
- `GET /admin/plugins` (list)
- `POST /admin/plugins` (start async generation job)
- `GET /admin/plugins/jobs/{id}` (poll job)
- `POST /admin/plugins/{name}/enable`
- `POST /admin/plugins/{name}/disable`
- `DELETE /admin/plugins/{name}`

### Client: invoke an enabled plugin
- `POST /tools/plugins/{name}`

### Async generation flow (important)
Cloudflare can time out long requests, so generation is async:

```bash
# 1) start job
job=$(curl -sS \
  -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"echo","description":"Echo input","requirement":"Return {\\"ok\\":true,\\"input\\":<original>}"}' \
  "$GLM_TOOLSERVER_BASE_URL/admin/plugins")

# 2) poll
id=$(python3 - <<'PY'
import json,sys
print(json.loads(sys.stdin.read())['id'])
PY
<<<"$job")

curl -sS -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  "$GLM_TOOLSERVER_BASE_URL/admin/plugins/jobs/$id"

# 3) enable
curl -sS -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  -X POST "$GLM_TOOLSERVER_BASE_URL/admin/plugins/echo/enable"

# 4) invoke as a normal client tool
curl -sS \
  -H "Authorization: Bearer $GLM_API_KEY" \
  -H "X-Client-Id: $GLM_CLIENT_ID" \
  -H 'Content-Type: application/json' \
  -d '{"hello":"world"}' \
  "$GLM_TOOLSERVER_BASE_URL/tools/plugins/echo"
```

### Plugin safety constraints (current)
- Generated code must be **stdlib-only** (imports containing `.` are rejected)
- Banned imports include `os/exec`, `syscall`, `unsafe`
- Build runs with module download disabled (`GOPROXY=off`)
- Plugins execute with a timeout and are run as `nobody` when possible

## User-defined skills: preflight (TALOS users)

User-created skills run on user machines, so **all dangerous effects must be routed through server tools**. Before a skill performs any tool calls or external actions, it should request a policy decision from:
- `POST /skills/preflight`

Server-side configuration (env):
- `GLM_PUBLIC_BASE_URL` (optional, used to return absolute URLs)
- `GLM_SKILL_ALLOWED_TOOL_PATHS` (comma-separated allowlist; supports `*` suffix for prefix match)
  - Example: `/tools/web_search,/tools/http_request,/tools/plugins/*`
- `GLM_SKILL_ALLOWED_DOMAINS` (comma-separated host allowlist; supports `*` suffix for prefix match)

Request example:
```bash
curl -sS \
  -H "Authorization: Bearer $GLM_API_KEY" \
  -H "X-Client-Id: $GLM_CLIENT_ID" \
  -H 'Content-Type: application/json' \
  -d '{
    "skill_name":"jira_helper",
    "intent":"Look up a Jira ticket and summarize it",
    "requested_tools":[
      {"kind":"tool","name":"web_search"},
      {"kind":"plugin","name":"echo"}
    ],
    "requested_domains":["api.github.com"]
  }' \
  "$GLM_TOOLSERVER_BASE_URL/skills/preflight"
```

The response contains:
- `decision`: `allow|deny|review`
- `invoke[]`: the exact `/tools/...` paths (and optional absolute URLs) the skill should call
- `limits`: size/timeout guidance

## Configuration reference (server-side)
Common env vars:
- `GLM_ADMIN_TOKEN` (admin auth)
- `GLM_ADMIN_ALLOW_REMOTE` (whether `/admin/*` is reachable beyond localhost)
- `API_KEYS_FILE` (YAML storage)
- `OLLAMA_OPENAI_BASE_URL`, `OLLAMA_EMBED_MODEL`, `OLLAMA_HTTP_TIMEOUT`
- `OLLAMA_MODEL_TOOLGEN` (ToolGen model)
- `PLUGINS_DIR`, `PLUGIN_EXEC_TIMEOUT`
- `GLM_PUBLIC_BASE_URL`, `GLM_SKILL_ALLOWED_TOOL_PATHS`, `GLM_SKILL_ALLOWED_DOMAINS`

## Trusted auto-enable
Server env vars:
- `GLM_TRUSTED_PLUGIN_PUBLISHERS` (comma-separated publisher ids)
- `GLM_TRUSTED_PLUGIN_PREFIXES` (comma-separated plugin name prefixes)

If a plugin generation request includes `publisher` and it matches a trusted publisher, the plugin is auto-enabled after a successful build.
If the plugin `name` starts with a trusted prefix, it is auto-enabled.
