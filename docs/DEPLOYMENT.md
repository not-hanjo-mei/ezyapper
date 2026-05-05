# Deployment Guide

This guide covers various deployment options for EZyapper.

## Prerequisites

- Go 1.24+ (for building from source)
- Discord Bot Token
- OpenAI-compatible API Key
- **Qdrant Vector Database** (included in Docker Compose)

## Building from Source

### Standard Build

```bash
git clone <repository-url>
cd ezyapper
go mod download
go build -o ezyapper ./cmd/bot
```

### Optimized Build

```bash
go build -ldflags="-s -w" -o ezyapper ./cmd/bot
```

| Flag | Description |
|------|-------------|
| `-s` | Strip symbol table |
| `-w` | Strip DWARF debug info |

### Cross-Compilation

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o ezyapper-linux ./cmd/bot

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -o ezyapper-arm64 ./cmd/bot

# Windows
GOOS=windows GOARCH=amd64 go build -o ezyapper.exe ./cmd/bot

# macOS
GOOS=darwin GOARCH=amd64 go build -o ezyapper-macos ./cmd/bot
```

## Docker Deployment (Recommended)

### Using Docker Compose

Docker Compose includes both the bot and Qdrant vector database.

1. Create environment file:
```bash
cp .env.example .env
```

2. Edit `.env`:
```env
EZYAPPER_DISCORD_TOKEN=your_discord_token
EZYAPPER_AI_API_KEY=your_api_key
EZYAPPER_AI_API_BASE_URL=https://api.openai.com/v1
EZYAPPER_WEB_PASSWORD=secure_password
EZYAPPER_QDRANT_HOST=qdrant
```

3. Start:
```bash
docker-compose up -d
```

4. View logs:
```bash
docker-compose logs -f
```

5. Stop:
```bash
docker-compose down
```

### Manual Docker Build

```bash
docker build -t ezyapper .
docker run -d \
  --name ezyapper \
  -e EZYAPPER_DISCORD_TOKEN=your_token \
  -e EZYAPPER_AI_API_KEY=your_key \
  -e EZYAPPER_QDRANT_HOST=your_qdrant_host \
  -p 8080:8080 \
  ezyapper
```

### Docker Configuration

| Environment Variable | Description |
|---------------------|-------------|
| `EZYAPPER_DISCORD_TOKEN` | Discord bot token |
| `EZYAPPER_AI_API_KEY` | AI API key |
| `EZYAPPER_AI_API_BASE_URL` | AI API endpoint |
| `EZYAPPER_WEB_PASSWORD` | WebUI password |
| `EZYAPPER_QDRANT_HOST` | Qdrant host (use "qdrant" for Docker Compose) |
| `EZYAPPER_QDRANT_PORT` | Qdrant port (example: 6334) |
| `EZYAPPER_QDRANT_API_KEY` | Qdrant API key (optional, for authenticated instances) |
| `TZ` | Timezone (e.g., `America/New_York`) |

## Systemd Service (Linux)

### Create Service File

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

Environment=EZYAPPER_DISCORD_TOKEN=your_token
Environment=EZYAPPER_AI_API_KEY=your_key
Environment=EZYAPPER_QDRANT_HOST=localhost
Environment=EZYAPPER_QDRANT_PORT=6334

StandardOutput=journal
StandardError=journal
SyslogIdentifier=ezyapper

[Install]
WantedBy=multi-user.target
```

### Setup User and Directories

```bash
sudo useradd -r -s /bin/false ezyapper
sudo mkdir -p /opt/ezyapper
sudo chown ezyapper:ezyapper /opt/ezyapper
sudo chmod 750 /opt/ezyapper
```

### Install and Start

```bash
sudo cp ezyapper /opt/ezyapper/
sudo cp config.yaml /opt/ezyapper/
sudo chown ezyapper:ezyapper /opt/ezyapper/*

sudo systemctl daemon-reload
sudo systemctl enable ezyapper
sudo systemctl start ezyapper
```

