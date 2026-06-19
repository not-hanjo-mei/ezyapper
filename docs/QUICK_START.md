# Quick Start

The fastest path to a running EZyapper. For full configuration details and tuning, see [CONFIGURATION.md](CONFIGURATION.md).

> [!IMPORTANT]
> **WebUI is experimental.** For normal operation keep `operations.web.enabled: false` in `config.yaml` and only enable it when you specifically need the dashboard.

## Prerequisites

- **Go 1.25+** (only if building from source)
- **Discord bot token** — [Discord Developer Portal](https://discord.com/developers/applications)
- **OpenAI-compatible API key** (or a local endpoint like Ollama)
- **Qdrant vector database** — bundled in the Docker Compose setup

---

## Option A: Docker Compose (Recommended)

1. Create an env file:

    ```bash
    # macOS/Linux
    cp examples/.env.example .env

    # Windows PowerShell
    Copy-Item examples/.env.example .env
    ```

2. Edit `.env` and set the bare minimum:

    ```env
    EZYAPPER_CORE_DISCORD_TOKEN=your_discord_token
    EZYAPPER_CORE_AI_API_KEY=your_api_key
    EZYAPPER_CORE_AI_API_BASE_URL=https://api.openai.com/v1
    EZYAPPER_MEMORY_PIPELINE_QDRANT_HOST=qdrant
    EZYAPPER_OPERATIONS_WEB_PASSWORD=replace_me
    ```

    > Env-var keys mirror the YAML path with dots replaced by underscores. See [CONFIGURATION.md](CONFIGURATION.md#environment-variable-overrides).

3. Drop in a `config.yaml`:

    ```bash
    cp examples/config.yaml.example config.yaml
    ```

    The compose file mounts this read-only into the container. Your `.env` overrides anything in the file.

4. Start it:

    ```bash
    docker compose up -d
    docker compose logs -f
    ```

---

## Option B: Local Build

1. Copy the config template:

    ```bash
    # macOS/Linux
    cp examples/config.yaml.example config.yaml

    # Windows PowerShell
    Copy-Item examples/config.yaml.example config.yaml
    ```

2. Edit `config.yaml` and fill in the required fields. EZyapper's config is strict — missing fields stop startup, and you'll see *all* errors at once.

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

If you don't want to compile, grab a prebuilt binary from [GitHub Actions Artifacts](https://github.com/not-hanjo-mei/ezyapper/actions).

---

## Verify

- Bot shows up online in Discord.
- No validation errors in the logs.
- If the WebUI is enabled: `curl http://localhost:8080/health` returns `{"status":"ok",...}`.

If validation fails, the log will list every missing/invalid field. Fix them, restart.

---

## Next Steps

- [Configuration](CONFIGURATION.md) — every field, with defaults policy and tuning notes
- [Architecture](ARCHITECTURE.md) — how the pieces fit together
- [Vision Modes](VISION.md) — text_only / hybrid / multimodal
- [Plugins](PLUGINS.md) — write or install extensions
- [WebUI Reference](API.md) — what the dashboard exposes
- [Deployment](DEPLOYMENT.md) — Docker, systemd, Kubernetes, reverse proxy
