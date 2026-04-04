# TALOS Tool Server - External Access Guide

## Overview

**Service:** TALOS Tool Server  
**Base URL:** `http://85.31.233.157:8081`  
**Version:** `dev`  
**Language:** Go  
**Status:** ✅ **Configured for external access**

TALOS Tool Server is a high-performance Go HTTP server that exposes advanced capabilities for LLM agents, including web search, URL fetching, HTTP requests, semantic vector retrieval, and sandboxed code execution.

---

## Key Features

- 🔍 **Web Search** - Brave Search API integration for current information
- 🌐 **URL Fetching** - Retrieve and parse content from any URL
- 🔗 **HTTP Requests** - Make arbitrary HTTP calls with allowlist control
- 🧠 **Vector Retrieval** - Semantic search via PocketBase + Ollama embeddings
- 💻 **Code Sandbox** - Execute code safely in isolated Docker containers
- 🔒 **Authentication** - API key + Client ID based auth
- 📚 **OpenAPI** - Full OpenAPI 3.0 spec with Swagger UI

---

## Quick Start

### 1. Authentication

All requests require two headers:

```bash
Authorization: Bearer <api_key>
X-Client-Id: <client_id>
```

### 2. Available Credentials

**Client A:**
- Client ID: `client-a`
- API Key: `U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN`

**Client B:**
- Client ID: `client-b`
- API Key: `20nnNohGHGBnrMq2yp45ITj5oaWmt3f-JSDXXIeSjWjE6NOY9Qn2gbzVSw790Jl-`

### 3. Test Connection

```bash
curl http://85.31.233.157:8081/version \
  -H "X-Client-Id: client-a"
```

Response:
```json
{
  "$schema": "http://localhost:8081/schemas/InfoResponse.json",
  "version": "dev",
  "commit": "unknown"
}
```

---

## API Endpoints

### Base Information

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/version` | GET | Client ID only | Get server version |
| `/docs` | GET | None | Swagger UI documentation |
| `/openapi.json` | GET | None | OpenAPI 3.0 specification |

### Tool Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/tools/web_search` | POST | Full | Search the web via Brave Search |
| `/tools/fetch_url` | POST | Full | Fetch and parse content from URL |
| `/tools/http_request` | POST | Full | Make HTTP request with allowlist |
| `/tools/vector_retrieve` | POST | Full | Semantic search via embeddings |
| `/tools/code_exec_sandbox` | POST | Full | Execute code in Docker sandbox |

---

## Tool Documentation

### 1. Web Search (`/tools/web_search`)

Search the web using the Brave Search API. Returns ranked results with titles, URLs, descriptions, and snippets.

#### Input Schema

```json
{
  "query": "string (required)",
  "top_k": "integer (optional, default: 10)",
  "recency_days": "integer (optional)",
  "site_filter": ["string"] (optional)
}
```

#### Parameters

- **query** - Search query string
- **top_k** - Number of results to return (default: 10)
- **recency_days** - Filter results to last N days
- **site_filter** - Array of domains to limit search (e.g., `["github.com", "stackoverflow.com"]`)

#### Example Request

```bash
curl -X POST http://85.31.233.157:8081/tools/web_search \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "latest developments in quantum computing 2026",
    "top_k": 5,
    "recency_days": 30
  }'
```

#### Example Response

```json
{
  "$schema": "http://85.31.233.157:8081/schemas/WebSearchOutput.json",
  "results": [
    {
      "title": "Quantum Computing Breakthrough 2026",
      "url": "https://example.com/quantum-2026",
      "description": "Recent advances in quantum error correction...",
      "snippet": "Scientists at MIT have achieved..."
    },
    {
      "title": "IBM Quantum Roadmap Update",
      "url": "https://ibm.com/quantum/roadmap",
      "description": "IBM announces new 1000-qubit processor...",
      "snippet": "The latest generation of IBM quantum processors..."
    }
  ],
  "query": "latest developments in quantum computing 2026",
  "total_results": 5
}
```

#### Python Example

```python
import requests

def web_search(query, top_k=5, recency_days=None):
    response = requests.post(
        "http://85.31.233.157:8081/tools/web_search",
        headers={
            "Authorization": "Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN",
            "X-Client-Id": "client-a",
            "Content-Type": "application/json"
        },
        json={
            "query": query,
            "top_k": top_k,
            "recency_days": recency_days
        }
    )
    return response.json()

# Usage
results = web_search("AI breakthroughs 2026", top_k=5, recency_days=7)
for result in results['results']:
    print(f"{result['title']}: {result['url']}")
```

