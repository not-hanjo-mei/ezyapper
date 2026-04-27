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

```yaml
# EZyapper Configuration
# ======================

# Discord Bot Settings
discord:
  # Your Discord bot token (required)
  # Get it from: https://discord.com/developers/applications
  token: "YOUR_DISCORD_BOT_TOKEN_HERE"
  
  # Bot's display name for system prompts
  bot_name: "EZyapper"
  
  # Reply probability (0.0 - 1.0) when not mentioned
  # 0.15 = 15% chance to respond randomly
  reply_percentage: 0.15
  
  # Cooldown between responses to same user (seconds)
  cooldown_seconds: 5
  
  # Maximum responses per minute per channel
  max_responses_per_minute: 10
  
  # Blacklist/whitelist configuration moved to top-level sections
  # See 'blacklist:' and 'whitelist:' sections below

# AI/LLM Configuration
ai:
  # OpenAI-compatible API endpoint
  # Supports: OpenAI, DeepSeek, Qwen, Azure, local LLMs
  api_base_url: "https://api.openai.com/v1"
  
  # Your API key (required)
  api_key: "YOUR_API_KEY_HERE"
  
  # Model for text generation
  model: "gpt-4o-mini"
  
  # Model for vision (image analysis)
  vision_model: "gpt-4o"
  
  # Maximum tokens in response
  max_tokens: 1024
  
  # Temperature for response creativity (0.0 - 2.0)
  # Lower = more focused, Higher = more creative
  temperature: 0.8
  
  # System prompt template
  # Variables: {BotName}, {AuthorName}, {ServerName}, {GuildID}, {ChannelID}, {UserID}
  system_prompt: |
    You are {BotName}, a friendly and helpful Discord bot in the {ServerName} server.
    You're chatting with {AuthorName}.

    If the `datetime` plugin is enabled, you can use the `get_current_datetime` tool when you need current date or time.

    Guidelines:
    - Be conversational and natural, like a real person in a group chat
    - Keep responses concise (under 200 words typically)
    - Use appropriate emoji occasionally but don't overdo it
    - Match the tone and energy of the conversation
    - If asked about your capabilities, explain you can see images and use tools

    You have access to tools for Discord operations. Use them when appropriate.

  # Vision configuration
  vision:
    # Vision mode for image handling
    # - text_only: Ignore images completely (fastest, cheapest)
    # - hybrid: Vision model describes images �?text model with tools (2 API calls)
    # - multimodal: Single vision model handles images + tools directly (1 API call)
    mode: "multimodal"

    # Maximum images to process per message
    # Higher values may hit API limits and increase costs
    max_images: 4

    # Prompt for hybrid mode image descriptions
    # This prompt is used to generate text descriptions from images
    description_prompt: "Describe this image in 1-2 sentences."

# Long-term Memory Configuration
memory:
  # Trigger consolidation every N messages
  # Lower = more frequent updates, Higher = less overhead
  consolidation_interval: 50
  
  # Short-term context length (Discord messages fetched)
  # Higher = better context, but more tokens
  short_term_limit: 20

  # Retrieval configuration
  retrieval:
    # Number of memories to return per search
    top_k: 5
    
    # Minimum similarity score (0.0 - 1.0)
    # Higher = more relevant, but fewer results
    min_score: 0.75

# Qdrant Vector Database Configuration
qdrant:
  # Qdrant host
  # Use "localhost" for local development
  # Use "qdrant" for Docker Compose
  host: "localhost"

  # Qdrant gRPC port (example: 6334)
  port: 6334

  # Qdrant API key (optional, for authenticated instances)
  # Leave empty for local development without authentication
  api_key: ""

  # Vector dimension size (must match your embedding model)
  # OpenAI text-embedding-3-small: 1536
  # OpenAI text-embedding-3-large: 3072
  # Local models (e.g., LM Studio): varies (typically 768 or 1024)
  # 
  # IMPORTANT: Changing this requires deleting (nuking) existing Qdrant collections!
  # Different embedding models produce vectors of different dimensions.
  # After changing vector_size, you MUST delete existing collections:
  #   curl -X DELETE http://localhost:6333/collections/memories
  #   curl -X DELETE http://localhost:6333/collections/profiles
  # The bot will recreate them with correct dimensions on restart.
  vector_size: 1536

# Blacklist Configuration
blacklist:
  # User IDs to blacklist (bot ignores these users)
  users: []
  
  # Channel IDs to blacklist
  channels: []
  
  # Guild IDs to blacklist
  guilds: []

# WebUI Configuration
web:
  # Port for WebUI and API
  port: 8080
  
  # Basic auth credentials
  username: "admin"
  password: "changeme123"
  
  # Enable WebUI (temporary recommendation: false)
  enabled: false

# Logging Configuration
logging:
  # Log level: debug, info, warn, error
  level: "info"
  
  # Log to file (empty = stdout only)
  file: "logs/ezyapper.log"
  
  # Max log file size (MB)
  max_size: 100
  
  # Max number of old log files
  max_backups: 3
  
  # Max age of log files (days)
  max_age: 30

# Plugin Configuration
plugins:
  # Enable plugin system
  enabled: true
  
  # Directory for external plugins
  plugins_dir: "plugins"

# MCP (Model Context Protocol) Configuration
# Connect to external MCP servers to extend bot capabilities
mcp:
  # Enable MCP client
  enabled: false
  
  # List of MCP servers to connect to
  servers:
    # Example: Filesystem MCP server
    # - name: filesystem
    #   type: stdio
    #   command: npx
    #   args: ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/dir"]
    #   env:
    #     NODE_ENV: production
    
    # Example: GitHub MCP server
    # - name: github
    #   type: stdio
    #   command: npx
    #   args: ["-y", "@modelcontextprotocol/server-github"]
    #   env:
    #     GITHUB_PERSONAL_ACCESS_TOKEN: your_token_here
    
    # Example: PostgreSQL MCP server
    # - name: postgres
    #   type: stdio
    #   command: npx
    #   args: ["-y", "@modelcontextprotocol/server-postgres", "postgresql://localhost/mydb"]
    
    # Example: SSE transport
    # - name: remote-server
    #   type: sse
    #   url: https://example.com/mcp/sse
```