### Manage Service

```bash
sudo systemctl status ezyapper
sudo systemctl restart ezyapper
sudo systemctl stop ezyapper
sudo journalctl -u ezyapper -f
```

## Qdrant Setup

### Docker (Recommended)

Qdrant is included in the docker-compose.yml:

```yaml
services:
  qdrant:
    image: qdrant/qdrant:v1.17.0
    volumes:
      - qdrant_storage:/qdrant/storage
    ports:
      - "6333:6333"  # REST API
      - "6334:6334"  # gRPC
```

### Standalone Qdrant

```bash
docker run -d \
  --name qdrant \
  -p 6333:6333 \
  -p 6334:6334 \
  -v qdrant_storage:/qdrant/storage \
  qdrant/qdrant:v1.17.0
```

### Qdrant Configuration

Qdrant collections are automatically created on startup:

- **memories** - Stores conversation memories (1536 dimensions)
- **profiles** - Stores user profiles (1536 dimensions)

No manual configuration needed.

## Kubernetes Deployment

### Deployment YAML

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ezyapper
  labels:
    app: ezyapper
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
        imagePullPolicy: IfNotPresent
        env:
        - name: EZYAPPER_DISCORD_TOKEN
          valueFrom:
            secretKeyRef:
              name: ezyapper-secrets
              key: discord-token
        - name: EZYAPPER_AI_API_KEY
          valueFrom:
            secretKeyRef:
              name: ezyapper-secrets
              key: api-key
        - name: EZYAPPER_QDRANT_HOST
          value: "qdrant-service"
        - name: EZYAPPER_QDRANT_PORT
          value: "6334"
        ports:
        - containerPort: 8080
          name: webui
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
```

### Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ezyapper-secrets
type: Opaque
stringData:
  discord-token: your_discord_token
  api-key: your_api_key
```

### Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: ezyapper
spec:
  selector:
    app: ezyapper
  ports:
  - port: 8080
    targetPort: 8080
  type: ClusterIP
```

### Qdrant in Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: qdrant
spec:
  replicas: 1
  selector:
    matchLabels:
      app: qdrant
  template:
    metadata:
      labels:
        app: qdrant
    spec:
      containers:
      - name: qdrant
        image: qdrant/qdrant:v1.17.0
        ports:
        - containerPort: 6333
        - containerPort: 6334
        volumeMounts:
        - name: qdrant-storage
          mountPath: /qdrant/storage
      volumes:
      - name: qdrant-storage
        persistentVolumeClaim:
          claimName: qdrant-pvc
---
apiVersion: v1
kind: Service
metadata:
  name: qdrant-service
spec:
  selector:
    app: qdrant
  ports:
  - name: rest
    port: 6333
  - name: grpc
    port: 6334
```

## Nginx Reverse Proxy

### Configuration

`/etc/nginx/sites-available/ezyapper`:

```nginx
server {
    listen 80;
    server_name bot.yourdomain.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### Enable Site

```bash
sudo ln -s /etc/nginx/sites-available/ezyapper /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### SSL with Let's Encrypt

```bash
sudo apt install certbot python3-certbot-nginx
sudo certbot --nginx -d bot.yourdomain.com
```

## Monitoring

### Health Check Endpoint

```bash
curl http://localhost:8080/health
```

Response:
```json
{
  "status": "ok",
  "timestamp": 1705312800
}
```

### Qdrant Health Check

```bash
# REST API
curl http://localhost:6333/healthz

# Collections
curl http://localhost:6333/collections
```

### Prometheus Metrics (Optional)

Add to `docker-compose.yml`:

```yaml
labels:
  - "prometheus.io/scrape=true"
  - "prometheus.io/port=8080"
  - "prometheus.io/path=/metrics"
```

### Log Monitoring

```bash
# Docker
docker logs -f ezyapper

# Docker Compose
docker-compose logs -f

# Systemd
sudo journalctl -u ezyapper -f

# Log file
tail -f logs/ezyapper.log
```

## Scaling Considerations

### Single Instance

Example configuration supports:
- Single Discord bot instance
- Single Qdrant instance
- Stateless design allows easy scaling

### Multiple Instances (Advanced)

For high-availability:

1. Use external Qdrant cluster
2. Configure Discord sharding
3. Use load balancer for WebUI
4. Each instance is stateless

## Backup and Recovery

### Qdrant Backup

```bash
# Create snapshot
curl -X POST http://localhost:6333/snapshots

# List snapshots
curl http://localhost:6333/snapshots

# Download snapshot
curl http://localhost:6333/snapshots/{snapshot_name} > backup.snapshot
```

### Automated Backup Script

```bash
#!/bin/bash
BACKUP_DIR="/backup/ezyapper"
DATE=$(date +%Y%m%d_%H%M%S)

mkdir -p $BACKUP_DIR

# Backup Qdrant
curl -X POST http://localhost:6333/snapshots
cp /var/lib/docker/volumes/qdrant_storage/_data/snapshots/* $BACKUP_DIR/qdrant_$DATE.snapshot

# Cleanup old backups
find $BACKUP_DIR -name "*.snapshot" -mtime +7 -delete
```

### Restore

```bash
# Upload and restore snapshot
curl -X POST http://localhost:6333/snapshots/upload \
  -H "Content-Type: multipart/form-data" \
  -F "snapshot=@backup.snapshot"
```

## Security Checklist

- [ ] Change default WebUI password
- [ ] Use environment variables for secrets
- [ ] Enable HTTPS via reverse proxy
- [ ] Restrict WebUI access with firewall
- [ ] Keep dependencies updated
- [ ] Enable audit logging
- [ ] Use read-only config file
- [ ] Run as non-root user
- [ ] Set appropriate file permissions
- [ ] Secure Qdrant (use authentication in production)

## Troubleshooting

### Bot Not Responding

1. Check Discord token:
```bash
./ezyapper -config config.yaml 2>&1 | grep -i token
```

2. Verify intents are enabled in Discord Developer Portal:
- Message Content Intent
- Server Members Intent (if needed)

### AI API Errors

1. Test API connection:
```bash
curl -H "Authorization: Bearer YOUR_KEY" \
  https://api.openai.com/v1/models
```

2. Check rate limits and credits

### Qdrant Connection Errors

1. Check Qdrant is running:
```bash
curl http://localhost:6333/healthz
```

2. Verify connection settings in config.yaml

3. Check Qdrant logs:
```bash
docker logs qdrant
```

### Memory Issues

1. Reduce context window:
```yaml
memory_pipeline:
  memory:
    short_term_limit: 10
```

2. Lower max tokens:
```yaml
core:
  ai:
    max_tokens: 512
```

3. Check Qdrant memory usage:
```bash
docker stats qdrant
```

### Vector Dimension Errors

**Error:** `Vector dimension error: expected dim: X, got Y`

This happens when changing embedding models with different vector sizes.

**Solution - Nuke and Recreate Collections:**

```bash
# Delete collections (data will be lost!)
curl -X DELETE http://localhost:6333/collections/memories
curl -X DELETE http://localhost:6333/collections/profiles

# Or with authentication:
curl -X DELETE https://your-cluster.qdrant.io:6333/collections/memories \
  -H "api-key: your-key"
```

**Why:** Qdrant collections have fixed vector dimensions. When you switch from:
- OpenAI text-embedding-3-small (1536) to MiniLM (384)
- Or any model with different output size

The existing collection expects the old dimension and rejects new vectors.

**Prevention:**
- Set correct `memory_pipeline.qdrant.vector_size` before first run
- Or always delete collections when switching embedding models
- Vector sizes by model:
  - OpenAI text-embedding-3-small: 1536
  - OpenAI text-embedding-3-large: 3072
  - MiniLM/M3E: 384-1024
  - BGE: 1024
