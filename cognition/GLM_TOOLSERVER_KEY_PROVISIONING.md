# How-To: GLM Toolserver Key Provisioning (for Humans + LLM Agents)

This document explains how to **create, rotate, revoke, and use** `client_id` + `api_key` credentials for `glm-toolserver`.

## What exists today
- Tool endpoints require **client auth**:
  - `Authorization: Bearer <api_key>`
  - `X-Client-Id: <client_id>`
- Key provisioning is available via **admin endpoints**:
  - `GET /admin/clients`
  - `POST /admin/clients`
  - `POST /admin/clients/{client_id}/rotate`
  - `DELETE /admin/clients/{client_id}`

## Security model (read this first)
- Admin endpoints are enabled only if `GLM_ADMIN_TOKEN` is set.
- Admin endpoints are **localhost-only by default** (`GLM_ADMIN_ALLOW_REMOTE=false`).
- Admin endpoints use **admin auth**, separate from client auth:
  - `Authorization: Bearer $GLM_ADMIN_TOKEN`

**Do not paste `api_key` or `GLM_ADMIN_TOKEN` into chats, logs, tickets, or PRs.**

## Where keys are stored
- Keys are persisted to the YAML file configured by:
  - `API_KEYS_FILE` (default: `./examples/api_keys.yaml`)

The server writes the file **atomically** and updates its in-memory key store immediately.

## Admin operations (run from the server)
All examples assume the toolserver is local at:

```bash
BASE_URL=http://127.0.0.1:8081
```

Load the admin token from the server env file:

```bash
export GLM_ADMIN_TOKEN="$(grep '^GLM_ADMIN_TOKEN=' /root/glm-toolserver/.env | head -n1 | cut -d= -f2-)"
```

### 1) List clients (no secrets)

```bash
curl -sS \
  -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  "$BASE_URL/admin/clients"
```

### 2) Create a client (returns `api_key` once)
Create with an auto-generated client id:

```bash
curl -sS \
  -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{}' \
  "$BASE_URL/admin/clients"
```

Optionally provide your own client id:

```bash
curl -sS \
  -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"client-myteam-prod"}' \
  "$BASE_URL/admin/clients"
```

Expected response (save this securely):

```json
{
  "client_id": "client-...",
  "api_key": "..."
}
```

### 3) Rotate a client key (returns new `api_key` once)

```bash
CLIENT_ID='client-...'
curl -sS \
  -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  -X POST \
  "$BASE_URL/admin/clients/$CLIENT_ID/rotate"
```

### 4) Revoke a client

```bash
CLIENT_ID='client-...'
curl -sS \
  -H "Authorization: Bearer $GLM_ADMIN_TOKEN" \
  -X DELETE \
  -o /dev/null -w '%{http_code}\n' \
  "$BASE_URL/admin/clients/$CLIENT_ID"
```

## Using credentials (client auth)
For **tool calls**, always send both headers:

```bash
export GLM_CLIENT_ID='client-...'
export GLM_API_KEY='...'

curl -sS \
  -H "Authorization: Bearer $GLM_API_KEY" \
  -H "X-Client-Id: $GLM_CLIENT_ID" \
  -H 'Content-Type: application/json' \
  -d '{"query":"hello","max_results":3}' \
  "$BASE_URL/tools/web_search"
```

## LLM-Agent guidance (operational rules)
When acting as an agent:
1. **Never echo secrets** (`GLM_ADMIN_TOKEN`, `GLM_API_KEY`) back to the user or into logs.
2. Prefer environment variables:
   - `GLM_CLIENT_ID`, `GLM_API_KEY` for normal tool usage.
   - `GLM_ADMIN_TOKEN` only when provisioning/rotating/revoking.
3. Use admin endpoints only from localhost unless the operator explicitly enables remote admin.
4. After provisioning, use the new credentials consistently:
   - `Authorization: Bearer $GLM_API_KEY`
   - `X-Client-Id: $GLM_CLIENT_ID`

## Troubleshooting
- `401 Unauthorized` on tools: missing/invalid `Authorization` or `X-Client-Id`.
- `401 Unauthorized` on admin: missing/invalid `GLM_ADMIN_TOKEN`.
- `403 Forbidden` on admin: you’re not calling from localhost and `GLM_ADMIN_ALLOW_REMOTE` is false.
- Confirm the server is reachable:
  - `curl -sS $BASE_URL/healthz`
  - `curl -sS $BASE_URL/openapi.json | head`