---

### 2. Fetch URL (`/tools/fetch_url`)

Fetch and parse content from any URL. Returns text content with optional character limit.

#### Input Schema

```json
{
  "url": "string (required)",
  "max_chars": "integer (optional)"
}
```

#### Parameters

- **url** - Full URL to fetch (must be valid HTTP/HTTPS)
- **max_chars** - Maximum characters to return (truncates if exceeded)

#### Example Request

```bash
curl -X POST http://85.31.233.157:8081/tools/fetch_url \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://en.wikipedia.org/wiki/Artificial_intelligence",
    "max_chars": 5000
  }'
```

#### Example Response

```json
{
  "$schema": "http://85.31.233.157:8081/schemas/FetchURLOutput.json",
  "url": "https://en.wikipedia.org/wiki/Artificial_intelligence",
  "content": "Artificial intelligence (AI) is intelligence demonstrated by machines...",
  "content_type": "text/html",
  "status_code": 200,
  "truncated": true,
  "char_count": 5000
}
```

#### Use Cases

- Extract article content for summarization
- Fetch documentation pages
- Retrieve API responses
- Parse HTML/JSON data
- Monitor webpage changes

#### Python Example

```python
def fetch_url(url, max_chars=None):
    response = requests.post(
        "http://85.31.233.157:8081/tools/fetch_url",
        headers={
            "Authorization": "Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN",
            "X-Client-Id": "client-a",
            "Content-Type": "application/json"
        },
        json={"url": url, "max_chars": max_chars}
    )
    return response.json()

# Fetch and summarize
content = fetch_url("https://blog.example.com/latest-post", max_chars=2000)
print(f"Fetched {content['char_count']} chars from {content['url']}")
```

---

### 3. HTTP Request (`/tools/http_request`)

Make arbitrary HTTP requests with method, headers, and body. Uses allowlist profiles for security.

#### Input Schema

```json
{
  "method": "string (required)",
  "url": "string (required)",
  "headers": {"key": "value"} (optional),
  "body": "string (optional)",
  "allowlist_profile": "string (optional)"
}
```

#### Parameters

- **method** - HTTP method (GET, POST, PUT, DELETE, PATCH, etc.)
- **url** - Target URL
- **headers** - Custom headers as key-value pairs
- **body** - Request body (for POST/PUT/PATCH)
- **allowlist_profile** - Security profile name (e.g., `public_web`, `internal_api`)

#### Allowlist Profiles

Profiles defined in `/root/glm-toolserver/examples/allowlist.yaml`:

**public_web:**
- Allowed schemes: HTTPS only
- Allowed methods: GET
- Allowed hosts: `example.com`, `*.example.com`

**internal_api:**
- Allowed schemes: HTTPS only
- Allowed methods: GET, POST, PUT, DELETE, PATCH
- Allowed hosts: `api.yourcompany.com`

#### Example Request

```bash
curl -X POST http://85.31.233.157:8081/tools/http_request \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{
    "method": "POST",
    "url": "https://api.example.com/data",
    "headers": {
      "Content-Type": "application/json",
      "X-API-Key": "secret"
    },
    "body": "{\"query\": \"test\"}",
    "allowlist_profile": "internal_api"
  }'
```

#### Example Response

```json
{
  "$schema": "http://85.31.233.157:8081/schemas/HTTPRequestOutput.json",
  "status_code": 200,
  "headers": {
    "content-type": "application/json",
    "date": "Fri, 14 Feb 2026 21:00:00 GMT"
  },
  "body": "{\"status\": \"success\", \"data\": {...}}",
  "duration_ms": 245
}
```

#### Python Example

```python
def http_request(method, url, headers=None, body=None, profile=None):
    response = requests.post(
        "http://85.31.233.157:8081/tools/http_request",
        headers={
            "Authorization": "Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN",
            "X-Client-Id": "client-a",
            "Content-Type": "application/json"
        },
        json={
            "method": method,
            "url": url,
            "headers": headers,
            "body": body,
            "allowlist_profile": profile
        }
    )
    return response.json()

# Make API call
result = http_request(
    "POST",
    "https://api.example.com/webhook",
    headers={"Authorization": "Bearer token123"},
    body='{"event": "test"}',
    profile="internal_api"
)
```

---

### 4. Vector Retrieve (`/tools/vector_retrieve`)

Semantic search using PocketBase vector storage and Ollama embeddings. Finds documents similar to the query using cosine similarity.

#### Input Schema

```json
{
  "query": "string (required)",
  "namespace": "string (required)",
  "top_k": "integer (required)",
  "filters": {"key": "value"} (optional)
}
```

