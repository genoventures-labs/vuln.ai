# LLM Tooling External Access Guide

## System Overview

**Server IP:** `85.31.233.157`  
**Status:** ✅ **Configured for external access**

This VPS hosts multiple LLM tooling services that agents and external applications can use:

### Available Services

| Service | Port | Status | Purpose |
|---------|------|--------|---------|
| **Ollama** | 11434 | ✅ External | LLM inference engine with 8 models |
| **OllamaBench API** | 8000 | ✅ External | Benchmarking, model management, automation |
| **TALOS Tool Server** | 8081 | ✅ External | Web search, fetch, vector retrieval, code sandbox |
| **Open WebUI** | 8080 | 🔒 Internal | Web interface (via Caddy proxy at chat.thynaptic.com) |

---

## Service Details

### 1. Ollama (Port 11434)

**Base URL:** `http://85.31.233.157:11434`

The main LLM inference engine providing OpenAI-compatible API endpoints.

#### Available Models
- qcwind/qwen3-8b-instruct-Q4-K-M (5.2GB)
- llama3:latest (4.7GB)
- mistral:7b (4.4GB)
- gemma2:2b (1.6GB)
- phi3:mini (2.2GB)
- qwen3:4b (2.5GB)
- qwen2.5:3b-instruct (1.9GB)
- llama3.2:1b (1.3GB)

#### Quick Examples

**List models:**
```bash
curl http://85.31.233.157:11434/api/tags
```

**Generate completion:**
```bash
curl http://85.31.233.157:11434/api/generate -d '{
  "model": "llama3",
  "prompt": "Explain quantum computing in simple terms",
  "stream": false
}'
```

**Chat (OpenAI-compatible):**
```bash
curl http://85.31.233.157:11434/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "llama3",
  "messages": [{"role": "user", "content": "Hello!"}]
}'
```

**Python Example:**
```python
import ollama

client = ollama.Client(host='http://85.31.233.157:11434')
response = client.chat(model='llama3', messages=[
  {'role': 'user', 'content': 'What is the meaning of life?'}
])
print(response['message']['content'])
```

---

### 2. OllamaBench API (Port 8000)

**Base URL:** `http://85.31.233.157:8000`  
**Docs:** `http://85.31.233.157:8000/docs` (Interactive Swagger UI)  
**OpenAPI Spec:** `http://85.31.233.157:8000/openapi.json`

A comprehensive benchmarking and model management API built with FastAPI.

#### Key Features
- Model discovery and management
- Automated benchmarking with profiles
- Performance metrics (latency, throughput, percentiles)
- Resource monitoring (CPU, memory)
- Benchmark queue and automation
- History tracking and comparison
- Model registry integration

#### Core Endpoints

**Health Check:**
```bash
curl http://85.31.233.157:8000/api/health
# {"status":"healthy"}
```

**List Models:**
```bash
curl http://85.31.233.157:8000/api/models
```

**Get Model Info:**
```bash
curl http://85.31.233.157:8000/api/models/llama3/info
```

**Run Benchmark:**
```bash
curl -X POST http://85.31.233.157:8000/api/benchmark/run \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama3",
    "profile": "quick",
    "iterations": 3
  }'
```

**Get Benchmark History:**
```bash
curl http://85.31.233.157:8000/api/benchmark/history
```

**Queue Model for Benchmarking:**
```bash
curl -X POST "http://85.31.233.157:8000/api/models/automation/benchmark-queue/llama3?profile=standard&priority=high"
```

**Start Benchmark Queue:**
```bash
curl -X POST http://85.31.233.157:8000/api/models/automation/benchmark-queue/start
```

#### Python Example
```python
import requests

base_url = "http://85.31.233.157:8000"

# Run a benchmark
response = requests.post(f"{base_url}/api/benchmark/run", json={
    "model": "llama3",
    "profile": "quick",
    "iterations": 5
})
benchmark_result = response.json()

print(f"Average response time: {benchmark_result['avg_response_time']}ms")
print(f"P95 latency: {benchmark_result['p95_latency']}ms")
```