## Environment Variables

All settings can be overridden with environment variables using the `EZYAPPER_` prefix:

| Environment Variable | Config Path | Example |
|---------------------|-------------|---------|
| `EZYAPPER_DISCORD_TOKEN` | `discord.token` | `EZYAPPER_DISCORD_TOKEN=abc123` |
| `EZYAPPER_DISCORD_BOT_NAME` | `discord.bot_name` | `EZYAPPER_DISCORD_BOT_NAME=MyBot` |
| `EZYAPPER_DISCORD_REPLY_PERCENTAGE` | `discord.reply_percentage` | `EZYAPPER_DISCORD_REPLY_PERCENTAGE=0.2` |
| `EZYAPPER_AI_API_KEY` | `ai.api_key` | `EZYAPPER_AI_API_KEY=sk-xxx` |
| `EZYAPPER_AI_API_BASE_URL` | `ai.api_base_url` | `EZYAPPER_AI_API_BASE_URL=https://api.deepseek.com/v1` |
| `EZYAPPER_AI_MODEL` | `ai.model` | `EZYAPPER_AI_MODEL=deepseek-chat` |
| `EZYAPPER_AI_VISION_MODEL` | `ai.vision_model` | `EZYAPPER_AI_VISION_MODEL=gpt-4o` |
| `EZYAPPER_AI_MAX_TOKENS` | `ai.max_tokens` | `EZYAPPER_AI_MAX_TOKENS=1024` |
| `EZYAPPER_AI_TEMPERATURE` | `ai.temperature` | `EZYAPPER_AI_TEMPERATURE=0.7` |
| `EZYAPPER_AI_RETRY_COUNT` | `ai.retry_count` | `EZYAPPER_AI_RETRY_COUNT=3` |
| `EZYAPPER_AI_TIMEOUT` | `ai.timeout` | `EZYAPPER_AI_TIMEOUT=30` |
| `EZYAPPER_AI_VISION_MODE` | `ai.vision.mode` | `EZYAPPER_AI_VISION_MODE=hybrid` |
| `EZYAPPER_AI_VISION_MAX_IMAGES` | `ai.vision.max_images` | `EZYAPPER_AI_VISION_MAX_IMAGES=4` |
| `EZYAPPER_AI_VISION_API_BASE_URL` | `ai.vision.api_base_url` | `EZYAPPER_AI_VISION_API_BASE_URL=https://api.openai.com/v1` |
| `EZYAPPER_AI_VISION_API_KEY` | `ai.vision.api_key` | `EZYAPPER_AI_VISION_API_KEY=sk-...` |
| `EZYAPPER_AI_VISION_MAX_TOKENS` | `ai.vision.max_tokens` | `EZYAPPER_AI_VISION_MAX_TOKENS=1024` |
| `EZYAPPER_AI_VISION_TEMPERATURE` | `ai.vision.temperature` | `EZYAPPER_AI_VISION_TEMPERATURE=0.8` |
| `EZYAPPER_AI_VISION_RETRY_COUNT` | `ai.vision.retry_count` | `EZYAPPER_AI_VISION_RETRY_COUNT=3` |
| `EZYAPPER_AI_VISION_TIMEOUT` | `ai.vision.timeout` | `EZYAPPER_AI_VISION_TIMEOUT=30` |
| `EZYAPPER_MEMORY_CONSOLIDATION_INTERVAL` | `memory.consolidation_interval` | `EZYAPPER_MEMORY_CONSOLIDATION_INTERVAL=50` |
| `EZYAPPER_MEMORY_SHORT_TERM_LIMIT` | `memory.short_term_limit` | `EZYAPPER_MEMORY_SHORT_TERM_LIMIT=20` |
| `EZYAPPER_MEMORY_CONSOLIDATION_MODEL` | `memory.consolidation.model` | `EZYAPPER_MEMORY_CONSOLIDATION_MODEL=gpt-4o-mini` |
| `EZYAPPER_MEMORY_CONSOLIDATION_MAX_TOKENS` | `memory.consolidation.max_tokens` | `EZYAPPER_MEMORY_CONSOLIDATION_MAX_TOKENS=1024` |
| `EZYAPPER_MEMORY_CONSOLIDATION_RETRY_COUNT` | `memory.consolidation.retry_count` | `EZYAPPER_MEMORY_CONSOLIDATION_RETRY_COUNT=3` |
| `EZYAPPER_MEMORY_CONSOLIDATION_VISION_MODEL` | `memory.consolidation.vision_model` | `EZYAPPER_MEMORY_CONSOLIDATION_VISION_MODEL=gpt-4o-mini` |
| `EZYAPPER_QDRANT_HOST` | `qdrant.host` | `EZYAPPER_QDRANT_HOST=qdrant` |
| `EZYAPPER_QDRANT_PORT` | `qdrant.port` | `EZYAPPER_QDRANT_PORT=6334` |
| `EZYAPPER_QDRANT_API_KEY` | `qdrant.api_key` | `EZYAPPER_QDRANT_API_KEY=your_key` |
| `EZYAPPER_WEB_PORT` | `web.port` | `EZYAPPER_WEB_PORT=3000` |
| `EZYAPPER_WEB_USERNAME` | `web.username` | `EZYAPPER_WEB_USERNAME=admin` |
| `EZYAPPER_WEB_PASSWORD` | `web.password` | `EZYAPPER_WEB_PASSWORD=secret` |
| `EZYAPPER_LOGGING_LEVEL` | `logging.level` | `EZYAPPER_LOGGING_LEVEL=debug` |

## AI Provider Configuration

### OpenAI

```yaml
ai:
  api_base_url: "https://api.openai.com/v1"
  api_key: "sk-..."
  model: "gpt-4o-mini"
  vision_model: "gpt-4o"
```

### DeepSeek

```yaml
ai:
  api_base_url: "https://api.deepseek.com/v1"
  api_key: "sk-..."
  model: "deepseek-chat"
  vision_model: "deepseek-chat"
```

### Qwen (Alibaba Cloud)

```yaml
ai:
  api_base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
  api_key: "sk-..."
  model: "qwen-plus"
  vision_model: "qwen-vl-plus"
```

### Azure OpenAI

```yaml
ai:
  api_base_url: "https://YOUR_RESOURCE.openai.azure.com"
  api_key: "your-azure-key"
  model: "gpt-4o-mini"
```

### Local LLM (LM Studio, Ollama)

```yaml
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
| `text_only` | Ignored | �?| 1 | Lowest | Budget constraints, text-only chats |
| `hybrid` | Described �?Text | �?| 2 | Medium | Tool-heavy workflows, budget balance |
| `multimodal` | Direct | �?| 1 | Higher | Visual reasoning, image-heavy chats |

### How Each Mode Works

#### text_only (Fastest, Cheapest)
- **Behavior**: Ignores all images in messages
- **Flow**: Message text �?�?Text model + Tools �?Response
- **API Calls**: 1 (text model with tools)
- **When to use**: Budget constraints, low-latency requirement, text-only discussions

```yaml
ai:
  vision:
    mode: "text_only"
```

#### hybrid (2 API Calls)
- **Behavior**: Vision model generates text description of images, then text model processes with tools
- **Flow**: Message + Images �?Vision description �?Text model + Tools �?Response
- **API Calls**: 2 (vision model + text model)
- **When to use**: Need tools with images, want better tool support than pure vision models

```yaml
ai:
  vision:
    mode: "hybrid"
    description_prompt: "Describe this image in detail for context."
    max_images: 4
```

#### multimodal (Direct Vision + Tools)
- **Behavior**: Single vision model handles both images and tools directly
- **Flow**: Message + Images �?Vision model with tools �?Response
- **API Calls**: 1 (multimodal model)
- **When to use**: Best visual reasoning, image-heavy workflows, have GPT-4V/Claude-vision

```yaml
ai:
  vision:
    mode: "multimodal"
    max_images: 4
```

### Vision Configuration Options

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| `ai.vision.mode` | string | �?Yes | Vision mode: `text_only`, `hybrid`, `multimodal` |
| `ai.vision.max_images` | int | �?Yes | Maximum images to process per message (must be > 0) |
| `ai.vision.description_prompt` | string | �?Yes* | Prompt for hybrid mode descriptions (required only when mode = "hybrid") |

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
| `{UserID}` | Discord user ID | "111222333444555666" |

**Note:** For current date/time, use the `get_current_datetime` tool (provided by the `datetime` plugin) instead of a variable. This preserves prompt caching.

## Channel Configuration

### Blacklist vs Whitelist

**Blacklist Mode:**
- Bot responds in all channels except blacklisted ones
- Set `blacklist.channels` with channel IDs to ignore
- This is the default behavior when no whitelist is configured

**Whitelist Mode:**
- Bot only responds in whitelisted channels
- Set `whitelist.channels` with allowed channel IDs

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

**System Prompt** (`decision.system_prompt`):
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
1. Bot uses `discord.reply_percentage` to randomly decide
2. Example: `0.15` = 15% chance to respond on fallback
3. Logged as `llm decision failed, fallback`

This ensures the bot remains responsive even when the decision service encounters problems.

## Memory Settings

### Image Descriptions in Memory

During memory consolidation, images are automatically described using the vision model:

```yaml
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
memory:
  consolidation:
    vision_model: "gpt-4o-mini"  # Use cheaper model for consolidation
```

**Use cases:**
- Use expensive high-quality model (gpt-4o) for real-time chat
- Use cheaper model (gpt-4o-mini) for background consolidation
- Reduces costs while maintaining good memory quality

**Note:** If not specified, defaults to `ai.vision_model`

### Separate Endpoints and Parameters

You can configure separate API endpoints and parameters for every LLM component:

```yaml
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
1. If component-specific value is set �?use it
2. Else if parent config has value �?use it  
3. Else use main AI config value

Example: `memory.consolidation.vision_max_tokens` �?`ai.vision.max_tokens` �?`ai.max_tokens`

### Consolidation Interval

Controls how often memories are processed:

```yaml
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
qdrant:
  host: "localhost"
  port: 6334
```

### Docker Compose

```yaml
qdrant:
  host: "qdrant"  # Service name in docker-compose.yml
  port: 6334
```

### Remote Qdrant

```yaml
qdrant:
  host: "your-qdrant-instance.example.com"
  port: 6334
```

### Qdrant with Authentication

For Qdrant Cloud or authenticated instances:

```yaml
qdrant:
  host: "your-cluster.qdrant.io"
  port: 6334
  api_key: "your-qdrant-api-key"
```

Or via environment variable:
```bash
export EZYAPPER_QDRANT_API_KEY="your-qdrant-api-key"
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
   web:
     password: "your-secure-password"
   ```

2. **Use environment variables for secrets:**
   ```bash
   export EZYAPPER_DISCORD_TOKEN="your-token"
   export EZYAPPER_AI_API_KEY="your-key"
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
Always set `qdrant.vector_size` to match your embedding model BEFORE first run, or delete collections when switching models.