#### Parameters

- **query** - Natural language search query
- **namespace** - PocketBase collection namespace (e.g., `documentation`, `knowledge_base`)
- **top_k** - Number of similar documents to return
- **filters** - Additional filters as key-value pairs (e.g., `{"category": "tutorial"}`)

#### How It Works

1. Query is converted to embeddings via Ollama (`nomic-embed-text` model)
2. Embeddings are compared against stored vectors in PocketBase
3. Most similar documents are returned with similarity scores
4. Results include document text, metadata, and relevance score

#### Example Request

```bash
curl -X POST http://85.31.233.157:8081/tools/vector_retrieve \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "how to optimize LLM inference performance",
    "namespace": "documentation",
    "top_k": 5,
    "filters": {
      "category": "optimization"
    }
  }'
```

#### Example Response

```json
{
  "$schema": "http://85.31.233.157:8081/schemas/VectorRetrieveOutput.json",
  "results": [
    {
      "id": "rec_abc123",
      "text": "To optimize LLM inference, consider: 1) Quantization...",
      "metadata": {
        "title": "LLM Optimization Guide",
        "category": "optimization",
        "date": "2026-02-01"
      },
      "score": 0.89
    },
    {
      "id": "rec_def456",
      "text": "Batch processing can significantly improve throughput...",
      "metadata": {
        "title": "Batch Processing Techniques",
        "category": "optimization"
      },
      "score": 0.82
    }
  ],
  "query": "how to optimize LLM inference performance",
  "total": 5
}
```

#### Use Cases

- RAG (Retrieval-Augmented Generation) for LLMs
- Semantic documentation search
- Knowledge base queries
- Context retrieval for chatbots
- Similar content recommendations

#### Configuration

The tool uses:
- **PocketBase URL:** `https://pocketbase.thynaptic.com`
- **Embedding Model:** `nomic-embed-text` via Ollama
- **Ollama Endpoint:** `http://localhost:11434/v1`

#### Python Example

```python
def vector_search(query, namespace="documentation", top_k=5, filters=None):
    response = requests.post(
        "http://85.31.233.157:8081/tools/vector_retrieve",
        headers={
            "Authorization": "Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN",
            "X-Client-Id": "client-a",
            "Content-Type": "application/json"
        },
        json={
            "query": query,
            "namespace": namespace,
            "top_k": top_k,
            "filters": filters or {}
        }
    )
    return response.json()

# Semantic search
results = vector_search(
    "best practices for prompt engineering",
    namespace="documentation",
    top_k=3
)

for result in results['results']:
    print(f"[Score: {result['score']:.2f}] {result['metadata']['title']}")
    print(result['text'][:200])
```

---

### 5. Code Execution Sandbox (`/tools/code_exec_sandbox`)

Execute code safely in isolated Docker containers. Supports multiple languages with timeout protection and artifact collection.

> ✅ **Recent fix (Feb 15, 2026):** Resolved sandbox file-permission issue that caused errors like:
> `python3: can't open file '/work/main.py': [Errno 13] Permission denied`
> The runner now writes readable files for the non-root container user, and `code_exec_sandbox` is verified working.

#### Input Schema

```json
{
  "language": "string (required)",
  "code": "string (required)",
  "timeout_seconds": "integer (required)",
  "input_files": [
    {
      "name": "string",
      "content": "string",
      "content_base64": "string (optional)"
    }
  ] (optional)
}
```

#### Parameters

- **language** - Programming language (e.g., `python`, `javascript`, `go`, `bash`)
- **code** - Source code to execute
- **timeout_seconds** - Maximum execution time (hard limit)
- **input_files** - Optional files to include in sandbox (e.g., data files, configs)

#### Supported Languages

- Python (`python`)
- JavaScript/Node.js (`javascript`, `node`)
- Go (`go`)
- Bash/Shell (`bash`, `sh`)
- Ruby (`ruby`)
- Rust (`rust`)
- Java (`java`)
- C++ (`cpp`)

#### Example Request - Python

```bash
curl -X POST http://85.31.233.157:8081/tools/code_exec_sandbox \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{
    "language": "python",
    "code": "import sys\nfor i in range(1, 11):\n    print(f\"Number: {i}\")\nprint(f\"Python version: {sys.version}\")",
    "timeout_seconds": 10
  }'
```

#### Example Response

```json
{
  "$schema": "http://85.31.233.157:8081/schemas/CodeExecOutput.json",
  "stdout": "Number: 1\nNumber: 2\n...\nPython version: 3.11.0",
  "stderr": "",
  "exit_code": 0,
  "timed_out": false,
  "artifacts": []
}
```

#### Example Request - With Input Files

```bash
curl -X POST http://85.31.233.157:8081/tools/code_exec_sandbox \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{
    "language": "python",
    "code": "with open(\"data.txt\") as f:\n    data = f.read()\nprint(f\"Data length: {len(data)}\")\nwith open(\"output.txt\", \"w\") as f:\n    f.write(data.upper())",
    "timeout_seconds": 10,
    "input_files": [
      {
        "name": "data.txt",
        "content": "hello world from input file"
      }
    ]
  }'
```

#### Example Response - With Artifacts

```json
{
  "$schema": "http://85.31.233.157:8081/schemas/CodeExecOutput.json",
  "stdout": "Data length: 27\n",
  "stderr": "",
  "exit_code": 0,
  "timed_out": false,
  "artifacts": [
    {
      "name": "output.txt",
      "content_type": "text/plain",
      "download_url": "http://85.31.233.157:8081/artifacts/abc123/output.txt"
    }
  ]
}
```

#### Security Features

- **Isolated Docker containers** - Each execution runs in fresh container
- **Network isolation** - No internet access from sandbox
- **Timeout enforcement** - Hard limit prevents infinite loops
- **Resource limits** - CPU and memory constraints
- **Automatic cleanup** - Containers destroyed after execution

#### Use Cases

- Execute user-submitted code safely
- Run data analysis scripts
- Validate algorithms
- Test code snippets
- Generate visualizations or reports
- Automated code testing

#### Python Example

```python
def execute_code(language, code, timeout=10, input_files=None):
    response = requests.post(
        "http://85.31.233.157:8081/tools/code_exec_sandbox",
        headers={
            "Authorization": "Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN",
            "X-Client-Id": "client-a",
            "Content-Type": "application/json"
        },
        json={
            "language": language,
            "code": code,
            "timeout_seconds": timeout,
            "input_files": input_files
        }
    )
    return response.json()

# Execute Python code
result = execute_code(
    "python",
    """
import math
for i in range(1, 6):
    print(f"{i}! = {math.factorial(i)}")
""",
    timeout=5
)

print(result['stdout'])
print(f"Exit code: {result['exit_code']}")
```

---

## Complete Client Library

Here's a comprehensive Python client for all TALOS Tool Server endpoints:

```python
import requests
from typing import Optional, Dict, List, Any

class GLMToolClient:
    """
    Complete client for TALOS Tool Server
    
    Example:
        client = GLMToolClient(
            base_url="http://85.31.233.157:8081",
            api_key="U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN",
            client_id="client-a"
        )
        
        results = client.web_search("AI trends 2026")
        content = client.fetch_url("https://example.com")
        code_output = client.execute_code("python", "print('Hello')")
    """
    
    def __init__(self, base_url: str, api_key: str, client_id: str):
        self.base_url = base_url.rstrip('/')
        self.api_key = api_key
        self.client_id = client_id
        
    def _headers(self) -> Dict[str, str]:
        return {
            "Authorization": f"Bearer {self.api_key}",
            "X-Client-Id": self.client_id,
            "Content-Type": "application/json"
        }
    
    def _post(self, endpoint: str, data: Dict[str, Any]) -> Dict[str, Any]:
        """Make POST request to tool server."""
        response = requests.post(
            f"{self.base_url}{endpoint}",
            headers=self._headers(),
            json=data
        )
        response.raise_for_status()
        return response.json()
    
    def version(self) -> Dict[str, str]:
        """Get server version."""
        response = requests.get(
            f"{self.base_url}/version",
            headers={"X-Client-Id": self.client_id}
        )
        response.raise_for_status()
        return response.json()
    
    def web_search(
        self,
        query: str,
        top_k: int = 10,
        recency_days: Optional[int] = None,
        site_filter: Optional[List[str]] = None
    ) -> Dict[str, Any]:
        """
        Search the web using Brave Search.
        
        Args:
            query: Search query
            top_k: Number of results (default: 10)
            recency_days: Filter to last N days
            site_filter: List of domains to restrict search
            
        Returns:
            Dict with 'results' list containing title, url, description, snippet
        """
        data = {"query": query, "top_k": top_k}
        if recency_days:
            data["recency_days"] = recency_days
        if site_filter:
            data["site_filter"] = site_filter
        
        return self._post("/tools/web_search", data)
    
    def fetch_url(self, url: str, max_chars: Optional[int] = None) -> Dict[str, Any]:
        """
        Fetch content from URL.
        
        Args:
            url: URL to fetch
            max_chars: Maximum characters to return
            
        Returns:
            Dict with 'content', 'url', 'status_code', 'content_type'
        """
        data = {"url": url}
        if max_chars:
            data["max_chars"] = max_chars
        
        return self._post("/tools/fetch_url", data)
    
    def http_request(
        self,
        method: str,
        url: str,
        headers: Optional[Dict[str, str]] = None,
        body: Optional[str] = None,
        allowlist_profile: Optional[str] = None
    ) -> Dict[str, Any]:
        """
        Make HTTP request with allowlist control.
        
        Args:
            method: HTTP method (GET, POST, etc.)
            url: Target URL
            headers: Custom headers
            body: Request body
            allowlist_profile: Security profile name
            
        Returns:
            Dict with 'status_code', 'headers', 'body', 'duration_ms'
        """
        data = {"method": method, "url": url}
        if headers:
            data["headers"] = headers
        if body:
            data["body"] = body
        if allowlist_profile:
            data["allowlist_profile"] = allowlist_profile
        
        return self._post("/tools/http_request", data)
    
    def vector_retrieve(
        self,
        query: str,
        namespace: str,
        top_k: int = 5,
        filters: Optional[Dict[str, Any]] = None
    ) -> Dict[str, Any]:
        """
        Semantic search using vector embeddings.
        
        Args:
            query: Natural language query
            namespace: PocketBase collection namespace
            top_k: Number of results
            filters: Additional filters
            
        Returns:
            Dict with 'results' list containing text, metadata, score
        """
        data = {
            "query": query,
            "namespace": namespace,
            "top_k": top_k,
            "filters": filters or {}
        }
        
        return self._post("/tools/vector_retrieve", data)
    
    def execute_code(
        self,
        language: str,
        code: str,
        timeout_seconds: int = 10,
        input_files: Optional[List[Dict[str, str]]] = None
    ) -> Dict[str, Any]:
        """
        Execute code in sandbox.
        
        Args:
            language: Programming language
            code: Source code
            timeout_seconds: Execution timeout
            input_files: List of dicts with 'name' and 'content'
            
        Returns:
            Dict with 'stdout', 'stderr', 'exit_code', 'timed_out', 'artifacts'
        """
        data = {
            "language": language,
            "code": code,
            "timeout_seconds": timeout_seconds
        }
        if input_files:
            data["input_files"] = input_files
        
        return self._post("/tools/code_exec_sandbox", data)


# Example usage
if __name__ == "__main__":
    client = GLMToolClient(
        base_url="http://85.31.233.157:8081",
        api_key="U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN",
        client_id="client-a"
    )
    
    # Get version
    print(client.version())
    
    # Web search
    search_results = client.web_search("AI breakthroughs 2026", top_k=3)
    for result in search_results['results']:
        print(f"- {result['title']}: {result['url']}")
    
    # Fetch URL
    content = client.fetch_url("https://example.com", max_chars=1000)
    print(f"Fetched {len(content['content'])} chars")
    
    # Execute code
    output = client.execute_code("python", "print('Hello from sandbox!')")
    print(f"Output: {output['stdout']}")
    
    # Vector search
    docs = client.vector_retrieve(
        "machine learning tutorials",
        namespace="documentation",
        top_k=5
    )
    print(f"Found {len(docs['results'])} similar documents")
```

---

## JavaScript/TypeScript Client