#### JavaScript/TypeScript Example
```typescript
const baseUrl = 'http://85.31.233.157:8000';

// List available models
async function listModels() {
  const response = await fetch(`${baseUrl}/api/models`);
  const models = await response.json();
  return models;
}

// Run benchmark
async function runBenchmark(model: string) {
  const response = await fetch(`${baseUrl}/api/benchmark/run`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model,
      profile: 'quick',
      iterations: 3
    })
  });
  return await response.json();
}
```

---

### 3. TALOS Tool Server (Port 8081)

**Base URL:** `http://85.31.233.157:8081`  
**Docs:** `http://85.31.233.157:8081/docs` (Swagger UI)  
**OpenAPI Spec:** `http://85.31.233.157:8081/openapi.json`

A Go-based tool server providing advanced capabilities for LLM agents.

#### Authentication Required

All requests must include:
```bash
Authorization: Bearer <api_key>
X-Client-Id: <client_id>
```

**Note:** API keys are configured server-side in `/root/glm-toolserver/examples/api_keys.yaml`. Contact the server administrator for credentials.

#### Available Tools

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/healthz` | GET | Health check |
| `/version` | GET | Server version |
| `/tools/web_search` | POST | Brave Search API integration |
| `/tools/fetch_url` | POST | Fetch content from URLs |
| `/tools/http_request` | POST | Make arbitrary HTTP requests |
| `/tools/vector_retrieve` | POST | Semantic search via PocketBase + embeddings |
| `/tools/code_exec_sandbox` | POST | Execute code in Docker sandbox |

#### Examples

**Health Check:**
```bash
curl http://85.31.233.157:8081/healthz \
  -H "X-Client-Id: your-client-id"
```

**Web Search:**
```bash
curl -X POST http://85.31.233.157:8081/tools/web_search \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "X-Client-Id: YOUR_CLIENT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "latest developments in quantum computing",
    "count": 5
  }'
```

**Fetch URL:**
```bash
curl -X POST http://85.31.233.157:8081/tools/fetch_url \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "X-Client-Id: YOUR_CLIENT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com/article",
    "format": "markdown"
  }'
```

**Vector Retrieve (Semantic Search):**
```bash
curl -X POST http://85.31.233.157:8081/tools/vector_retrieve \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "X-Client-Id: YOUR_CLIENT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "how to optimize LLM performance",
    "limit": 10,
    "collection": "documentation"
  }'
```

**Code Execution Sandbox:**
```bash
curl -X POST http://85.31.233.157:8081/tools/code_exec_sandbox \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "X-Client-Id: YOUR_CLIENT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "language": "python",
    "code": "print(sum(range(1, 101)))",
    "timeout": 10
  }'
```

#### Python Client Example
```python
import requests

class GLMToolClient:
    def __init__(self, base_url, api_key, client_id):
        self.base_url = base_url
        self.headers = {
            "Authorization": f"Bearer {api_key}",
            "X-Client-Id": client_id,
            "Content-Type": "application/json"
        }
    
    def web_search(self, query, count=5):
        response = requests.post(
            f"{self.base_url}/tools/web_search",
            headers=self.headers,
            json={"query": query, "count": count}
        )
        return response.json()
    
    def fetch_url(self, url, format="text"):
        response = requests.post(
            f"{self.base_url}/tools/fetch_url",
            headers=self.headers,
            json={"url": url, "format": format}
        )
        return response.json()

# Usage
client = GLMToolClient(
    "http://85.31.233.157:8081",
    "your-api-key",
    "your-client-id"
)

results = client.web_search("AI trends 2026")
```

---

## Integration Examples

### LangChain Integration

```python
from langchain_community.llms import Ollama
from langchain.agents import Tool, initialize_agent, AgentType
import requests

# Setup Ollama LLM
llm = Ollama(
    base_url="http://85.31.233.157:11434",
    model="llama3"
)

# Create tools from the TALOS Tool Server
def web_search(query: str) -> str:
    """Search the web using Brave Search."""
    response = requests.post(
        "http://85.31.233.157:8081/tools/web_search",
        headers={
            "Authorization": "Bearer YOUR_API_KEY",
            "X-Client-Id": "YOUR_CLIENT_ID"
        },
        json={"query": query, "count": 5}
    )
    return str(response.json())

# Create LangChain agent with tools
tools = [
    Tool(
        name="Web Search",
        func=web_search,
        description="Search the web for current information"
    )
]

agent = initialize_agent(
    tools, llm, agent=AgentType.ZERO_SHOT_REACT_DESCRIPTION, verbose=True
)

# Use the agent
result = agent.run("What are the latest AI breakthroughs in 2026?")
```

### Continue.dev Configuration

Add to your `.continue/config.json`:

```json
{
  "models": [
    {
      "title": "Remote Llama3",
      "provider": "ollama",
      "model": "llama3",
      "apiBase": "http://85.31.233.157:11434"
    },
    {
      "title": "Remote Mistral 7B",
      "provider": "ollama",
      "model": "mistral:7b",
      "apiBase": "http://85.31.233.157:11434"
    }
  ]
}
```

### Cursor IDE Configuration

In Cursor settings:

```json
{
  "ollama.baseUrl": "http://85.31.233.157:11434",
  "ollama.models": [
    "llama3",
    "mistral:7b",
    "qwen3:4b"
  ]
}
```

### Custom Agent Integration

```python
class RemoteLLMAgent:
    """Agent that uses remote Ollama and tooling services."""
    
    def __init__(self):
        self.ollama_url = "http://85.31.233.157:11434"
        self.bench_url = "http://85.31.233.157:8000"
        self.tools_url = "http://85.31.233.157:8081"
        self.tools_headers = {
            "Authorization": "Bearer YOUR_API_KEY",
            "X-Client-Id": "YOUR_CLIENT_ID"
        }
    
    def chat(self, message, model="llama3"):
        """Send chat message to Ollama."""
        response = requests.post(
            f"{self.ollama_url}/api/generate",
            json={"model": model, "prompt": message, "stream": False}
        )
        return response.json()['response']
    
    def search_web(self, query):
        """Search the web via TALOS Tool Server."""
        response = requests.post(
            f"{self.tools_url}/tools/web_search",
            headers=self.tools_headers,
            json={"query": query}
        )
        return response.json()
    
    def benchmark_model(self, model):
        """Run benchmark on a model."""
        response = requests.post(
            f"{self.bench_url}/api/benchmark/run",
            json={"model": model, "profile": "quick"}
        )
        return response.json()

# Usage
agent = RemoteLLMAgent()
response = agent.chat("Explain neural networks")
search_results = agent.search_web("latest AI papers")
benchmark = agent.benchmark_model("llama3")
```

---

## Current Configuration

### Service Status

All services are running via systemd and configured for external access:

```bash
# Check service status
systemctl status ollama ollamabench-api glm-toolserver

# View logs
journalctl -u ollama -f
journalctl -u ollamabench-api -f
journalctl -u glm-toolserver -f
```

### Configuration Files

| Service | Config Location |
|---------|----------------|
| Ollama | `/etc/systemd/system/ollama.service.d/override.conf` |
| OllamaBench API | `/etc/systemd/system/ollamabench-api.service` |
| TALOS Tool Server | `/root/glm-toolserver/.env` |

### Ollama Configuration
```ini
[Service]
Environment="OLLAMA_NUM_PARALLEL=2"
Environment="OLLAMA_MAX_QUEUE=64"
Environment="OLLAMA_KEEP_ALIVE=15m"
Environment="OLLAMA_MAX_LOADED_MODELS=2"
Environment="OLLAMA_HOST=0.0.0.0:11434"
```

### OllamaBench API Configuration
```ini
[Service]
User=mike
WorkingDirectory=/home/mike/projects/OllamaBench/api
Environment=OLLAMA_HOST=http://127.0.0.1:11434
ExecStart=/home/mike/projects/OllamaBench/venv/bin/python -m uvicorn main:app --host 0.0.0.0 --port 8000
```

### TALOS Tool Server Configuration
```ini
PORT=8081
BIND_ADDR=0.0.0.0:8081
BRAVE_API_KEY=BSA***
OLLAMA_OPENAI_BASE_URL=http://localhost:11434/v1
POCKETBASE_URL=https://pocketbase.thynaptic.com
```

### Firewall Configuration

The following ports are open:

```bash
sudo ufw status
# 22/tcp    - SSH
# 80/tcp    - HTTP (Caddy)
# 443/tcp   - HTTPS (Caddy)
# 8000/tcp  - OllamaBench API
# 8081/tcp  - TALOS Tool Server
# 11434/tcp - Ollama
```

---

## Security Considerations

### ⚠️ Important Notes

1. **Ollama - No Built-in Authentication:**
   - Anyone with network access can use the models
   - Consider using a reverse proxy with authentication
   - Monitor usage for abuse

2. **OllamaBench API - Currently No Auth:**
   - Open API for benchmarking and model management
   - Consider implementing API key authentication for production
   - Rate limiting recommended

3. **TALOS Tool Server - Authentication Required:**
   - ✅ Requires API key and client ID
   - Keys configured in `/root/glm-toolserver/examples/api_keys.yaml`
   - Contact administrator for credentials

### Recommended Security Measures

**1. IP Allowlisting (if serving specific clients):**
```bash
# Allow only from specific IP ranges
sudo ufw delete allow 8000/tcp
sudo ufw delete allow 8081/tcp
sudo ufw delete allow 11434/tcp

