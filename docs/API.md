# API Documentation

EZyapper provides a RESTful API for management and configuration.

> [!WARNING]
> **Temporary WebUI/API Status**
> API endpoints are served by the Web module and are unavailable when WebUI is disabled.
> The dashboard currently has known stability issues, so the temporary recommendation is to keep `operations.web.enabled: false` for normal operation and enable it only for targeted debugging.

## Authentication

All API endpoints (except `/health`) require HTTP Basic Authentication.

> [!NOTE]
> **Current API Behavior**
> - Configuration updates are validated, applied to runtime, and persisted to `config.yaml`.
> - Plugin `/api/plugins/:name/enable` and `/api/plugins/:name/disable` endpoints invoke `PluginManager` and refresh plugin tools.
> - Statistics uptime is derived from Web server start time.
> - List APIs are strict: `/api/blacklist` and `/api/whitelist` accept `{ "type", "id" }` only.
> - API authentication is admin-level Basic Auth; endpoints are management APIs, not per-user tenant isolation.

```bash
curl -u admin:changeme123 http://localhost:8080/api/config
```

## Base URL

```
http://localhost:8080
```

## Endpoints

### Health Check

Public endpoint for health monitoring.

```
GET /health
```

**Response:**
```json
{
  "status": "ok",
  "timestamp": 1705312800
}
```

---

### Configuration

#### Get Configuration

```
GET /api/config
```

**Response:**
```json
{
  "discord": {
    "bot_name": "EZyapper",
    "reply_percentage": 0.15,
    "cooldown_seconds": 5
  },
  "ai": {
    "model": "gpt-4o-mini",
    "vision_model": "gpt-4o",
    "max_tokens": 1024,
    "temperature": 0.8
  },
  "memory": {
    "consolidation_interval": 50,
    "short_term_limit": 20,
    "retrieval": {
      "top_k": 5,
      "min_score": 0.75
    }
  },
  "web": {
    "port": 8080
  }
}
```

#### Update Discord Configuration

```
PUT /api/config/discord
```

**Request Body:**
```json
{
  "bot_name": "MyBot",
  "reply_percentage": 0.25,
  "cooldown_seconds": 3,
  "max_responses_per_minute": 15
}
```

**Response:**
```json
{
  "message": "discord config updated",
  "config": {
    "bot_name": "MyBot",
    "reply_percentage": 0.25,
    "cooldown_seconds": 3,
    "max_responses_per_minute": 15
  }
}
```

#### Update AI Configuration

```
PUT /api/config/ai
```

**Request Body:**
```json
{
  "model": "gpt-4o",
  "max_tokens": 2048,
  "temperature": 0.7,
  "system_prompt": "You are a helpful assistant."
}
```

**Response:**
```json
{
  "message": "ai config updated",
  "config": {
    "model": "gpt-4o",
    "max_tokens": 2048,
    "temperature": 0.7
  }
}
```

---

### Blacklist Management

#### Get Blacklist

```
GET /api/blacklist
```

**Response:**
```json
{
  "users": ["123456789", "987654321"],
  "channels": ["111222333"],
  "guilds": []
}
```

#### Add to Blacklist

```
POST /api/blacklist
```

**Request Body:**
```json
{
  "type": "user",
  "id": "123456789012345678"
}
```

| Type | Description |
|------|-------------|
| `user` | User ID to blacklist |
| `channel` | Channel ID to blacklist |
| `guild` | Guild ID to blacklist |

**Response:**
```json
{
  "message": "added to blacklist"
}
```

#### Remove from Blacklist

```
DELETE /api/blacklist/:type/:id
```

**Example:**
```
DELETE /api/blacklist/user/123456789012345678
```

**Response:**
```json
{
  "message": "removed from blacklist"
}
```

---

### Whitelist Management

#### Get Whitelist

```
GET /api/whitelist
```

**Response:**
```json
{
  "channels": ["123456789", "987654321"]
}
```

#### Add to Whitelist

```
POST /api/whitelist
```

**Request Body:**
```json
{
  "type": "channel",
  "id": "123456789012345678"
}
```

**Response:**
```json
{
  "message": "added to whitelist"
}
```

#### Remove from Whitelist

```
DELETE /api/whitelist/:type/:id
```

**Example:**
```
DELETE /api/whitelist/channel/123456789012345678
```

**Response:**
```json
{
  "message": "removed from whitelist"
}
```

---

### Memory Management

#### Get User Memories

```
GET /api/memories/:userID
```

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `limit` | int | 50 | Number of memories to return |

**Response:**
```json
{
  "user_id": "123456789",
  "count": 3,
  "memories": [
    {
      "id": "uuid-1",
      "user_id": "123456789",
      "memory_type": "fact",
      "content": "User likes programming in Go",
      "summary": "User likes Go programming",
      "keywords": ["programming", "go"],
      "confidence": 0.9,
      "created_at": "2024-01-15T10:00:00Z"
    }
  ]
}
```

#### Search Memories

```
GET /api/memories/:userID/search?q=programming
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | yes | Search query |

**Response:**
```json
{
  "user_id": "123456789",
  "query": "programming",
  "count": 2,
  "memories": [
    {
      "id": "uuid-1",
      "content": "User likes programming in Go",
      "summary": "User likes Go programming",
      "confidence": 0.9
    }
  ]
}
```

#### Delete Memory

```
DELETE /api/memories/:userID/:memoryID
```

**Response:**
```json
{
  "message": "memory deleted"
}
```

#### Clear All Memories

```
DELETE /api/memories/:userID
```

**Response:**
```json
{
  "message": "all user data deleted"
}
```

---

### Profile Management

#### Get User Profile

```
GET /api/profiles/:userID
```

**Response:**
```json
{
  "user_id": "123456789",
  "traits": ["friendly", "curious"],
  "facts": {
    "location": "San Francisco",
    "job": "Software Engineer"
  },
  "preferences": {
    "language": "Go",
    "editor": "VS Code"
  },
  "interests": ["programming", "gaming", "music"],
  "message_count": 150,
  "memory_count": 12,
  "first_seen_at": "2024-01-01T00:00:00Z",
  "last_active_at": "2024-01-15T10:00:00Z"
}
```

#### Update Profile

```
PUT /api/profiles/:userID
```

**Request Body:**
```json
{
  "traits": ["friendly", "helpful"],
  "facts": {
    "location": "New York"
  },
  "preferences": {
    "theme": "dark"
  },
  "interests": ["coding", "reading"]
}
```

**Response:**
```json
{
  "message": "profile updated",
  "profile": {
    "user_id": "123456789",
    "traits": ["friendly", "helpful"],
    "facts": {
      "location": "New York"
    },
    "preferences": {
      "theme": "dark"
    },
    "interests": ["coding", "reading"]
  }
}
```

#### Delete Profile

```
DELETE /api/profiles/:userID
```

**Response:**
```json
{
  "message": "profile deleted"
}
```

---

### Consolidation

#### Trigger Consolidation

```
POST /api/consolidate/:userID
```

Triggers async memory consolidation for a user.

**Response:**
```json
{
  "message": "consolidation triggered",
  "user_id": "123456789"
}
```

---

### Logs

#### Get Logs

```
GET /api/logs
```

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `lines` | int | 100 | Number of lines to return |

**Response:**
```json
{
  "logs": [
    "2024-01-15T10:00:00Z INFO Starting EZyapper...",
    "2024-01-15T10:00:01Z INFO Memory service initialized",
    "2024-01-15T10:00:02Z INFO Bot is now running"
  ]
}
```

---

### Plugins

#### List Plugins

```
GET /api/plugins
```

**Response:**
```json
{
  "plugins": [
    {
      "name": "anti-spam",
      "version": "0.0.0",
      "author": "EZyapper",
      "description": "Prevents spam messages",
      "priority": 100,
      "enabled": true
    }
  ]
}
```

#### Enable Plugin

```
POST /api/plugins/:name/enable
```

**Response:**
```json
{
  "message": "plugin enabled",
  "name": "anti-spam"
}
```

#### Disable Plugin

```
POST /api/plugins/:name/disable
```

**Response:**
```json
{
  "message": "plugin disabled",
  "name": "anti-spam"
}
```

---

### Statistics

#### Get Statistics

```
GET /api/stats
```

**Response:**
```json
{
  "uptime": 86400,
  "stats": {
    "total_users": 89,
    "total_memories": 523,
    "total_messages": 1523
  }
}
```

#### Get User Statistics

```
GET /api/stats/:userID
```

**Response:**
```json
{
  "user_id": "123456789",
  "message_count": 150,
  "memory_count": 12,
  "first_seen_at": "2024-01-01T00:00:00Z",
  "last_active_at": "2024-01-15T10:00:00Z"
}
```

---

## Error Responses

All endpoints return consistent error responses:

```json
{
  "error": "description of the error"
}
```

**HTTP Status Codes:**
| Code | Description |
|------|-------------|
| 200 | Success |
| 201 | Created |
| 400 | Bad Request |
| 401 | Unauthorized |
| 404 | Not Found |
| 500 | Internal Server Error |

---

## Rate Limiting

Management APIs currently do not emit dedicated HTTP rate-limit headers.

---

## Examples

### cURL

```bash
# Get configuration
curl -u admin:password http://localhost:8080/api/config

# Update AI settings
curl -u admin:password \
  -X PUT \
  -H "Content-Type: application/json" \
  -d '{"temperature": 0.9}' \
  http://localhost:8080/api/config/ai

# Get user memories
curl -u admin:password \
  http://localhost:8080/api/memories/123456789

# Search memories
curl -u admin:password \
  "http://localhost:8080/api/memories/123456789/search?q=programming"

# Get user profile
curl -u admin:password \
  http://localhost:8080/api/profiles/123456789

# Trigger consolidation
curl -u admin:password \
  -X POST \
  http://localhost:8080/api/consolidate/123456789

# Add user to blacklist
curl -u admin:password \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{"type": "user", "id": "123456789"}' \
  http://localhost:8080/api/blacklist
```

### JavaScript

```javascript
const API = 'http://localhost:8080/api';
const AUTH = btoa('admin:password');

async function getConfig() {
  const response = await fetch(`${API}/config`, {
    headers: {
      'Authorization': `Basic ${AUTH}`
    }
  });
  return response.json();
}

async function getMemories(userID) {
  const response = await fetch(`${API}/memories/${userID}`, {
    headers: {
      'Authorization': `Basic ${AUTH}`
    }
  });
  return response.json();
}

async function searchMemories(userID, query) {
  const response = await fetch(
    `${API}/memories/${userID}/search?q=${encodeURIComponent(query)}`,
    {
      headers: {
        'Authorization': `Basic ${AUTH}`
      }
    }
  );
  return response.json();
}

async function updateAIConfig(settings) {
  const response = await fetch(`${API}/config/ai`, {
    method: 'PUT',
    headers: {
      'Authorization': `Basic ${AUTH}`,
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(settings)
  });
  return response.json();
}
```

### Python

```python
import requests
from requests.auth import HTTPBasicAuth

API = 'http://localhost:8080/api'
AUTH = HTTPBasicAuth('admin', 'password')

def get_config():
    response = requests.get(f'{API}/config', auth=AUTH)
    return response.json()

def get_memories(user_id):
    response = requests.get(f'{API}/memories/{user_id}', auth=AUTH)
    return response.json()

def search_memories(user_id, query):
    response = requests.get(
        f'{API}/memories/{user_id}/search',
        params={'q': query},
        auth=AUTH
    )
    return response.json()

def update_ai_config(settings):
    response = requests.put(
        f'{API}/config/ai',
        auth=AUTH,
        json=settings
    )
    return response.json()

def trigger_consolidation(user_id):
    response = requests.post(
        f'{API}/consolidate/{user_id}',
        auth=AUTH
    )
    return response.json()
```