```typescript
interface GLMToolClientConfig {
  baseUrl: string;
  apiKey: string;
  clientId: string;
}

interface WebSearchParams {
  query: string;
  top_k?: number;
  recency_days?: number;
  site_filter?: string[];
}

interface CodeExecParams {
  language: string;
  code: string;
  timeout_seconds: number;
  input_files?: Array<{name: string; content: string}>;
}

class GLMToolClient {
  private baseUrl: string;
  private apiKey: string;
  private clientId: string;

  constructor(config: GLMToolClientConfig) {
    this.baseUrl = config.baseUrl.replace(/\/$/, '');
    this.apiKey = config.apiKey;
    this.clientId = config.clientId;
  }

  private headers(): HeadersInit {
    return {
      'Authorization': `Bearer ${this.apiKey}`,
      'X-Client-Id': this.clientId,
      'Content-Type': 'application/json'
    };
  }

  async webSearch(params: WebSearchParams) {
    const response = await fetch(`${this.baseUrl}/tools/web_search`, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify(params)
    });
    
    if (!response.ok) {
      throw new Error(`Web search failed: ${response.statusText}`);
    }
    
    return await response.json();
  }

  async fetchUrl(url: string, maxChars?: number) {
    const response = await fetch(`${this.baseUrl}/tools/fetch_url`, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify({ url, max_chars: maxChars })
    });
    
    if (!response.ok) {
      throw new Error(`Fetch URL failed: ${response.statusText}`);
    }
    
    return await response.json();
  }

  async executeCode(params: CodeExecParams) {
    const response = await fetch(`${this.baseUrl}/tools/code_exec_sandbox`, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify(params)
    });
    
    if (!response.ok) {
      throw new Error(`Code execution failed: ${response.statusText}`);
    }
    
    return await response.json();
  }

  async vectorRetrieve(query: string, namespace: string, topK: number = 5) {
    const response = await fetch(`${this.baseUrl}/tools/vector_retrieve`, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify({
        query,
        namespace,
        top_k: topK,
        filters: {}
      })
    });
    
    if (!response.ok) {
      throw new Error(`Vector retrieve failed: ${response.statusText}`);
    }
    
    return await response.json();
  }
}

// Usage
const client = new GLMToolClient({
  baseUrl: 'http://85.31.233.157:8081',
  apiKey: 'U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN',
  clientId: 'client-a'
});

// Search
const results = await client.webSearch({
  query: 'TypeScript best practices',
  top_k: 5
});

// Execute code
const output = await client.executeCode({
  language: 'javascript',
  code: 'console.log("Hello from Node.js!");',
  timeout_seconds: 10
});
```

---

## Configuration

### Server Configuration

Location: `/root/glm-toolserver/.env`

```ini
# Server
PORT=8081
BIND_ADDR=0.0.0.0:8081
REQUEST_TIMEOUT=60s
MAX_BODY_BYTES=1048576

# Auth
API_KEYS_FILE=./examples/api_keys.yaml
API_KEYS_RELOAD_INTERVAL=30s

# Allowlist profiles
ALLOWLIST_PROFILES_FILE=./examples/allowlist.yaml

# Brave Search
BRAVE_API_KEY=BSAVTmOCr8rjj6OMeBYoXYs8Plxtxz_

# Embeddings (Ollama OpenAI-compatible)
OLLAMA_OPENAI_BASE_URL=http://localhost:11434/v1
OLLAMA_EMBED_MODEL=nomic-embed-text

# PocketBase
POCKETBASE_URL=https://pocketbase.thynaptic.com
POCKETBASE_AUTH_COLLECTION=service_accounts
POCKETBASE_IDENTITY=cass@thynaptic.com
POCKETBASE_PASSWORD=***
GLM_POCKETBASE_ALLOW_UNAUTH=false
POCKETBASE_COLLECTION_PREFIX=glm
POCKETBASE_CANDIDATE_LIMIT=200
```

### API Keys Configuration

Location: `/root/glm-toolserver/examples/api_keys.yaml`

```yaml
clients:
  client-a: "U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN"
  client-b: "20nnNohGHGBnrMq2yp45ITj5oaWmt3f-JSDXXIeSjWjE6NOY9Qn2gbzVSw790Jl-"
```

Keys are reloaded every 30 seconds, allowing runtime updates.

### Allowlist Configuration

Location: `/root/glm-toolserver/examples/allowlist.yaml`

```yaml
profiles:
  public_web:
    allowed_schemes: ["https"]
    allowed_methods: ["GET"]
    allowed_hosts:
      - "example.com"
      - "*.example.com"
      
  internal_api:
    allowed_schemes: ["https"]
    allowed_methods: ["GET", "POST", "PUT", "DELETE", "PATCH"]
    allowed_hosts:
      - "api.yourcompany.com"
```

### Systemd Service

Location: `/etc/systemd/system/glm-toolserver.service`

```ini
[Unit]
Description=TALOS Tool Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/root/glm-toolserver/.env
WorkingDirectory=/root/glm-toolserver
ExecStart=/usr/local/bin/glm-toolserver
Restart=on-failure
RestartSec=2
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

---

## Security Best Practices

### 1. Credential Management

- ✅ **Rotate API keys regularly**
- ✅ **Use different keys per client/application**
- ✅ **Never commit keys to version control**
- ✅ **Use environment variables in applications**

### 2. Network Security

- ✅ **Consider IP allowlisting for production**
- ✅ **Use HTTPS proxy (Nginx/Caddy) for encryption**
- ✅ **Implement rate limiting**
- ✅ **Monitor access logs**

### 3. Allowlist Profiles

- ✅ **Use strict allowlists for HTTP requests**
- ✅ **Limit allowed domains and methods**
- ✅ **Require HTTPS for external requests**
- ✅ **Review and update allowlists regularly**

### 4. Code Sandbox Security

- ✅ **Docker containers are isolated and destroyed after use**
- ✅ **Network access is disabled in sandbox**
- ✅ **Resource limits prevent DoS**
- ✅ **Timeout enforcement prevents runaway code**
- ⚠️ **Still review code before execution when possible**

---

## Monitoring & Troubleshooting

### Check Service Status

```bash
systemctl status glm-toolserver
```

### View Logs

```bash
# Real-time logs
journalctl -u glm-toolserver -f

# Last 50 lines
journalctl -u glm-toolserver -n 50

# Errors only
journalctl -u glm-toolserver -p err
```

### Test Connectivity

```bash
# Basic health check (no auth needed for version)
curl http://85.31.233.157:8081/version \
  -H "X-Client-Id: client-a"

# Full auth test
curl http://85.31.233.157:8081/tools/web_search \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{"query": "test", "top_k": 1}'
```

### Live Endpoint Verification (All Integrated Services)

Use these checks to confirm the full agent toolchain is reachable now:

```bash
# 1) TALOS OpenAPI + version
curl -s http://85.31.233.157:8081/openapi.json | jq '.paths | keys'
curl -s http://85.31.233.157:8081/version -H "X-Client-Id: client-a"

# 2) TALOS tools quick smoke tests
curl -s -X POST http://85.31.233.157:8081/tools/web_search \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{"query":"test","top_k":1}'

curl -s -X POST http://85.31.233.157:8081/tools/code_exec_sandbox \
  -H "Authorization: Bearer U31645rK2bzY7xrKZcS7vb5k8UmuRmqG2gWlpWYaYcgO_za6N43u2wUqWVihW9WN" \
  -H "X-Client-Id: client-a" \
  -H "Content-Type: application/json" \
  -d '{"language":"python","code":"print(2+2)","timeout_seconds":10}'

# 3) Ollama model runtime
curl -s http://85.31.233.157:11434/api/tags

# 4) Registry + orchestration API
curl -s http://85.31.233.157:8002/api/v1/models?limit=3
curl -s http://85.31.233.157:8002/healthz
curl -s http://85.31.233.157:8002/api/v1/orchestration/tools/catalog \
  -H "X-API-Key: S6k9Fz6ulcFFOrWp0zAYh-ql2mlRy_cL"

# 5) OllamaBench API
curl -s http://85.31.233.157:8000/api/health
```

If all return 200 with valid JSON, your end-to-end agent tooling stack is healthy.

### Common Errors

**401 Unauthorized:**
- Check Authorization header format: `Bearer <api_key>`
- Verify API key is correct
- Ensure key exists in `api_keys.yaml`

**403 Forbidden:**
- Check X-Client-Id header
- Verify client ID matches API key
- Check allowlist profile restrictions

**500 Internal Server Error:**
- Check server logs: `journalctl -u glm-toolserver -n 50`
- Verify dependencies (Docker for sandbox, PocketBase for vectors)
- Check environment variables in `.env`

**`/work/main.py` Permission denied (code_exec_sandbox):**
- This was fixed in the current deployment (Feb 15, 2026).
- If it reappears after a rollback/redeploy, rebuild and restart from `/root/glm-toolserver`:
```bash
cd /root/glm-toolserver
go build -o /usr/local/bin/glm-toolserver ./cmd/toolserver
sudo systemctl restart glm-toolserver
```

**Timeout:**
- Check network connectivity
- Verify server is running: `systemctl status glm-toolserver`
- Check firewall: `sudo ufw status`

### Restart Service

```bash
sudo systemctl restart glm-toolserver
```

### Reload Configuration

API keys and allowlists are reloaded automatically every 30 seconds. For immediate reload:

```bash
sudo systemctl restart glm-toolserver
```

---

## Performance Considerations

### Request Timeout

Default: 60 seconds (configurable via `REQUEST_TIMEOUT`)

### Max Body Size

Default: 1MB (configurable via `MAX_BODY_BYTES`)

### Concurrent Requests

The Go server handles concurrent requests efficiently. No explicit limit configured.

### Code Execution

- Docker container startup adds ~1-3 seconds overhead
- Execution time counted separately from timeout
- Resource limits prevent memory/CPU exhaustion

### Vector Retrieval

- Embedding generation: ~100-500ms (depends on Ollama load)
- PocketBase query: ~50-200ms
- Total latency: typically <1 second for top_k=10

---

## Integration Examples

### RAG (Retrieval-Augmented Generation) Pipeline

```python
def rag_pipeline(question: str, llm_client, glm_client):
    """Complete RAG pipeline using TALOS tools and LLM."""
    
    # Step 1: Search for relevant context
    search_results = glm_client.web_search(question, top_k=3)
    
    # Step 2: Fetch detailed content from top results
    contexts = []
    for result in search_results['results'][:2]:
        content = glm_client.fetch_url(result['url'], max_chars=2000)
        contexts.append(content['content'])
    
    # Step 3: Also search vector database
    vector_results = glm_client.vector_retrieve(
        question,
        namespace="knowledge_base",
        top_k=3
    )
    for result in vector_results['results']:
        contexts.append(result['text'])
    
    # Step 4: Construct prompt with context
    context_str = "\n\n---\n\n".join(contexts)
    prompt = f"""Based on the following context, answer the question.

Context:
{context_str}

Question: {question}

Answer:"""
    
    # Step 5: Generate answer with LLM
    response = llm_client.generate(prompt)
    return response
