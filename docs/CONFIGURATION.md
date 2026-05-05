# Configuration Guide

This guide covers all configuration options for EZyapper.

## Configuration File

The primary configuration is in `config.yaml`. You can specify a custom config path with the `-config` flag:

```bash
./ezyapper -config /path/to/config.yaml
```

> [!CAUTION]
> **STRICT CONFIGURATION REQUIRED**
> EZyapper does **NOT** provide default values for configuration fields. Every field in the `config.yaml` example must be explicitly set, or the bot will refuse to start. The startup validation will list ALL missing fields at once.

> [!IMPORTANT]
> **NO HOT-RELOAD**
> Changes to the configuration file require a full bot restart to take effect. WebUI updates are persisted to `config.yaml`, but a restart is still required for settings that cannot be applied at runtime.

> [!WARNING]
> **Temporary WebUI Recommendation**
> The WebUI dashboard currently has known stability issues. Keep it disabled for normal operation and only enable it when you need to troubleshoot dashboard/API behavior.
>
> In `schema_version: 3` configs, this is `operations.web.enabled: false`.

## Full Configuration Reference

The v3 config is grouped. For the full reference, see examples/config.yaml.example and examples/config.schema.json.

Top-level shape:

```yaml
schema_version: 3

core:
  discord: {}
  ai: {}
  decision: {}

memory_pipeline:
  embedding: {}
  memory: {}
  qdrant: {}

access_control:
  blacklist: {}
  whitelist: {}

operations:
  web: {}
  logging: {}
  plugins: {}
  mcp: {}
  runtime: {}
```

## Environment Variables

All settings can be overridden with environment variables using the `EZYAPPER_` prefix. Convert the config path to uppercase and replace dots with underscores.

Examples:
- `core.ai.api_key` -> `EZYAPPER_CORE_AI_API_KEY`
- `memory_pipeline.memory.short_term_limit` -> `EZYAPPER_MEMORY_PIPELINE_MEMORY_SHORT_TERM_LIMIT`
- `operations.web.enabled` -> `EZYAPPER_OPERATIONS_WEB_ENABLED`

## AI Provider Configuration

### OpenAI

```yaml
core:
  ai:
    api_base_url: "https://api.openai.com/v1"
    api_key: "sk-..."
    model: "gpt-4o-mini"
    vision_model: "gpt-4o"
```

### DeepSeek

```yaml
core:
  ai:
    api_base_url: "https://api.deepseek.com/v1"
    api_key: "sk-..."
    model: "deepseek-chat"
    vision_model: "deepseek-chat"
```

### Qwen (Alibaba Cloud)

```yaml
core:
  ai:
    api_base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
    api_key: "sk-..."
    model: "qwen-plus"
    vision_model: "qwen-vl-plus"
```

### Azure OpenAI

```yaml
core:
  ai:
    api_base_url: "https://YOUR_RESOURCE.openai.azure.com"
    api_key: "your-azure-key"
    model: "gpt-4o-mini"
```

### Local LLM (LM Studio, Ollama)

```yaml
core:
  ai:
    api_base_url: "http://localhost:1234/v1"
    api_key: "not-needed"
    model: "local-model"
```

## Vision Modes

EZyapper supports three vision modes for handling images in Discord messages:

### Mode Comparison

| Mode | Images | Tools | API Calls | Cost | Best For |
|------|--------|-------|-----------|------|----------|
| `text_only` | Ignored | Yes | 1 | Lowest | Budget constraints, text-only chats |
| `hybrid` | Described -> Text | Yes | 2 | Medium | Tool-heavy workflows, budget balance |
| `multimodal` | Direct | Yes | 1 | Higher | Visual reasoning, image-heavy chats |

### How Each Mode Works

#### text_only (Fastest, Cheapest)
- **Behavior**: Ignores all images in messages
- **Flow**: Message text -> Text model + tools -> Response
- **API Calls**: 1 (text model with tools)
- **When to use**: Budget constraints, low-latency requirement, text-only discussions

```yaml
core:
  ai:
    vision:
      mode: "text_only"
```

#### hybrid (2 API Calls)
- **Behavior**: Vision model generates text description of images, then text model processes with tools
- **Flow**: Message + images -> Vision description -> Text model + tools -> Response
- **API Calls**: 2 (vision model + text model)
- **When to use**: Need tools with images, want better tool support than pure vision models

```yaml
core:
  ai:
    vision:
      mode: "hybrid"
      description_prompt: "Describe this image in detail for context."
      max_images: 4
```

#### multimodal (Direct Vision + Tools)
- **Behavior**: Single vision model handles both images and tools directly
- **Flow**: Message + images -> Vision model with tools -> Response
- **API Calls**: 1 (multimodal model)
- **When to use**: Best visual reasoning, image-heavy workflows, have GPT-4V/Claude-vision

```yaml
core:
  ai:
    vision:
      mode: "multimodal"
      max_images: 4
```

### Vision Configuration Options

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| `core.ai.vision.mode` | string | Yes | Vision mode: `text_only`, `hybrid`, `multimodal` |
| `core.ai.vision.max_images` | int | Yes | Maximum images to process per message (must be > 0) |
| `core.ai.vision.description_prompt` | string | Yes* | Prompt for hybrid mode descriptions (required only when mode = "hybrid") |

> [!NOTE]
> **NO DEFAULTS**: All vision configuration options are required. The bot will exit on startup with a validation error if any required value is missing.

### Important Notes

- **Memory is text-only**: Images are never stored in long-term memory, only their text descriptions (in hybrid mode)
- **Image-aware decisions**: The bot's reply decision system considers whether images are attached when deciding whether to respond
- **Model compatibility**: Ensure your `vision_model` supports your chosen mode:
  - `text_only`: Only need `model` (text)
  - `hybrid`: Need both `vision_model` (for description) and `model` (for text + tools)
  - `multimodal`: Need `vision_model` that supports tool calling alongside vision

## System Prompt Variables

The system prompt supports dynamic variables:

| Variable | Description | Example Output |
|----------|-------------|----------------|
| `{BotName}` | Bot's display name | "EZyapper" |
| `{AuthorName}` | Message author's name | "JohnDoe" |
| `{ServerName}` | Discord server name | "My Server" |
| `{GuildID}` | Discord guild ID | "123456789012345678" |
| `{ChannelID}` | Discord channel ID | "876543210987654321" |

**Note:** For current date/time, use the `get_current_datetime` tool (provided by the `datetime` plugin) instead of a variable. This preserves prompt caching.

## Channel Configuration

### Blacklist vs Whitelist

**Blacklist Mode:**
- Bot responds in all channels except blacklisted ones
- Set `access_control.blacklist.channels` with channel IDs to ignore
- This is the default behavior when no whitelist is configured

**Whitelist Mode:**
- Bot only responds in whitelisted channels
- Set `access_control.whitelist.channels` with allowed channel IDs

## Decision Configuration

The decision system uses an LLM to intelligently decide whether the bot should respond to messages that don't mention it. This provides more nuanced behavior than simple probability-based replies.

### Configuration Options

| Option | Description | Required |
|--------|-------------|----------|
| `enabled` | Enable LLM-based reply decision | No (default: false) |
| `model` | Model to use for decisions | Yes (if enabled) |
| `api_base_url` | API endpoint for decision requests | Yes (if enabled) |
| `api_key` | API key for decision requests | Yes (if enabled) |
| `max_tokens` | Max tokens for decision response | Yes (if enabled) |
| `temperature` | Response randomness (0.0-2.0) | No |
| `retry_count` | API retry attempts | No |
| `timeout` | Request timeout (seconds) | Yes (if enabled) |
| `system_prompt` | System prompt with role and rules | Yes (if enabled) |

### Decision Prompt Structure

The decision system separates **system prompt** and **user prompt** following LLM best practices:

**System Prompt** (`core.decision.system_prompt`):
- Defines the role (decision classifier)
- Lists rules for responding vs not responding
- Specifies output format (JSON with `should_respond`, `reason`, `confidence`)
- Template variable: `{BotName}`

**User Prompt** (auto-generated):
- Message content to analyze
- Attachment info (if images present)
- Recent conversation context
- No template variables - built dynamically

This separation improves model performance by keeping instructions in system messages and dynamic data in user messages.

### Example Configuration

```yaml
core:
  decision:
    enabled: true
    model: "gpt-4o-mini"  # Fast model recommended
    api_base_url: "https://api.openai.com/v1"
    api_key: "YOUR_DECISION_API_KEY"
    max_tokens: 4096
    temperature: 0.8
    retry_count: 5
    timeout: 30
    system_prompt: |
      You are a decision classifier for a Discord bot named "{BotName}".
      Your job is to decide if the bot should respond to the latest message.

    RULES FOR RESPONDING (should_respond: true):
    1. Bot is directly mentioned (@{BotName} or by name)
    2. Message is a reply to the bot
    3. Message is a question that could benefit from bot's knowledge
    4. Message discusses a topic the bot has expertise in
    5. User seems to want engagement (asking for opinions, help, or conversation)
    6. Message is in response to bot's previous message
    7. User shared an image that the bot should analyze or comment on
    8. Image contains content relevant to ongoing conversation

    RULES FOR NOT RESPONDING (should_respond: false):
    1. Casual conversation between other users (bot not involved)
    2. Message is a simple acknowledgment ("ok", "yeah", "lol")
    3. Message is a command or discussion for another bot
    4. Message is too short or unclear to warrant response
    5. Topic doesn't need bot's input

      Respond ONLY with valid JSON in this exact format:
      {"should_respond": true/false, "reason": "brief explanation", "confidence": 0.0-1.0}
```

### Response Format

The LLM must return JSON in this exact format:

```json
{
  "should_respond": true,
  "reason": "User asked a specific question that requires bot expertise",
  "confidence": 0.95
}
```

- `should_respond`: Boolean indicating whether to respond
- `reason`: Brief explanation of the decision
- `confidence`: Float between 0.0 and 1.0 indicating certainty

### Image-Aware Decisions

When images are attached to a message, the decision system automatically includes:

1. Image count in user message
2. Context like: `[User attached 2 image(s) to this message]`

This allows the decision rules to consider visual content when deciding whether to respond.

### Fallback Behavior

If the decision LLM fails (timeout, error, or invalid response):
1. Bot uses `core.discord.reply_percentage` to randomly decide
2. Example: `0.15` = 15% chance to respond on fallback
3. Logged as `llm decision failed, fallback`

This ensures the bot remains responsive even when the decision service encounters problems.

## Memory Settings

### Image Descriptions in Memory

During memory consolidation, images are automatically described using the vision model:

```yaml
core:
  ai:
    vision_model: "gpt-4o"  # Used to describe images during consolidation
    vision_base64: false    # Set to true if your API requires base64 images
    vision:
      description_prompt: "Describe this image in 1-2 sentences."
```

**How it works:**
1. When consolidating messages, any attached images are processed
2. Vision model generates text descriptions
3. Descriptions are included in the conversation context
4. LLM extracts memories that may reference image content

**Example:**
If a user sends a photo of their cat, the memory might be: "User has an orange tabby cat"

### Separate Vision Model for Consolidation

You can use a different (often cheaper) vision model for image description during consolidation:

```yaml
memory_pipeline:
  memory:
    consolidation:
      vision_model: "gpt-4o-mini"  # Use cheaper model for consolidation
```

**Use cases:**
- Use expensive high-quality model (gpt-4o) for real-time chat
- Use cheaper model (gpt-4o-mini) for background consolidation
- Reduces costs while maintaining good memory quality

**Note:** If not specified, defaults to `core.ai.vision_model`

### Separate Endpoints and Parameters

You can configure separate API endpoints and parameters for every LLM component:

```yaml
core:
  ai:
    # Main chat configuration
    api_base_url: "https://api.openai.com/v1"
    api_key: "sk-..."
    model: "gpt-4o"
    max_tokens: 1024
    temperature: 0.8
    retry_count: 3
    timeout: 30

    # Vision-specific endpoint and parameters
    vision:
      mode: "multimodal"
      api_base_url: "https://api.openai.com/v1"  # Optional: separate vision endpoint
      api_key: "sk-..."                           # Optional: separate vision API key
      max_tokens: 2048                            # Optional: higher for vision
      temperature: 0.5                            # Optional: different for vision
      retry_count: 5                              # Optional: more retries for vision
      timeout: 60                                 # Optional: longer timeout for vision

# Embedding with separate endpoint
memory_pipeline:
  embedding:
    api_base_url: "https://api.openai.com/v1"  # Optional
    api_key: "sk-..."                           # Optional
    model: "text-embedding-3-small"
    retry_count: 3
    timeout: 30

  # Memory consolidation with full customization
  memory:
    consolidation:
      # Text model for consolidation analysis
      model: "gpt-4o-mini"              # Cheaper model for consolidation
      api_base_url: "https://api.openai.com/v1"
      api_key: "sk-..."
      max_tokens: 1024
      temperature: 0.8
      retry_count: 3
      timeout: 30

      # Vision model for image descriptions
      vision_model: "gpt-4o-mini"       # Cheaper vision model
      vision_api_base_url: "https://api.openai.com/v1"
      vision_api_key: "sk-..."
      vision_max_tokens: 1024
      vision_temperature: 0.8
      vision_retry_count: 3
      vision_timeout: 30
```

**Fallback Chain:**
1. If component-specific value is set -> use it
2. Else if parent config has value -> use it
3. Else use main AI config value

Example: `memory_pipeline.memory.consolidation.vision_max_tokens` -> `core.ai.vision.max_tokens` -> `core.ai.max_tokens`

### Consolidation Interval

Controls how often memories are processed:

```yaml
memory_pipeline:
  memory:
    consolidation_interval: 50  # Process every 50 messages
```

**Recommendations:**
- Active users: 30-50 messages
- Normal users: 50-100 messages
- Low activity: 100-200 messages

### Short-term Limit

Controls how many recent Discord messages are fetched:

```yaml
memory_pipeline:
  memory:
    short_term_limit: 20  # Fetch last 20 messages
```

**Recommendations:**
- Fast-paced chats: 20-30 messages
- Normal chats: 15-25 messages
- Slow chats: 10-20 messages

### Retrieval Settings

Controls memory search behavior:

```yaml
memory_pipeline:
  memory:
    retrieval:
      top_k: 5        # Return top 5 memories
      min_score: 0.75 # Minimum 75% similarity
```

**Tuning:**
- Increase `top_k` for more context (uses more tokens)
- Decrease `min_score` for more results (may be less relevant)

## Qdrant Configuration

### Local Development

```yaml
memory_pipeline:
  qdrant:
    host: "localhost"
    port: 6334
```

### Docker Compose

```yaml
memory_pipeline:
  qdrant:
    host: "qdrant"  # Service name in docker-compose.yml
    port: 6334
```

### Remote Qdrant

```yaml
memory_pipeline:
  qdrant:
    host: "your-qdrant-instance.example.com"
    port: 6334
```

### Qdrant with Authentication

For Qdrant Cloud or authenticated instances:

```yaml
memory_pipeline:
  qdrant:
    host: "your-cluster.qdrant.io"
    port: 6334
    api_key: "your-qdrant-api-key"
```

Or via environment variable:
```bash
export EZYAPPER_MEMORY_PIPELINE_QDRANT_API_KEY="your-qdrant-api-key"
```

## Logging Levels

| Level | Description |
|-------|-------------|
| `debug` | Detailed debugging information |
| `info` | General operational messages |
| `warn` | Warning conditions |
| `error` | Error conditions |

## Security Best Practices

1. **Change the example password:**
   ```yaml
   operations:
     web:
       password: "your-secure-password"
   ```

2. **Use environment variables for secrets:**
   ```bash
  export EZYAPPER_CORE_DISCORD_TOKEN="your-token"
  export EZYAPPER_CORE_AI_API_KEY="your-key"
   ```

3. **Restrict WebUI access:**
   - Use a reverse proxy with additional authentication
   - Bind to localhost only if external access not needed

4. **Rotate credentials regularly**

5. **Use read-only config file:**
   ```bash
   chmod 400 config.yaml
   ```

6. **Configure blacklist appropriately:**
   ```yaml
   access_control:
     blacklist:
       users:
         - "user_id_to_ignore"
       channels:
         - "channel_id_to_ignore"
       guilds:
         - "guild_id_to_ignore"
   ```

## Troubleshooting

### Vector Dimension Errors

**Error:** `Vector dimension error: expected dim: X, got Y`

This happens when you change your embedding model to one with different dimensions.

**Solution:** You must delete existing Qdrant collections before changing embedding models:

```bash
# Delete existing collections
curl -X DELETE http://localhost:6333/collections/memories
curl -X DELETE http://localhost:6333/collections/profiles

# For authenticated Qdrant:
curl -X DELETE https://your-cluster.qdrant.io:6333/collections/memories \
  -H "api-key: your-api-key"
curl -X DELETE https://your-cluster.qdrant.io:6333/collections/profiles \
  -H "api-key: your-api-key"
```

Then restart the bot - it will recreate collections with correct dimensions.

**Why this happens:**
- Different embedding models produce vectors of different sizes
- OpenAI text-embedding-3-small: 1536 dimensions
- OpenAI text-embedding-3-large: 3072 dimensions  
- Local models (MiniLM, etc.): 384-1024 dimensions
- Qdrant collections are created with fixed vector size
- Once created, the size cannot be changed

**Prevention:**
Always set `memory_pipeline.qdrant.vector_size` to match your embedding model BEFORE first run, or delete collections when switching models.
