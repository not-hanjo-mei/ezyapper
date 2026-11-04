# Deployment

This guide covers a few common ways to run EZyapper. Pick whichever matches your environment — they're all the same binary underneath.

## Prerequisites

- **Go 1.27+** (only needed if you build from source)
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
# Linux (amd64, arm64, 386, arm)
GOOS=linux GOARCH=amd64 go build -o ezyapper-linux-amd64 ./cmd/bot
GOOS=linux GOARCH=arm64 go build -o ezyapper-linux-arm64 ./cmd/bot
GOOS=linux GOARCH=386  go build -o ezyapper-linux-386  ./cmd/bot
GOOS=linux GOARCH=arm  GOARM=7 go build -o ezyapper-linux-arm ./cmd/bot

# Windows
GOOS=windows GOARCH=amd64 go build -o ezyapper.exe        ./cmd/bot
GOOS=windows GOARCH=arm64 go build -o ezyapper-arm64.exe  ./cmd/bot
GOOS=windows GOARCH=386  go build -o ezyapper-386.exe     ./cmd/bot

# macOS
GOOS=darwin GOARCH=amd64 go build -o ezyapper-macos-amd64 ./cmd/bot
GOOS=darwin GOARCH=arm64 go build -o ezyapper-macos-arm64 ./cmd/bot

# FreeBSD
GOOS=freebsd GOARCH=amd64 go build -o ezyapper-freebsd-amd64 ./cmd/bot
GOOS=freebsd GOARCH=arm64 go build -o ezyapper-freebsd-arm64 ./cmd/bot

# Android / Termux (pure-Go static binary)
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o ezyapper-android-arm64 ./cmd/bot
# 32-bit ARM Android: Go requires cgo (external linking) for android/arm,
# so use the Android NDK's clang:
CGO_ENABLED=1 GOOS=android GOARCH=arm GOARM=7 \
  CC=$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/armv7a-linux-androideabi21-clang \
  go build -o ezyapper-android-arm ./cmd/bot

# Niche Linux archs (mipsle, mips64le, loong64)
GOOS=linux GOARCH=mipsle    go build -o ezyapper-linux-mipsle    ./cmd/bot
GOOS=linux GOARCH=mips64le  go build -o ezyapper-linux-mips64le  ./cmd/bot
GOOS=linux GOARCH=loong64   go build -o ezyapper-linux-loong64   ./cmd/bot
```

`make build-all` does linux/windows/darwin amd64. CI builds the full matrix across three path-filtered workflows:

| Workflow | Matrix entries | What it builds |
|---|---|---|
| [`build-main.yml`](../.github/workflows/build-main.yml) | 17 | Main binary (`cmd/bot`) — triggers on changes to Go source |
| [`build-go-plugins.yml`](../.github/workflows/build-go-plugins.yml) | 17 | Go plugin binaries — triggers on plugin source |
| [`build-command-plugins.yml`](../.github/workflows/build-command-plugins.yml) | 18 | Zig/C plugin binaries (including musl-static Android/Termux) — triggers on command plugin source |

### Don't want to compile? Grab a prebuilt binary

GitHub Actions publishes per-platform artifacts on every push. Hit [Actions](https://github.com/not-hanjo-mei/ezyapper/actions), pick the most recent successful **build** run, and download the artifact for your OS/arch.

You'll also find prebuilt **plugin binaries** in the same artifacts — Go plugins for 17 platforms, plus Zig and C command plugins for 18 targets (including `-musl` static Android/Termux builds). Drop them into `plugins/` if you want a head start.

---

## Docker

> [!WARNING]
> **The Docker build is unverified.** The `Dockerfile` and `docker-compose.yml` have not been tested against the current code. Reading them against the code shows these problems; work around them as described until they are fixed:
>
> - The build copies `config.yaml` from the build context into the image (`COPY --from=builder /build/config.yaml ...`), so the build fails without one, and whatever it contains (including secrets) ends up in the image. Mount the real file at run time instead and keep secrets in the environment.
> - The image has no ffmpeg, which `audio.transcode` and `video.transcode` need. See [ffmpeg in Docker](#ffmpeg-in-docker).
> - The health check probes `/health`, which is not a real endpoint (see [Health check](#health-check)).

## Docker Compose

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

The compose file mounts `./config.yaml` read-only into the container. A variable in `.env` overrides a key **only if that key is present in `config.yaml`** (see [CONFIGURATION.md](CONFIGURATION.md#environment-variable-overrides)); keep the keys in the file with placeholder values. The mount is read-only, so saving the config from the WebUI fails; edit the file on the host instead (with hot reload enabled the bot picks up the change).

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
  ezyapper --config /app/config.yaml
```

Arguments after the image name replace the image's `CMD`, so `--config /app/config.yaml` works around the broken default.

The image:

- Base: `golang:1.27-alpine` (build) → `alpine:3.24` (runtime)
- Runs as non-root `appuser`
- Healthcheck: `wget --spider http://localhost:8080/health`. There is no `/health` route: the request falls through to the dashboard, which redirects to `/login`, and `wget` follows the redirect. So the check passes whenever the WebUI is running and fails whenever `operations.web.enabled` is `false`. Switch to a different probe (e.g., `pgrep ezyapper`) if the WebUI is off.
- No ffmpeg (see below).

### ffmpeg in Docker

`core.ai.modalities.audio.transcode` and `core.ai.modalities.video.transcode` run ffmpeg, and the bot refuses to start when one of them is enabled and ffmpeg is missing. The runtime stage of the `Dockerfile` installs only `ca-certificates` and `tzdata`. To use transcoding, add ffmpeg to that line and rebuild:

```dockerfile
# Runtime stage
RUN apk add --no-cache ca-certificates tzdata ffmpeg
```

This makes the image noticeably larger. Without transcoding the bot never looks for ffmpeg, so leave it out if you do not use those options.

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
ExecStart=/opt/ezyapper/ezyapper --config /opt/ezyapper/config.yaml
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

The bot expects Qdrant on **gRPC port 6334** (REST 6333 is for diagnostics). These collections are created automatically at startup:

| Collection | Holds |
|------------|-------|
| `memories_source` | Every memory's text and metadata. The source of truth; independent of the embedding model. |
| `memories_vec_<model>_<dim>` | Vectors for the configured embedding model and `vector_size`, e.g. `memories_vec_text_embedding_3_small_1536`. One collection per model and dimension. |
| `profiles` | One record per user. |
| `relationships` | User-to-user links. |

A `memories` collection from versions before the two-tier store is migrated into `memories_source` on startup and can be deleted afterwards (see [ARCHITECTURE.md](ARCHITECTURE.md#internalmemory--vector-store--consolidation)).

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

Switching models does **not** require deleting anything: change `embedding.model` and `vector_size` and the bot creates the new model's `memories_vec_...` collection and re-embeds every memory into it in the background. See [Switching Embedding Models](CONFIGURATION.md#switching-embedding-models).

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
          args: ["--config", "/app/config.yaml"]
          # There is no /health route; this probe passes only while the
          # WebUI is enabled (see Health check). Otherwise use an exec probe.
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

There is no health endpoint. `/health` is not a route: it falls through to the dashboard, which redirects an unauthenticated request to `/login`:

```bash
curl -sL -o /dev/null -w '%{http_code}\n' http://localhost:8080/health   # 200 after the redirect
```

That only tells you the WebUI is serving. When it is off, use process supervision instead.

### Qdrant health

```bash
curl http://localhost:6333/healthz
curl http://localhost:6333/collections   # memories_source, memories_vec_..., profiles, relationships
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
curl -X POST http://localhost:6333/collections/memories_source/snapshots
curl -X POST http://localhost:6333/collections/profiles/snapshots
curl -X POST http://localhost:6333/collections/relationships/snapshots

# List snapshots
curl http://localhost:6333/collections/memories_source/snapshots

# Download
curl http://localhost:6333/collections/memories_source/snapshots/<name> > memories_source.snapshot
```

`memories_source`, `profiles` and `relationships` hold all the data. A `memories_vec_...` collection can be rebuilt from `memories_source` (the bot re-embeds missing records on startup), so backing it up only saves the re-embedding time.

### Restore

```bash
curl -X PUT http://localhost:6333/collections/memories_source/snapshots/upload \
  -H "Content-Type: multipart/form-data" \
  -F "snapshot=@memories_source.snapshot"
```

Or restore from a copy of `qdrant_storage` if you snapshot the volume directly.

### Quick backup script

```bash
#!/bin/bash
set -euo pipefail
BACKUP_DIR="/backup/ezyapper"
DATE=$(date +%Y%m%d_%H%M%S)
mkdir -p "$BACKUP_DIR"

for col in memories_source profiles relationships; do
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
./ezyapper --config config.yaml 2>&1 | grep -i token
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

Happens when `memory_pipeline.qdrant.vector_size` does not match what the embedding model returns. The vector collection is named after the model **and** the configured dimension, so the fix is only to correct `vector_size` and restart (or save, with hot reload on): the bot then uses `memories_vec_<model>_<correct dim>` and re-embeds the memories from `memories_source` into it in the background. Do **not** delete collections; `memories_source` holds all memory text and deleting it loses every memory. The collection created with the wrong dimension can be deleted once it is no longer used:

```bash
curl -X DELETE http://localhost:6333/collections/memories_vec_<model>_<wrong dim> \
  -H "api-key: your-key"
```

For the OpenAI models `text-embedding-3-small`, `text-embedding-3-large` and `text-embedding-ada-002` the config check already rejects a mismatched `vector_size` at startup.

### `/health` says unhealthy but the bot is fine

There is no real `/health` endpoint; the probe only succeeds because the WebUI answers it with a redirect to `/login`. If `operations.web.enabled: false`, nothing serves HTTP and the Docker healthcheck fails. Either:

- enable the WebUI, or
- override the healthcheck in your runtime (compose, k8s) to check the process instead of HTTP.

## Capturing model requests

Set `EZYAPPER_CAPTURE_DIR` to a directory to record every request the bot
sends to the chat, decision, vision, consolidation and embedding APIs, with
the response, as one JSON object per line in
`capture-<timestamp>.jsonl`. API keys and auth headers are redacted; image
downloads are recorded by size only; bodies are capped at 1 MiB. Leave the
variable unset in normal operation.

```bash
EZYAPPER_CAPTURE_DIR=./captures ./ezyapper --config config.yaml
jq -r 'select(.label|startswith("ai:")) | .request_body.messages[] | "\(.role):\n\(.content)\n"' captures/*.jsonl
```
