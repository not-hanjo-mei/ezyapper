# Quick Start

This guide gets EZyapper running with minimal setup. For full options and advanced settings, see the rest of the docs.

> [!IMPORTANT]
> **Temporary WebUI Status**
> The WebUI dashboard is currently known to be unstable. For normal bot operation, keep `operations.web.enabled: false` in `config.yaml` and enable it only when you explicitly need dashboard/API debugging.

## Prerequisites

- Go 1.24+ (for local build)
- Discord bot token
- OpenAI-compatible API key
- Qdrant vector database (included in Docker Compose)

## Option A: Docker Compose (Recommended)

1. Create an env file:

```bash
# macOS/Linux
cp examples/.env.example .env

# Windows PowerShell
Copy-Item examples/.env.example .env
```

2. Edit `.env` and set at least:

```env
EZYAPPER_DISCORD_TOKEN=your_discord_token
EZYAPPER_AI_API_KEY=your_api_key
EZYAPPER_AI_API_BASE_URL=https://api.openai.com/v1
EZYAPPER_QDRANT_HOST=qdrant
EZYAPPER_WEB_PASSWORD=change_me
```

3. Start services:

```bash
docker-compose up -d
```

4. View logs:

```bash
docker-compose logs -f
```

## Option B: Local Build

1. Copy the config template:

```bash
# macOS/Linux
cp examples/config.yaml.example config.yaml

# Windows PowerShell
Copy-Item examples/config.yaml.example config.yaml
```

2. Edit `config.yaml` and fill required fields.

EZyapper uses strict config validation. Missing required fields will stop startup and list all validation errors.

3. Build and run:

```bash
go mod download

# macOS/Linux
go build -o ezyapper ./cmd/bot
./ezyapper -config config.yaml

# Windows PowerShell
go build -o ezyapper.exe ./cmd/bot
.\ezyapper.exe -config config.yaml
```

## Verify Startup

- Bot appears online in Discord
- No configuration validation errors in logs
- Health endpoint responds (if WebUI enabled for debugging): `http://localhost:8080/health`

## Next Steps

- [Configuration Guide](CONFIGURATION.md)
- [Deployment Guide](DEPLOYMENT.md)
- [Vision Modes](VISION.md)
- [Plugin System](PLUGINS.md)
- [API Reference](API.md)