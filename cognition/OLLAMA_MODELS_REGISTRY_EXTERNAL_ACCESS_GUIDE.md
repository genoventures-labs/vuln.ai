# Ollama Models Registry API (External Access)

## Status
- **Service:** `ollama-models-api`
- **Host:** `85.31.233.157`
- **Port:** `8002`
- **Base URL:** `http://85.31.233.157:8002/api/v1`
- **State:** ✅ Running and externally accessible

---

## What this gives you
This endpoint exposes a searchable registry API for Ollama models:
- `GET /api/v1/models`
- Filtering: `search`, `model_identifier`, `namespace`, `capability`, `model_type`
- Sorting: `sort_by=pulls|last_updated`, `order=asc|desc`
- Pagination: `limit`, `skip`
- Pullable tags: `labels` now contains full pull tags (e.g. `llama3.1:8b-instruct-q4_K_M`)
- Remote machine management:
  - `GET /api/v1/models/local` (list installed models)
  - `POST /api/v1/models/local/pull` (pull model to this machine)
  - `DELETE /api/v1/models/local?model=<tag>` (delete model from this machine)
  - `POST /api/v1/models/local/pull-jobs` (async pull job enqueue)
  - `GET /api/v1/models/local/pull-jobs` (list pull jobs)
  - `GET /api/v1/models/local/pull-jobs/{job_id}` (get job status)
  - `POST /api/v1/models/local/pull-jobs/{job_id}/cancel` (cancel queued/running job)
  - `GET /api/v1/models/local/audit` (management action audit log)

Current registry count on this server: **212 models**.

---

## Quick tests

```bash
# List first 3 models
curl "http://85.31.233.157:8002/api/v1/models?limit=3"

# Search for qwen models
curl "http://85.31.233.157:8002/api/v1/models?search=qwen&limit=5"

# Get full pull tags for a specific model
curl "http://85.31.233.157:8002/api/v1/models?model_identifier=llama3.1&limit=1"

# Community models only
curl "http://85.31.233.157:8002/api/v1/models?model_type=community&limit=10"

# Sort by last update ascending
curl "http://85.31.233.157:8002/api/v1/models?sort_by=last_updated&order=asc&limit=20"
```

## Remote pull/delete/jobs (authenticated)

Management endpoints require `X-API-Key` and are rate-limited (30 req / 60 sec / client IP / endpoint).

```bash
export REGISTRY_READ_KEY='S6k9Fz6ulcFFOrWp0zAYh-ql2mlRy_cL'
export REGISTRY_WRITE_KEY='JJo2U-a3uip3UasdTQALBlfixryxgOGr'
export REGISTRY_ADMIN_KEY='bvGoEe2rhZtzKHeI0mHXFuEmeNA-TiHJChdjvhYHMXE'

# List models currently installed on this VPS
curl "http://85.31.233.157:8002/api/v1/models/local" \
  -H "X-API-Key: ${REGISTRY_READ_KEY}"

# Pull model to this VPS
curl -X POST "http://85.31.233.157:8002/api/v1/models/local/pull" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${REGISTRY_WRITE_KEY}" \
  -d '{"model":"qwen2.5:3b-instruct"}'

# Delete model from this VPS
curl -X DELETE "http://85.31.233.157:8002/api/v1/models/local?model=qwen2.5:3b-instruct" \
  -H "X-API-Key: ${REGISTRY_WRITE_KEY}"

# Enqueue async pull job
curl -X POST "http://85.31.233.157:8002/api/v1/models/local/pull-jobs" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${REGISTRY_WRITE_KEY}" \
  -d '{"model":"llama3.2:1b"}'

# List jobs
curl "http://85.31.233.157:8002/api/v1/models/local/pull-jobs" \
  -H "X-API-Key: ${REGISTRY_READ_KEY}"

# Audit events
curl "http://85.31.233.157:8002/api/v1/models/local/audit?limit=20" \
  -H "X-API-Key: ${REGISTRY_READ_KEY}"
```

---

## Example response shape

```json
{
  "models": [
    {
      "model_identifier": "qwen3",
      "namespace": null,
      "model_name": "qwen3",
      "model_type": "official",
      "description": "Qwen3 is the latest generation...",
      "capability": "Tools",
      "labels": [],
      "pulls": 1900000,
      "tags": 40,
      "last_updated": "2026-02-09",
      "last_updated_str": "5 days ago",
      "url": "https://ollama.com/library/qwen3"
    }
  ],
  "total_count": 17,
  "limit": 5,
  "skip": 0,
  "data_updated": "2026-02-14T21:23:31.123456+00:00"
}
```

---

## How it is implemented on this VPS

- Source repo: `https://github.com/frefrik/ollama-models-api`
- Local path: `/home/mike/projects/ollama-models-api`
- Runtime: Python + FastAPI + Uvicorn
- Service file: `/etc/systemd/system/ollama-models-api.service`
- Firewall: `8002/tcp` allowed via UFW
- Health endpoint: `GET /healthz`
- Metrics endpoint: `GET /metrics` (Prometheus text)
- Request-size guard: `MAX_REQUEST_BYTES=1048576`
- Scoped keys: `MANAGEMENT_READ_API_KEY`, `MANAGEMENT_WRITE_API_KEY`
- Back-compat admin key: `MANAGEMENT_API_KEY`

Because `ollamadb.dev` is not resolvable from this VPS, the API uses a local fallback that scrapes `ollama.com/search` and serves the same API contract. Results are cached in-memory for 1 hour.

---

## Service management

```bash
# status
sudo systemctl status ollama-models-api

# restart
sudo systemctl restart ollama-models-api

# logs
sudo journalctl -u ollama-models-api -f
```

---

## Agent integration examples

### Python
```python
import requests

BASE = "http://85.31.233.157:8002/api/v1/models"
r = requests.get(BASE, params={"search": "qwen", "limit": 10})
data = r.json()
print(data["total_count"])
for m in data["models"]:
    print(m["model_identifier"], m["pulls"])
```

### JavaScript
```javascript
const base = "http://85.31.233.157:8002/api/v1/models";
const res = await fetch(`${base}?search=llama&limit=10`);
const data = await res.json();
console.log(data.total_count, data.models.map(m => m.model_identifier));
```
