# Thynaptic VPS — External Service API (Agent Guide)

This document explains how an agent (or any external service) can safely call the APIs hosted on this VPS.

## Service directory

| Capability | Recommended base URL | Notes / Auth |
|---|---|---|
| OpenWebUI API (OpenAI-style chat/embeddings, plus WebUI routes) | `https://chat.thynaptic.com` | Auth required (JWT or OpenWebUI API key). Prefer HTTPS over direct IP/port. |
| G‑LM Tool Server (agent tools: web search, fetch, HTTP, vector retrieve, sandbox) | `https://chat.thynaptic.com` | Auth required: `Authorization: Bearer <GLM_API_KEY>` + `X-Client-Id: <GLM_CLIENT_ID>`. OpenA
PI at `/openapi.json`. |
| Ollama (raw inference engine) | `http://85.31.233.157:11434` (or on-box: `http://127.0.0.1:11434`) | **No built-in auth.** Use only from trusted networks (VPN/allowlist). Supports `/v1/*` OpenAI-compatible a
nd `/api/*` native endpoints. |
| OllamaBench API (benchmarking/automation) | `http://85.31.233.157:8000` | Swagger UI: `/docs`. Auth depends on the app’s configuration / network controls. |
| Ollama Models API (registry/managed model ops) | `http://85.31.233.157:8002` | Prefer this for pull/delete if it is fronted with auth; avoid exposing raw Ollama management endpoints to the internet. |

## Network and routing (high level)

- Public TLS termination and routing is handled by **Caddy** on ports **80/443**.
- Internally, services typically bind to localhost and are exposed externally only via Caddy.
- A prompt-injection gateway (`promptguard`) also runs on-box and can be placed in front of upstreams as part of the reverse-proxy chain.

### Direct-IP endpoints (trusted network only)

This README includes the VPS public IP for agent automation. Treat direct-IP ports (especially Ollama on `:11434`) like a database port: only use from approved hosts (VPN/firewall allowlist), and prefer `https
://chat.thynaptic.com` whenever possible.

## Authentication model

### 1) OpenWebUI

OpenWebUI supports:

- **JWT** via username/password sign-in
- **API keys** (enabled on this instance)

Use either as:

```http
Authorization: Bearer <token-or-api-key>
```

Never embed credentials in prompts or logs; keep them in environment variables or a secrets manager.

### 2) G‑LM Tool Server

For `/tools/*` endpoints, send both headers:

```http
Authorization: Bearer <GLM_API_KEY>
X-Client-Id: <GLM_CLIENT_ID>
```

For tool discovery, agents should treat the OpenAPI document as the source of truth:

- `GET https://chat.thynaptic.com/openapi.json`
- `GET https://chat.thynaptic.com/docs`

### 3) Ollama (raw)

Ollamas HTTP API is unauthenticated by default. Treat it like a database port:

- Do not share the URL broadly.
- Prefer calling OpenWebUI or a management layer that enforces auth.
- If you must call it directly, limit network access (firewall/VPN/allowlist).

## Quickstart recipes

### A) OpenWebUI: get a JWT (agent login)

```bash
OPENWEBUI_BASE_URL='https://chat.thynaptic.com'

TOKEN=$(curl -sS -X POST "$OPENWEBUI_BASE_URL/api/v1/auths/signin" \
  -H 'Content-Type: application/json' \
  -d '{"email":"YOUR_EMAIL","password":"YOUR_PASSWORD"}' \
  | jq -r '.token')

echo "JWT acquired (length=${#TOKEN})"
```

### B) OpenWebUI: OpenAI-compatible chat completions

```bash
curl -sS "$OPENWEBUI_BASE_URL/api/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "llama3.2:latest",
    "messages": [
      {"role":"system","content":"You are a concise assistant."},
      {"role":"user","content":"Summarize this in one sentence: ..."}
    ],
    "stream": false
  }'
```

### C) G‑LM Tool Server: web search

```bash
GLM_TOOLSERVER_BASE_URL='https://chat.thynaptic.com'

curl -sS "$GLM_TOOLSERVER_BASE_URL/tools/web_search" \
  -H "Authorization: Bearer $GLM_API_KEY" \
  -H "X-Client-Id: $GLM_CLIENT_ID" \
  -H 'Content-Type: application/json' \
  -d '{"query":"latest OpenWebUI release notes","max_results":5}'
```

### D) Ollama: OpenAI-compatible chat (direct)

```bash
OLLAMA_BASE_URL='http://85.31.233.157:11434'

curl -sS "$OLLAMA_BASE_URL/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "llama3.2:latest",
    "messages": [{"role":"user","content":"Hello from an external agent"}],
    "stream": false
  }'
```

## What to use when (agent guidance)

- If you need **tools** (search/fetch/sandbox/vector): use **G‑LM Tool Server**.
- If you need **chat/embeddings with access control and auditing**: use **OpenWebUI**.
- If you need **lowest-latency raw inference** and you are in a trusted network: use **Ollama**.
- If you need **benchmarking / comparisons / automation**: use **OllamaBench API**.

## Troubleshooting

- **401/403**: check you are using the correct Bearer token/API key and that the key is allowed for that endpoint.
- **CORS issues** (browser apps): request that your app’s origin be added to the OpenWebUI `CORS_ALLOW_ORIGIN` allowlist.
- **Timeouts**: prefer smaller/faster models; confirm the selected model set and benchmarking state in `/var/lib/ollama-model-guardian/state.json`.

## Security notes (important)

- Do not expose Ollama management endpoints (`/api/pull`, `/api/delete`) to untrusted callers.
- Do not paste API keys, admin tokens, or customer credentials into prompts, tickets, or chat logs.
- Rotate keys regularly and revoke on compromise.