```

### Code Analysis Agent

```python
def code_analysis_agent(code: str, language: str, glm_client):
    """Analyze and test code using sandbox."""
    
    # Execute code
    result = glm_client.execute_code(language, code, timeout_seconds=10)
    
    # Check for errors
    if result['exit_code'] != 0:
        return {
            'status': 'error',
            'stderr': result['stderr'],
            'suggestion': 'Code failed to execute'
        }
    
    # Run with test cases
    test_code = f"""
{code}

# Test cases
assert sum([1, 2, 3]) == 6
assert sum([]) == 0
print("All tests passed!")
"""
    
    test_result = glm_client.execute_code(language, test_code, timeout_seconds=10)
    
    return {
        'status': 'success',
        'output': result['stdout'],
        'tests': test_result['stdout']
    }
```

### Research Assistant

```python
async def research_assistant(topic: str, glm_client, llm_client):
    """Comprehensive research using multiple tools."""
    
    # Step 1: Initial web search
    print("Searching the web...")
    search_results = glm_client.web_search(
        topic,
        top_k=5,
        recency_days=30
    )
    
    # Step 2: Fetch and analyze top articles
    print("Fetching articles...")
    articles = []
    for result in search_results['results'][:3]:
        content = glm_client.fetch_url(result['url'], max_chars=3000)
        articles.append({
            'title': result['title'],
            'url': result['url'],
            'content': content['content']
        })
    
    # Step 3: Summarize findings
    print("Generating summary...")
    summary_prompt = f"Summarize the key findings about {topic} from these articles:\n\n"
    for article in articles:
        summary_prompt += f"### {article['title']}\n{article['content']}\n\n"
    
    summary = llm_client.generate(summary_prompt)
    
    return {
        'topic': topic,
        'sources': [{'title': a['title'], 'url': a['url']} for a in articles],
        'summary': summary
    }
```

---

## API Reference

### Full OpenAPI Specification

Available at: `http://85.31.233.157:8081/openapi.json`

### Interactive Documentation

Swagger UI: `http://85.31.233.157:8081/docs`

### Schema Validation

All requests and responses include JSON Schema references:

```json
{
  "$schema": "http://85.31.233.157:8081/schemas/WebSearchInput.json",
  "query": "example search",
  "top_k": 5
}
```

---

## Support & Resources

### Server Information

- **Server IP:** 85.31.233.157
- **Port:** 8081
- **Protocol:** HTTP
- **Status:** ✅ Running and accessible

### Configuration Locations

- **Service:** `/etc/systemd/system/glm-toolserver.service`
- **Environment:** `/root/glm-toolserver/.env`
- **API Keys:** `/root/glm-toolserver/examples/api_keys.yaml`
- **Allowlists:** `/root/glm-toolserver/examples/allowlist.yaml`
- **Binary:** `/usr/local/bin/glm-toolserver`

### Logs

```bash
journalctl -u glm-toolserver -f
```

### Management Commands

```bash
# Status
systemctl status glm-toolserver

# Restart
sudo systemctl restart glm-toolserver

# Stop
sudo systemctl stop glm-toolserver

# Start
sudo systemctl start glm-toolserver

# Enable auto-start
sudo systemctl enable glm-toolserver
```

---

**Last Updated:** February 15, 2026  
**Server:** 85.31.233.157:8081  
**Status:** ✅ Configured and accessible externally  
**Authentication:** API Key + Client ID required for all tool endpoints
