# OpenAI-Compatible TTS Plugin

Provides one AI tool via the plugin system:
- `generate_tts_audio`

This plugin calls an OpenAI-compatible `/audio/speech` endpoint, saves the returned audio to disk, and returns file metadata.

The plugin implements `before_send` and always requests TTS generation for every non-empty bot reply, so audio is uploaded together with the text response.

## Build

Windows:

```powershell
go build -o openai-tts-plugin.exe .
```

Linux/macOS:

```bash
go build -o openai-tts-plugin .
```

## Usage

Create a dedicated plugin folder and copy the binary there.

Windows example:

```powershell
New-Item -ItemType Directory -Force -Path plugins/openai-tts-go | Out-Null
Copy-Item openai-tts-plugin.exe plugins/openai-tts-go/openai-tts-plugin.exe
Copy-Item examples/plugins/openai-tts-go/config.yaml.example plugins/openai-tts-go/config.yaml
```

Linux/macOS example:

```bash
mkdir -p plugins/openai-tts-go
cp openai-tts-plugin plugins/openai-tts-go/
cp examples/plugins/openai-tts-go/config.yaml.example plugins/openai-tts-go/config.yaml
chmod +x plugins/openai-tts-go/openai-tts-plugin
```

The bot will load it on startup.

## Configuration

Config file location:

- `plugins/openai-tts-go/config.yaml`

Supported keys:

- `api_base_url` (string, required): OpenAI-compatible API base URL
- `api_key` (string, required): API key
- `model` (string, required): TTS model name
- `voice` (string, required): default voice
- `format` (string, required): default output format (`mp3`, `opus`, `aac`, `flac`, `wav`, `pcm`)
- `speed` (number, required): default speed in range (0, 4]
- `timeout_seconds` (integer, required): request timeout in seconds
- `output_dir` (string, required): output directory, relative to plugin directory or absolute path
- `max_text_chars` (integer, required): maximum input text length
- `upload_memory_threshold` (string, required): before_send upload threshold with unit (`B`, `KB`, `MB`, `GB`), for example `512KB` or `1MB`
- `rewriter.enabled` (bool, required): must be explicitly `true` or `false`
- `rewriter.api_base_url` (string, required when enabled): rewrite model API base URL
- `rewriter.api_key` (string, required when enabled): rewrite model API key
- `rewriter.model` (string, required when enabled): rewrite model name
- `rewriter.timeout_seconds` (integer, required when enabled): rewrite API timeout in seconds
- `rewriter.retry_count` (integer, required when enabled): rewrite retry count
- `rewriter.max_tokens` (integer, required when enabled): max output tokens for rewrite completion
- `rewriter.temperature` (number, required when enabled): rewrite temperature (0-2)
- `rewriter.prompt` (string, required when enabled): rewrite system prompt template
- `headers` (map[string]string, optional): extra request headers (cannot override `Authorization`)

This plugin does strict config validation and does not apply implicit defaults.

## Before-Send Behavior

This plugin always uses the plugin `before_send` hook (RPCWrapper.BeforeSend) to synthesize one audio file for each non-empty bot reply.

- The plugin returns in-memory audio bytes when size is <= `upload_memory_threshold`
- The plugin writes a temp file and returns path when size is > `upload_memory_threshold`
- The host prioritizes in-memory bytes upload and falls back to file path upload
- Temp files created by threshold fallback are marked for host-side cleanup after upload
- If synthesis fails, the hook returns an error and the send is aborted (intrusive mode)

## Optional AI Rewriter

When `rewriter.enabled=true`, an additional OpenAI-compatible chat model rewrites both:

- TTS `input`
- TTS `instructions`

The rewrite output must be JSON, for example:

```json
{"input":"...","instructions":"..."}
```

or (when target TTS model does not support instructions):

```json
{"input":"..."}
```

The parser is strict:

- output must contain one JSON object
- only keys `input` and `instructions` are accepted
- `input` key must exist
- `instructions` key is optional
- `input` must be non-empty after trim

Rewriter message flow:

- `rewriter.prompt` is sent as-is as the `system` message
- bot reply + instructions are sent as JSON payload in the `user` message
- plugin extracts JSON string from `assistant` content and parses it strictly
- TTS request includes `instructions` only when rewriter output contains `instructions` key

## Tool Parameters

- `text` (required): input text
- `voice` (optional): override config voice
- `model` (optional): override config model
- `format` (optional): override config format
- `speed` (optional): override config speed
- `instructions` (optional): speaking style instructions
- `file_name` (optional): output file base name
- `overwrite` (optional): overwrite existing file if same name exists

## Tool Output

Returns JSON string with fields such as:

- `status`
- `file_path`
- `relative_path`
- `bytes`
- `content_type`
- `model`
- `voice`
- `format`
- `speed`
- `text_chars`
- `endpoint`

Note: this tool generates a local audio file. It does not automatically upload audio to Discord.
