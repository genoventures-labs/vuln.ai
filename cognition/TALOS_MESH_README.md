# TALOS Mesh README (Client + Server)

This document explains the TALOS Mesh architecture, its APIs, and how a **client-side TALOS runtime** should use it.

## 1) What the Mesh Is
The Mesh is a **sovereign cognition exchange** layer. Each TALOS runtime remains local and private, but can share **patterns, reasoning hints, and tools** through a trusted mesh of nodes.

There is **no shared memory** and **no raw user data**. Only small, abstracted cognitive artifacts are exchanged.

## 2) Roles
- **Mesh Server (Gateway)**  
  Runs on `chat.thynaptic.com` and exposes mesh endpoints.
- **TALOS Clients**  
  Run on user machines and call the mesh to:
  - register/heartbeat
  - query thought patterns
  - publish or request cognitive shards

## 3) Communication Primitives
The mesh exposes three primitives:

1. **Thought Queries**  
   “Do any nodes have reasoning patterns for X?”

2. **Cognitive Shards**  
   Small reusable cognition blocks (summaries, patterns, traces, embeddings).

3. **Tool Exchange**  
   Already implemented tool discovery + acquisition.

## 4) Security Model
### Trust Token
All mesh calls require a shared trust token (`MESH_TRUST_TOKEN`).

### Signed Heartbeats
All `/mesh/ping` calls must be signed with HMAC-SHA256 to prove identity continuity.

Signature base string:
```
node_id|node_url|node_label|timestamp|nonce|capabilities_csv|shard_count|current_load|reasoning_tier
```

Signature = HMAC-SHA256(base_string, MESH_TRUST_TOKEN).

### Time Skew
Ping signatures must be within `MESH_SIGNATURE_SKEW` (default 90s).

## 5) Mesh Endpoints (Server)
Base URL: `https://chat.thynaptic.com`

### Registration / Heartbeat
`POST /mesh/ping`

Required fields:
- `trust_token`
- `node_url`
- `timestamp`
- `nonce`
- `signature`

Optional:
- `node_id`
- `node_label`
- `metadata`
- `neighbors` (gossip discovery)

### Thought Query
`POST /mesh/thought/query`

### Thought Broadcast (local + mesh)
`POST /mesh/thought/broadcast`

### Shard Query
`POST /mesh/shards/query`

### Shard Publish
`POST /mesh/shards/publish`

### Neighbors (returns list)
`POST /mesh/neighbors`

## 6) Heartbeat Payload (Client)
Example:
```json
{
  "trust_token": "MESH_TRUST_TOKEN",
  "node_id": "talos-client-001",
  "node_url": "https://client-001.local",
  "node_label": "talos-client-001",
  "timestamp": 1739760000,
  "nonce": "6f4c0b9e-9fd8-4d8c-9b2d-2d58d6d1d9a7",
  "signature": "hex_hmac_sha256",
  "metadata": {
    "capabilities": ["tools","shards","reasoning","mesh"],
    "shard_count": 1280,
    "current_load": 0.42,
    "reasoning_tier": "t2"
  },
  "neighbors": [
    { "url": "https://node-002.example.com", "label": "talos-002" }
  ]
}
```

## 7) Metadata Pings
The mesh records metadata for operator visibility:
- `capabilities` (array)
- `shard_count` (int)
- `current_load` (float 0-1)
- `reasoning_tier` (string)

Stored in `glm_mesh_neighbor.meta`.

## 8) Gossip Discovery
Clients may include a `neighbors` list in `/mesh/ping`.  
The server **soft-registers** them as inactive, so they can be reviewed/activated later.

## 9) Thought Broadcasting
Use `/mesh/thought/broadcast` to get:
- local shard matches
- mesh shard matches
- combined response

This is the “ask the council” endpoint.

## 10) Client Integration Flow (Recommended)
1. On startup:
   - Generate heartbeat payload
   - POST to `/mesh/ping`
2. On query:
   - Use `/mesh/thought/broadcast` for distributed thought patterns
3. On new cognition:
   - Publish to `/mesh/shards/publish`
4. Repeat ping every few minutes

## 11) Example Client Ping (Go)
```go
package main

import (
    "bytes"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "net/http"
    "sort"
    "strings"
    "time"

    "github.com/google/uuid"
)

type PingMetadata struct {
    Capabilities  []string `json:"capabilities,omitempty"`
    ShardCount    int      `json:"shard_count,omitempty"`
    CurrentLoad   float64  `json:"current_load,omitempty"`
    ReasoningTier string   `json:"reasoning_tier,omitempty"`
}

type PingInput struct {
    TrustToken string       `json:"trust_token"`
    NodeID     string       `json:"node_id,omitempty"`
    NodeURL    string       `json:"node_url"`
    NodeLabel  string       `json:"node_label,omitempty"`
    Timestamp  int64        `json:"timestamp"`
    Nonce      string       `json:"nonce"`
    Signature  string       `json:"signature"`
    Metadata   PingMetadata `json:"metadata,omitempty"`
}

func main() {
    meshURL := "https://chat.thynaptic.com/mesh/ping"
    token := "MESH_TRUST_TOKEN"
    nodeID := "talos-client-001"
    nodeURL := "https://client-001.local"
    nodeLabel := "talos-client-001"

    caps := []string{"tools", "shards", "reasoning", "mesh"}
    for i := range caps {
        caps[i] = strings.ToLower(strings.TrimSpace(caps[i]))
    }
    sort.Strings(caps)
    capsSorted := strings.Join(caps, ",")

    shardCount := 0
    currentLoad := 0.25
    reasoningTier := "t2"

    ts := time.Now().Unix()
    nonce := uuid.NewString()

    signStr := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%d|%.3f|%s",
        nodeID, nodeURL, nodeLabel, ts, nonce, capsSorted, shardCount, currentLoad, strings.ToLower(reasoningTier),
    )
    mac := hmac.New(sha256.New, []byte(token))
    mac.Write([]byte(signStr))
    sig := hex.EncodeToString(mac.Sum(nil))

    payload := PingInput{
        TrustToken: token,
        NodeID:     nodeID,
        NodeURL:    nodeURL,
        NodeLabel:  nodeLabel,
        Timestamp:  ts,
        Nonce:      nonce,
        Signature:  sig,
        Metadata: PingMetadata{
            Capabilities:  caps,
            ShardCount:    shardCount,
            CurrentLoad:   currentLoad,
            ReasoningTier: reasoningTier,
        },
    }

    body, _ := json.Marshal(payload)
    req, _ := http.NewRequest(http.MethodPost, meshURL, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        panic(err)
    }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        panic(fmt.Sprintf("mesh ping failed: %d", resp.StatusCode))
    }
}
```

## 12) Notes for Agents
- TALOS is **client-side**, not a backend service.
- The mesh server is a **shared council**, not a shared memory.
- The mesh only accepts abstracted cognition artifacts.
