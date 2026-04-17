# EZyapper

> *A Discord group chatbot that sees, remembers, and may occasionally roasts you.*

EZyapper is a group chat companion that can understand images, recall past conversations, and call tools to get things done. Whether you want AI shenanigans or just a bot that feels somewhat human, it's got you covered.

---

## What It Does

| Feature | Description |
|---------|-------------|
| **Multi-provider AI** | Works with OpenAI, DeepSeek, Qwen, Azure, or local endpoints |
| **Vision modes** | Three ways to handle images: ignore them (`text_only`), describe them (`hybrid`), or feed them raw to a multimodal model |
| **Long-term memory** | Qdrant vector storage for conversation history, user preferences, and facts |
| **Extensible tools** | Discord tools + MCP servers + custom plugins |
| **Web dashboard** | Manage config, memories, and plugins from a browser (a bit rough around the edges) |

---

## Quick Start

See [Quick Start](docs/QUICK_START.md) for the full walkthrough.

```bash
# Build it
go build -o ezyapper ./cmd/bot

# Run it (edit config.yaml first!)
./ezyapper -config config.yaml
```

---

## Documentation

- [Configuration](docs/CONFIGURATION.md) — Tweak your bot's personality and behavior
- [Deployment](docs/DEPLOYMENT.md) — Get it running on a server
- [Architecture](docs/ARCHITECTURE.md) — How the code is organized
- [Vision Modes](docs/VISION.md) — The difference between the three image-handling modes
- [Plugins](docs/PLUGINS.md) — Build your own extensions
- [Prompt Optimization](docs/PROMPT_OPTIMIZATION.md) — Make responses faster and cheaper
- [API Reference](docs/API.md) — Endpoints for the WebUI

---

## Development

```bash
# Run tests
go test ./...

# Format code (it's the Go way)
gofmt -w .

# Cross-compile if you need it
make build-all
```

---

## Heads Up

- **Strict config**: Every field is required. No defaults, no shortcuts. Missing something? It won't start.
- **Needs Qdrant**: For memory features, you'll need a Qdrant instance running.
- **Restart to reload**: Config changes require a restart. No hot reload here.
- **WebUI is experimental**: For stable production runs, consider disabling it with `operations.web.enabled: false`.
- **MCP logging gaps**: MCP tools can work even when logs look suspicious. Trust but verify.

---

## License

GPL-3.0 — Use it, modify it, share it. Just keep it open source.
Check [LICENSE](LICENSE) for details.

Inspired by [Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot).