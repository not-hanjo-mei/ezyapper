# Deployment

This guide covers a few common ways to run EZyapper. Pick whichever matches your environment — they're all the same binary underneath.

## Prerequisites

- **Go 1.25+** (only needed if you build from source)
- **Discord bot token** — from the [Discord Developer Portal](https://discord.com/developers/applications)
- **OpenAI-compatible API key** (or local LLM endpoint)
- **Qdrant vector database** — required if you want long-term memory. Bundled in the Docker Compose setup.

---

## Building from Source

### Standard

```bash
git clone <repo-url>
cd ezyapper
go mod download
go build -o ezyapper ./cmd/bot
```

### Stripped binary

```bash
go build -ldflags="-s -w" -o ezyapper ./cmd/bot
```

| Flag | Effect |
|------|--------|
| `-s` | Strip symbol table |
| `-w` | Strip DWARF debug info |

### Cross-compilation

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o ezyapper-linux-amd64 ./cmd/bot
GOOS=linux GOARCH=arm64 go build -o ezyapper-linux-arm64 ./cmd/bot

# Windows
GOOS=windows GOARCH=amd64 go build -o ezyapper.exe ./cmd/bot
GOOS=windows GOARCH=arm64 go build -o ezyapper-arm64.exe ./cmd/bot

# macOS
GOOS=darwin GOARCH=amd64 go build -o ezyapper-macos-amd64 ./cmd/bot
GOOS=darwin GOARCH=arm64 go build -o ezyapper-macos-arm64 ./cmd/bot
```

`make build-all` does the three amd64 platforms in one shot. CI builds the full six-platform matrix.

### Don't want to compile? Grab a prebuilt binary

GitHub Actions publishes per-platform artifacts on every push. Hit [Actions](https://github.com/not-hanjo-mei/ezyapper/actions), pick the most recent successful **Build** run, and download the artifact for your OS/arch.

You'll also find prebuilt **plugin binaries** in the same artifacts — Go plugins for six platforms, plus Zig and C command plugins for four. Drop them into `plugins/` if you want a head start.

---

## Docker Compose (Recommended)

The included `docker-compose.yml` runs both the bot and Qdrant.

### 1. Set up your env file

```bash
# macOS/Linux
cp examples/.env.example .env

# Windows PowerShell
Copy-Item examples\.env.example .env
```

Edit `.env` and fill in at least:

```env
EZYAPPER_CORE_DISCORD_TOKEN=your_discord_token
EZYAPPER_CORE_AI_API_KEY=your_api_key
EZYAPPER_CORE_AI_API_BASE_URL=https://api.openai.com/v1
EZYAPPER_OPERATIONS_WEB_PASSWORD=replace_me
EZYAPPER_MEMORY_PIPELINE_QDRANT_HOST=qdrant
```

> Env-var keys mirror the YAML path with dots replaced by underscores. See [CONFIGURATION.md](CONFIGURATION.md#environment-variable-overrides) for the full mapping.

### 2. Drop in a `config.yaml`

```bash
cp examples/config.yaml.example config.yaml
```

The compose file mounts `./config.yaml` read-only into the container. Anything in `.env` overrides the file.

### 3. Up and running

```bash
docker compose up -d
docker compose logs -f
```

Stop with `docker compose down`. Volumes (`ezyapper-logs`, `qdrant_storage`) survive a `down`; add `-v` if you want to wipe them.

### Qdrant network exposure

By default the compose file does **not** expose Qdrant ports to the host. The bot reaches it via the internal `bot-network` bridge. If you need to query Qdrant from your machine for debugging, add a port mapping yourself:

```yaml
  qdrant:
    image: qdrant/qdrant:v1.17.0
    ports:
      - "6333:6333"   # REST
      - "6334:6334"   # gRPC
```

---

## Manual Docker

Build and run without compose:

```bash
docker build -t ezyapper .

docker run -d \
  --name ezyapper \
  -e EZYAPPER_CORE_DISCORD_TOKEN=your_token \
  -e EZYAPPER_CORE_AI_API_KEY=your_key \
  -e EZYAPPER_CORE_AI_API_BASE_URL=https://api.openai.com/v1 \
  -e EZYAPPER_MEMORY_PIPELINE_QDRANT_HOST=your_qdrant_host \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  -p 8080:8080 \
  ezyapper
```

The image:

- Base: `golang:1.25-alpine` (build) → `alpine:3.19` (runtime)
- Runs as non-root `appuser`
- Healthcheck: `wget /health` against the WebUI. **Note:** `/health` only responds when `operations.web.enabled: true`. If the WebUI is off, the healthcheck will mark the container unhealthy — switch to a different probe (e.g., `pgrep ezyapper`) or accept that signal.

---

## Systemd (Linux)

`/etc/systemd/system/ezyapper.service`:

```ini
[Unit]
Description=EZyapper Discord Bot
After=network.target qdrant.service
Wants=network-online.target

[Service]
Type=simple
User=ezyapper
Group=ezyapper
WorkingDirectory=/opt/ezyapper
ExecStart=/opt/ezyapper/ezyapper -config /opt/ezyapper/config.yaml
Restart=on-failure
RestartSec=10
StartLimitBurst=5
StartLimitInterval=60

Environment=EZYAPPER_CORE_DISCORD_TOKEN=your_token
Environment=EZYAPPER_CORE_AI_API_KEY=your_key
Environment=EZYAPPER_MEMORY_PIPELINE_QDRANT_HOST=localhost
Environment=EZYAPPER_MEMORY_PIPELINE_QDRANT_PORT=6334

StandardOutput=journal
StandardError=journal
SyslogIdentifier=ezyapper

[Install]
WantedBy=multi-user.target
```

Setup:

```bash
sudo useradd -r -s /bin/false ezyapper
sudo mkdir -p /opt/ezyapper
sudo chown ezyapper:ezyapper /opt/ezyapper
sudo chmod 750 /opt/ezyapper

sudo cp ezyapper /opt/ezyapper/
sudo cp config.yaml /opt/ezyapper/
sudo chown ezyapper:ezyapper /opt/ezyapper/*

sudo systemctl daemon-reload
sudo systemctl enable --now ezyapper
sudo journalctl -u ezyapper -f
```

---

## Qdrant Setup

The bot expects Qdrant on **gRPC port 6334** (REST 6333 is for diagnostics). Three collections (`memories`, `profiles`, `relationships`) are created automatically at first startup.

### Standalone container

```bash
docker run -d \
  --name qdrant \
  -p 6333:6333 \
  -p 6334:6334 \
  -v qdrant_storage:/qdrant/storage \
  qdrant/qdrant:v1.17.0
```

### Vector size

`memory_pipeline.qdrant.vector_size` must match your embedding model's output dimensions. The most common values:

| Model | Dimensions |
|-------|-----------|
| `text-embedding-3-small` / `ada-002` | 1536 |
| `text-embedding-3-large` | 3072 |
| MiniLM, M3E variants | 384–1024 |
| BGE | 1024 |

Switching models requires deleting the existing collections — see [Vector Dimension Errors](#vector-dimension-errors) below.

---

## Kubernetes

Minimal deployment. Adapt resources and labels to your cluster.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ezyapper
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ezyapper
  template:
    metadata:
      labels:
        app: ezyapper
    spec:
      containers:
        - name: ezyapper
          image: ezyapper:latest
          env:
            - name: EZYAPPER_CORE_DISCORD_TOKEN
              valueFrom:
                secretKeyRef:
                  name: ezyapper-secrets
                  key: discord-token
            - name: EZYAPPER_CORE_AI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: ezyapper-secrets
                  key: api-key
            - name: EZYAPPER_MEMORY_PIPELINE_QDRANT_HOST
              value: qdrant
            - name: EZYAPPER_MEMORY_PIPELINE_QDRANT_PORT
              value: "6334"
          ports:
            - containerPort: 8080
              name: webui
          resources:
            requests: { cpu: 250m, memory: 256Mi }
            limits:   { cpu: 500m, memory: 512Mi }
          # If WebUI is enabled, the /health endpoint exists.
          # If not, drop the probes or switch to an exec probe.
          livenessProbe:
            httpGet: { path: /health, port: 8080 }
            initialDelaySeconds: 10
            periodSeconds: 30
          readinessProbe:
            httpGet: { path: /health, port: 8080 }
            initialDelaySeconds: 5
            periodSeconds: 10
          volumeMounts:
            - name: config
              mountPath: /app/config.yaml
              subPath: config.yaml
              readOnly: true
      volumes:
        - name: config
          configMap:
            name: ezyapper-config
---
apiVersion: v1
kind: Secret
metadata:
  name: ezyapper-secrets
type: Opaque
stringData:
  discord-token: your_discord_token
  api-key: your_api_key
```

For Qdrant in-cluster, run a single-replica StatefulSet with a PVC at `/qdrant/storage`. The official Helm chart works fine.

---

## Reverse Proxy (Nginx)

If you exposed the WebUI, put TLS in front of it.

```nginx
server {
    listen 80;
    server_name bot.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

```bash
sudo certbot --nginx -d bot.example.com
```

Caddy works just as well — it'll handle TLS automatically.

---

## Monitoring

### Health check

```bash
curl http://localhost:8080/health
```

Returns `{"status":"ok","timestamp":...}` only when the WebUI is enabled. There's no separate health endpoint when it's off — use process supervision instead.

### Qdrant health

```bash
curl http://localhost:6333/healthz
curl http://localhost:6333/collections   # lists memories, profiles, relationships
```

### Logs

```bash
# Docker Compose
docker compose logs -f

# systemd
sudo journalctl -u ezyapper -f

# log file (per config)
tail -f /opt/ezyapper/logs/ezyapper.log
```

### Metrics

There's no built-in `/metrics` endpoint. If you want metrics, scrape your container runtime or front the WebUI with a sidecar that watches application logs.

---

## Backup & Recovery

### Snapshot Qdrant

```bash
# Trigger a snapshot (REST API)
curl -X POST http://localhost:6333/collections/memories/snapshots
curl -X POST http://localhost:6333/collections/profiles/snapshots
curl -X POST http://localhost:6333/collections/relationships/snapshots

# List snapshots
curl http://localhost:6333/collections/memories/snapshots

# Download
curl http://localhost:6333/collections/memories/snapshots/<name> > memories.snapshot
```

### Restore

```bash
curl -X PUT http://localhost:6333/collections/memories/snapshots/upload \
  -H "Content-Type: multipart/form-data" \
  -F "snapshot=@memories.snapshot"
```

Or restore from a copy of `qdrant_storage` if you snapshot the volume directly.

### Quick backup script

```bash
#!/bin/bash
set -euo pipefail
BACKUP_DIR="/backup/ezyapper"
DATE=$(date +%Y%m%d_%H%M%S)
mkdir -p "$BACKUP_DIR"

for col in memories profiles relationships; do
  curl -sX POST "http://localhost:6333/collections/$col/snapshots" >/dev/null
  # Snapshot files are written into the qdrant volume; copy them out:
  cp /var/lib/docker/volumes/qdrant_storage/_data/snapshots/$col-* \
     "$BACKUP_DIR/${col}_${DATE}.snapshot"
done

# Keep last 7 days
find "$BACKUP_DIR" -name "*.snapshot" -mtime +7 -delete
```

---

## Scaling

The bot is designed for a single instance per Discord application. Discord bots are inherently stateful (gateway connection, presence), and we don't shard.

To handle larger guilds, the pragmatic options are:

1. **Vertical scaling** — give the bot more CPU/RAM, give Qdrant a faster disk.
2. **External Qdrant cluster** — run Qdrant separately with replication if memory durability matters.
3. **Discord sharding** — supported by `discordgo`, but EZyapper's session code doesn't currently coordinate shards. Not recommended without code changes.

The web layer is stateless (sessions live in memory but auto-expire), so you can put it behind a load balancer if you ever split it out — but right now it's bound to the same process as the bot.

---

## Security Checklist

- [ ] Real WebUI password (not `changeme123`)
- [ ] Secrets in env vars, not in `config.yaml` on disk
- [ ] HTTPS terminated by a reverse proxy
- [ ] Firewall: WebUI port not exposed publicly unless intentional
- [ ] Qdrant API key set if your instance is reachable beyond localhost
- [ ] `chmod 400 config.yaml`
- [ ] Container runs as non-root (the provided Dockerfile already does this)
- [ ] Dependency updates (`make deps-update`, `make vuln`)
- [ ] Audit logging enabled at the reverse proxy or systemd level

---

## Troubleshooting

### Bot doesn't connect to Discord

```bash
./ezyapper -config config.yaml 2>&1 | grep -i token
```

Check that:

- The token is valid (try regenerating in the Developer Portal)
- **Privileged Gateway Intents** are enabled in the Developer Portal:
  - Message Content Intent (required)
  - Server Members Intent (required for member-related tools)

### AI API errors

```bash
curl -H "Authorization: Bearer YOUR_KEY" https://api.openai.com/v1/models
```

If that fails, the issue is upstream. If it succeeds, double-check the `core.ai.api_base_url` and `core.ai.api_key` in your config.

### Qdrant connection errors

```bash
curl http://localhost:6333/healthz
docker logs qdrant
```

Verify host/port match `memory_pipeline.qdrant.host` / `memory_pipeline.qdrant.port`.

### Memory pressure

Trim a few knobs:

```yaml
memory_pipeline:
  memory:
    short_term_limit: 10            # was 20
    retrieval:
      top_k: 3                      # was 5

core:
  ai:
    max_tokens: 512                 # was 1024+
```

For Qdrant memory, watch `docker stats qdrant`. The big lever is collection size; consider lowering `prune_age_days` and bumping `prune_decay_threshold` to be more aggressive about cleanup.

### Vector dimension errors

Error: `Vector dimension error: expected dim: X, got Y`

Happens when you change embedding models without dropping collections. Fix:

```bash
curl -X DELETE http://localhost:6333/collections/memories
curl -X DELETE http://localhost:6333/collections/profiles
curl -X DELETE http://localhost:6333/collections/relationships
```

…or with auth:

```bash
curl -X DELETE https://your-cluster.qdrant.io:6333/collections/memories \
  -H "api-key: your-key"
```

Update `embedding.model` and `qdrant.vector_size`, restart. **All memory data is lost** by this — snapshot first if you care.

### `/health` says unhealthy but the bot is fine

The `/health` endpoint is part of the WebUI. If `operations.web.enabled: false`, there's nothing serving HTTP and the Docker healthcheck will fail. Either:

- enable the WebUI, or
- override the healthcheck in your runtime (compose, k8s) to check the process instead of HTTP.
