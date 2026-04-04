# Ollama VPS External Access Guide

## Current System Configuration

**Server IP Address:** `85.31.233.157`  
**Ollama Version:** Running via systemd  
**Default Port:** `11434`  
**Current Binding:** `0.0.0.0:11434` ✅ **Configured for external access**

### Available Models
- qcwind/qwen3-8b-instruct-Q4-K-M (5.2GB)
- llama3:latest (4.7GB)
- mistral:7b (4.4GB)
- gemma2:2b (1.6GB)
- phi3:mini (2.2GB)
- qwen3:4b (2.5GB)
- qwen2.5:3b-instruct (1.9GB)
- llama3.2:1b (1.3GB)

---

## ✅ External Access Enabled

Ollama is **now configured and accessible externally** at `http://85.31.233.157:11434`. The firewall has been opened and the service is listening on all network interfaces.

---

## Configuration Reference (Already Applied)

The following configuration has been applied to this system:

### Current Configuration File
Location: `/etc/systemd/system/ollama.service.d/override.conf`

```ini
[Service]
Environment="OLLAMA_NUM_PARALLEL=1"
Environment="OLLAMA_MAX_QUEUE=128"
Environment="OLLAMA_KEEP_ALIVE=30m"
Environment="OLLAMA_MAX_LOADED_MODELS=1"
Environment="OLLAMA_HOST=0.0.0.0:11434"
```

### Verification
- ✅ Service listening on all interfaces (`0.0.0.0:11434`)
- ✅ Firewall port 11434/tcp opened
- ✅ External access tested and confirmed working

To verify on this system:
```bash
ss -tlnp | grep 11434
# Output: LISTEN 0  4096  *:11434  *:*
```

---

## How to Connect External Agents/Services

### Base URL for External Access

```
http://85.31.233.157:11434
```

### API Endpoints

#### 1. List Available Models
```bash
curl http://85.31.233.157:11434/api/tags
```

#### 2. Generate Completion (Chat)
```bash
curl http://85.31.233.157:11434/api/generate -d '{
  "model": "llama3",
  "prompt": "Why is the sky blue?",
  "stream": false
}'
```

#### 3. Chat Completion (OpenAI-Compatible)
```bash
curl http://85.31.233.157:11434/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "llama3",
  "messages": [
    {
      "role": "user",
      "content": "Hello!"
    }
  ]
}'
```

#### 4. Generate Embeddings
```bash
curl http://85.31.233.157:11434/api/embeddings -d '{
  "model": "llama3",
  "prompt": "Here is an article about llamas..."
}'
```

#### 5. Pull a Model (Direct Ollama API)
```bash
curl http://85.31.233.157:11434/api/pull -d '{
  "model": "llama3.1:latest",
  "stream": false
}'
```

#### 6. Delete a Model (Direct Ollama API)
```bash
curl -X DELETE http://85.31.233.157:11434/api/delete -d '{
  "model": "llama3.1:latest"
}'
```

### Recommended for Remote Agents: Managed Model Operations

For controlled pull/delete with API keys, use the management layer on this VPS:

- Registry + management API: `http://85.31.233.157:8002`
- List local models: `GET /api/v1/models/local`
- Pull model: `POST /api/v1/models/local/pull`
- Delete model: `DELETE /api/v1/models/local?model=<tag>`

This is preferred over direct Ollama management calls because Ollama itself has no built-in auth.

---

## Configuration Examples for Popular Tools

### Open WebUI

Set the Ollama base URL in your environment:

```bash
OLLAMA_BASE_URL=http://85.31.233.157:11434
```

Or in `docker-compose.yml`:

```yaml
environment:
  - OLLAMA_BASE_URL=http://85.31.233.157:11434
```

### LangChain (Python)

```python
from langchain_community.llms import Ollama

llm = Ollama(
    base_url="http://85.31.233.157:11434",
    model="llama3"
)

response = llm.invoke("Tell me a joke")
print(response)
```

### Ollama Python Library

```python
import ollama

client = ollama.Client(host='http://85.31.233.157:11434')

response = client.chat(model='llama3', messages=[
  {
    'role': 'user',
    'content': 'Why is the sky blue?',
  },
])

print(response['message']['content'])
```

### Continue.dev (VS Code Extension)

In your `.continue/config.json`:

```json
{
  "models": [
    {
      "title": "Remote Llama3",
      "provider": "ollama",
      "model": "llama3",
      "apiBase": "http://85.31.233.157:11434"
    }
  ]
}
```

### LibreChat

In your `.env` file:

```bash
OLLAMA_BASE_URL=http://85.31.233.157:11434
```

---

## Security Considerations

### ⚠️ Important Security Notes

1. **No Authentication:** Ollama does not have built-in authentication. Anyone with network access to port 11434 can use your models.

2. **Firewall Protection:** Consider restricting access to specific IP addresses:

```bash
# Allow only from specific IP
sudo ufw allow from YOUR_IP_ADDRESS to any port 11434

# Or allow from a subnet
sudo ufw allow from 192.168.1.0/24 to any port 11434
```

3. **Reverse Proxy with Authentication:** Use Nginx or Caddy with basic auth:

**Nginx Example:**
```nginx
server {
    listen 80;
    server_name your-domain.com;

    location / {
        auth_basic "Ollama Access";
        auth_basic_user_file /etc/nginx/.htpasswd;
        proxy_pass http://127.0.0.1:11434;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

4. **SSL/TLS:** Use HTTPS with Let's Encrypt for encrypted connections.

5. **Rate Limiting:** Prevent abuse by implementing rate limiting at the firewall or proxy level.

---

## Troubleshooting

### Connection Refused

- Verify Ollama is listening on `0.0.0.0`: `ss -tlnp | grep 11434`
- Check firewall: `sudo ufw status` or `sudo iptables -L -n`
- Ensure port 11434 is open on your VPS provider's firewall (check their control panel)

### Slow Response Times

- Check current load: `ollama ps`
- Review your configuration (parallel requests, max loaded models)
- Monitor resources: `htop` or `free -h`

### Model Not Found

- List available models: `curl http://85.31.233.157:11434/api/tags`
- Pull a new model: `ollama pull model-name`

---

## Quick Reference Commands

```bash
# Check if Ollama is running
sudo systemctl status ollama

# View logs
sudo journalctl -u ollama -f

# Restart service
sudo systemctl restart ollama

# List running models
ollama ps

# Pull a new model
ollama pull mistral

# Test local connection
curl http://localhost:11434/api/tags

# Test external connection (from another machine)
curl http://85.31.233.157:11434/api/tags
```

---

## Performance Tuning (Current Settings)

Your current configuration:
- **Parallel Requests:** 1 (reduces CPU contention and token latency)
- **Max Queue:** 128 (handles burst traffic without immediate failure)
- **Keep Alive:** 30 minutes (reduces cold-start reloads in multi-step flows)
- **Max Loaded Models:** 1 (prevents RAM thrash on 16GB host)

These are latency-optimized defaults for this CPU VPS.

---

**Last Updated:** February 15, 2026  
**Server:** 85.31.233.157  
**Status:** ✅ Configured and accessible externally