sudo ufw allow from YOUR_IP_ADDRESS to any port 8000
sudo ufw allow from YOUR_IP_ADDRESS to any port 8081
sudo ufw allow from YOUR_IP_ADDRESS to any port 11434
```

**2. Reverse Proxy with Authentication (Nginx example):**
```nginx
server {
    listen 11435;
    server_name your-domain.com;

    auth_basic "Ollama Access";
    auth_basic_user_file /etc/nginx/.htpasswd;

    location / {
        proxy_pass http://127.0.0.1:11434;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

**3. Rate Limiting:**
```bash
# Use iptables to limit connections
sudo iptables -A INPUT -p tcp --dport 11434 -m limit --limit 100/min -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 11434 -j DROP
```

**4. Monitoring:**
```bash
# Monitor active connections
watch -n 2 'ss -tn | grep -E "(8000|8081|11434)"'

# Monitor resource usage
htop
ollama ps
```

---

## Performance Tuning

### Current Ollama Settings

- **Parallel Requests:** 2 (balanced for VPS)
- **Max Queue:** 64 (handles burst traffic)
- **Keep Alive:** 15 minutes (model stays loaded)
- **Max Loaded Models:** 2 (prevents OOM)

### Recommended Per-Use Case

**High Throughput (multiple concurrent users):**
```ini
Environment="OLLAMA_NUM_PARALLEL=4"
Environment="OLLAMA_MAX_QUEUE=128"
Environment="OLLAMA_MAX_LOADED_MODELS=1"
```

**Low Latency (single user, fast response):**
```ini
Environment="OLLAMA_NUM_PARALLEL=1"
Environment="OLLAMA_KEEP_ALIVE=60m"
Environment="OLLAMA_MAX_LOADED_MODELS=1"
```

**Multi-Model Testing:**
```ini
Environment="OLLAMA_NUM_PARALLEL=2"
Environment="OLLAMA_MAX_LOADED_MODELS=3"
Environment="OLLAMA_KEEP_ALIVE=30m"
```

---

## Troubleshooting

### Connection Issues

**Test connectivity:**
```bash
# From external machine
curl -v http://85.31.233.157:11434/api/tags
curl -v http://85.31.233.157:8000/api/health
curl -v http://85.31.233.157:8081/healthz -H "X-Client-Id: test"
```

**Check if services are listening:**
```bash
ss -tlnp | grep -E "(8000|8081|11434)"
```

**Check firewall:**
```bash
sudo ufw status verbose
```

### Service Not Responding

**Restart services:**
```bash
sudo systemctl restart ollama
sudo systemctl restart ollamabench-api
sudo systemctl restart glm-toolserver
```

**Check logs:**
```bash
sudo journalctl -u ollama -n 50
sudo journalctl -u ollamabench-api -n 50
sudo journalctl -u glm-toolserver -n 50
```

### High Resource Usage

**Check running models:**
```bash
ollama ps
```

**Check resource usage:**
```bash
htop
free -h
df -h
```

**Unload models if needed:**
```bash
# Models auto-unload after OLLAMA_KEEP_ALIVE period
# Or restart ollama to force unload all
sudo systemctl restart ollama
```

### TALOS Tool Server 401/403 Errors

**Missing authentication:**
```bash
# Make sure to include both headers
curl -X POST http://85.31.233.157:8081/tools/web_search \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "X-Client-Id: YOUR_CLIENT_ID" \
  -H "Content-Type: application/json" \
  -d '{"query": "test"}'
```

**Check API keys configuration:**
```bash
sudo cat /root/glm-toolserver/examples/api_keys.yaml
```

---

## Quick Reference

### Service URLs

```bash
# Ollama
http://85.31.233.157:11434
http://85.31.233.157:11434/api/tags          # List models
http://85.31.233.157:11434/api/generate      # Generate
http://85.31.233.157:11434/v1/chat/completions  # OpenAI-compatible

# OllamaBench API
http://85.31.233.157:8000
http://85.31.233.157:8000/docs               # Interactive docs
http://85.31.233.157:8000/api/health         # Health check
http://85.31.233.157:8000/api/models         # List models
http://85.31.233.157:8000/api/benchmark/run  # Run benchmark

# TALOS Tool Server
http://85.31.233.157:8081
http://85.31.233.157:8081/docs               # Interactive docs
http://85.31.233.157:8081/healthz            # Health (needs X-Client-Id)
http://85.31.233.157:8081/tools/web_search   # Search (auth required)
http://85.31.233.157:8081/tools/fetch_url    # Fetch (auth required)
```

### Common Commands

```bash
# List available models
curl http://85.31.233.157:11434/api/tags

# Quick health check all services
curl -s http://85.31.233.157:11434/api/tags | jq -r '.models[].name'
curl -s http://85.31.233.157:8000/api/health
curl -s http://85.31.233.157:8081/healthz -H "X-Client-Id: test"

# Run benchmark
curl -X POST http://85.31.233.157:8000/api/benchmark/run \
  -H "Content-Type: application/json" \
  -d '{"model": "llama3", "profile": "quick"}'

# Get benchmark history
curl http://85.31.233.157:8000/api/benchmark/history | jq
```

---

## Additional Resources

- **Ollama Documentation:** https://github.com/ollama/ollama/blob/main/docs/api.md
- **OllamaBench:** `/home/mike/projects/OllamaBench/README.md`
- **OllamaBench API Routes:** `/home/mike/projects/OllamaBench/API_ROUTES.md`
- **TALOS Tool Server:** `/root/glm-toolserver/README.md`

---

**Last Updated:** February 14, 2026  
**Server:** 85.31.233.157  
**Status:** ✅ All services configured and accessible externally  
**Services Running:** Ollama (11434), OllamaBench API (8000), TALOS Tool Server (8081)
