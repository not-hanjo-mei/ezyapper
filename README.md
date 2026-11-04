# EZyapper

> *A Discord group chatbot that sees, remembers, and may occasionally roasts you.*

EZyapper is a group chat companion that can understand images, recall past conversations, and call tools to get things done. Whether you want AI shenanigans or just a bot that feels somewhat human, it's got you covered.

---

## What It Does

| Feature | Description |
|---------|-------------|
| **Multi-provider AI** | Works with OpenAI, DeepSeek, Qwen, Azure, or local endpoints |
| **Media modalities** | Images, audio, video and stickers are turned into text (descriptions, transcripts) by dedicated models; the reply model stays text-only |
| **Long-term memory** | Qdrant vector storage for conversation history, user preferences, and facts |
| **Extensible tools** | Discord tools + MCP servers + custom plugins |
| **Web dashboard** | Manage config, memories, and plugins from a browser (a bit rough around the edges) |

---

## Quick Start

See [Quick Start](docs/QUICK_START.md) for the full walkthrough.

**Precompiled binaries** are available from [GitHub Actions Artifacts](https://github.com/not-hanjo-mei/ezyapper/actions) — download the latest build for your platform if you don't want to compile from source.

```bash
# Or build it yourself
go build -o ezyapper ./cmd/bot

# Run it (edit config.yaml first!)
./ezyapper --config config.yaml
```

---

## Documentation

- [Configuration](docs/CONFIGURATION.md) — Tweak your bot's personality and behavior
- [Deployment](docs/DEPLOYMENT.md) — Get it running on a server
- [Architecture](docs/ARCHITECTURE.md) — How the code is organized
- [Media Modalities](docs/MEDIA.md) — Image, audio, video and sticker handling (describe / ASR)
- [Plugins](docs/PLUGINS.md) — Build your own extensions
- [Prompt Optimization](docs/PROMPT_OPTIMIZATION.md) — Make responses faster and cheaper
- [WebUI Reference](docs/API.md) — The optional admin dashboard

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

- **Strict config, no built-in defaults**: the bot never fills in a missing value. Start from `examples/config.yaml.example`, whose values are suggestions. A missing required field stops startup, with every error listed at once; keys that a feature needs (a media modality, the decision provider, ...) must be set when you enable it. Absent optional sections are off.
- **Needs Qdrant**: For memory features, you'll need a Qdrant instance running.
- **Hot reload**: With `operations.runtime.hot_reload.enabled: true`, saved config changes are applied without a restart, or rejected as a whole if they don't validate or can't be applied. See [Hot reload](docs/CONFIGURATION.md#hot-reload).
- **WebUI is experimental**: For stable production runs, consider disabling it with `operations.web.enabled: false`.
- **MCP logging gaps**: MCP tools can work even when logs look suspicious. Trust but verify.

---

## License

GPL-3.0 — Use it, modify it, share it. Just keep it open source.
Check [LICENSE](LICENSE) for details.

Inspired by [Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot).