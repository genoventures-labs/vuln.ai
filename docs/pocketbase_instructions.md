# PocketBase API  External Integration README

**Base URL**: `https://pocketbase.thynaptic.com`

This guide explains how agents and external apps should authenticate and use the PocketBase API, including service accounts, required headers, and common patterns.

---

## 1) Authentication (Service Accounts)

Use the `service_accounts` auth collection.

**Required env vars:**
```bash
POCKETBASE_URL=https://pocketbase.thynaptic.com
POCKETBASE_AUTH_COLLECTION=service_accounts
POCKETBASE_IDENTITY=cass@thynaptic.com
POCKETBASE_PASSWORD=<SERVICE_PASSWORD>
```

**Get token:**
```bash
curl -s -X POST $POCKETBASE_URL/api/collections/$POCKETBASE_AUTH_COLLECTION/auth-with-password \
  -H "Content-Type: application/json" \
  -d '{
    "identity": "'"$POCKETBASE_IDENTITY"'",
    "password": "'"$POCKETBASE_PASSWORD"'"
  }' | jq -r '.token'
```

**Use token:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  $POCKETBASE_URL/api/collections/tenants/records
```

---

## 2) Access Rules Summary

- **Public access**: disabled for G‑LM collections.
- **Service account access**: allowed via JWT.
- **Roles** (for human users): commander/operator/analyst.

---

## 3) Core Collections (GLM V1)

| Collection | Purpose |
|---|---|
| tenants | Tenant registry |
| projects | Project grouping |
| api_keys | API key storage |
| roles | RBAC roles |
| memberships | User-role assignments |
| model_policies | Model access constraints |
| quotas | Rate limits |
| idempotency_records | Request idempotency |
| audit_events | Audit logs |

---

## 4) Service Accounts

**Collection**: `service_accounts`

Common fields:
- `email`, `password`
- `service_name`, `status`
- `scopes` (JSON array)

**Important scopes used:**
```
.tenants:*  api_keys:*  projects:*  roles:*  memberships:*  model_policies:*  quotas:*
.audit:write  idempotency:write  memory:*  memory:read  memory:write
```

---

## 5) Memory Nodes (AI Memory)

**Collection**: `memory_nodes`

Required fields:
- `key` (text)
- `content` (text)

Common fields:
- `tenant_id` (text)
- `session_id` (text)
- `metadata` (json)
- `embedding` (json)

**Create memory:**
```bash
curl -X POST $POCKETBASE_URL/api/collections/memory_nodes/records \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "TENANT_ID",
    "session_id": "session_123",
    "key": "msg_001",
    "content": "User asked about pricing",
    "metadata": {"source": "gateway"}
  }'
```

**Query by tenant/session/key:**
```bash
curl "$POCKETBASE_URL/api/collections/memory_nodes/records?filter=tenant_id='TENANT_ID'&&session_id='session_123'&&key='msg_001'" \
  -H "Authorization: Bearer $TOKEN"
```

---

## 6) Mesh Collections

### glm_mesh_neighbor
Fields: `url`, `node_id`, `label`, `is_active`, `perms`, `meta`, `last_seen_at`, `last_error`, `created_at`, `updated_at`

### glm_cog_shards
Fields: `embedding`, `title`, `summary`, `text`, `content`, `tags`, `type`, `source`, `node_id`, `node_url`, `node_label`, `metadata`, `created_at`, `updated_at`

Access is **service-account-only** (JWT required).

---

## 7) Common API Patterns

**List records:**
```bash
curl "$POCKETBASE_URL/api/collections/tenants/records?page=1&perPage=20" \
  -H "Authorization: Bearer $TOKEN"
```

**Filter records:**
```bash
curl "$POCKETBASE_URL/api/collections/api_keys/records?filter=status='active'" \
  -H "Authorization: Bearer $TOKEN"
```

**Create record:**
```bash
curl -X POST $POCKETBASE_URL/api/collections/tenants/records \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Acme","status":"active"}'
```

**Update record:**
```bash
curl -X PATCH $POCKETBASE_URL/api/collections/tenants/records/RECORD_ID \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status":"inactive"}'
```

**Delete record:**
```bash
curl -X DELETE $POCKETBASE_URL/api/collections/tenants/records/RECORD_ID \
  -H "Authorization: Bearer $TOKEN"
```

---

## 8) Health Check

```bash
curl $POCKETBASE_URL/api/health
```

---

## 9) Notes

- Timestamps must be RFC3339 (`2026-02-21T04:51:00Z`).
- Use `metadata` JSON for extra fields unless you need indexed queries.
- Service account scopes must include the collections you operate on.

---

## 10) Quick Troubleshooting

**401 Unauthorized** → Token missing/expired, re-authenticate.

**403 Forbidden** → API rules disallow; confirm scopes or role.

**400 Bad Request** → Field mismatch (e.g., timestamp format or missing required fields).

---

**Contact**: PocketBase Admin Team