# Vision Modes

EZyapper supports three different vision modes to balance cost, speed, and functionality. Configure via `ai.vision.mode` in `config.yaml`.

## Overview

| Mode | Images | Tools | API Calls | Cost | Use Case |
|------|--------|-------|-----------|------|----------|
| `text_only` | Ignored | �?| 1 | Lowest | Budget, speed |
| `hybrid` | Described | �?| 2 | Medium | Tool-heavy workflows |
| `multimodal` | Direct | �?| 1 | Higher | Visual reasoning |

## Mode Details

### Text-Only Mode

**Configuration:**
```yaml
ai:
  vision:
    mode: "text_only"
```

**Behavior:**
- Completely ignores all images
- No API calls to vision model
- Fastest response time
- Lowest cost
- Images from short-term context are URL text only

**When to Use:**
- Budget constraints
- Speed is priority
- Visual content is not important

**Data Flow:**
```
Discord Message with Image �?Skip Image Extraction �?Text Model + Tools �?Response
```

### Hybrid Mode

**Configuration:**
```yaml
ai:
  vision:
    mode: "hybrid"
    description_prompt: "Describe this image in 1-2 sentences."
    max_images: 4
```

**Behavior:**
- Uses vision model to describe images
- Inserts descriptions into text model's context
- Two API calls (vision + main)
- Text model can use all tools (Discord/MCP)

**When to Use:**
- Need tool support with image understanding
- Budget is a concern (use cheaper text model for main AI)
- Want to preserve image context for tool execution

**Data Flow:**
```
Discord Message with Image
  �?Vision Model (gpt-4o)
  �?Image Description ("A photo of a black cat")
  �?Text Model (gpt-4o-mini) + Tools
  �?Response with tool execution
```

**Example Response:**
```
User: [Image attachment] Look at this and tell me about it
Bot: I see that's a photo of a black cat sitting on a couch. 
    Based on the channel activity, you shared it earlier today.
    [Uses get_recent_messages tool to verify]
```

### Multimodal Mode

**Configuration:**
```yaml
ai:
  vision:
    mode: "multimodal"
    max_images: 4
```

**Behavior:**
- Single model handles both vision and tools
- Direct image input (no description intermediate)
- One API call
- Tools can operate with visual context

**When to Use:**
- Visual reasoning requires tool execution
- Visual complexity is high
- Budget allows expensive multimodal model

**Data Flow:**
```
Discord Message with Image
  �?Multimodal Model (gpt-4o) with Tools
  �?Response (can execute tools with image understanding)
```

**Example Response:**
```
User: [Image of code] What's wrong with this?
Bot: The issue is missing a closing brace on line 42. 
    I can see the function starts at line 35 and the loop ends at line 40, 
    but there's no closing brace for the function itself. 
    The syntax error will prevent the code from compiling.
```

## Configuration Options

### Required Fields

| Option | Description |
|--------|-------------|
| `ai.vision.mode` | Vision mode to use (required: text_only, hybrid, or multimodal) |
| `ai.vision.max_images` | Maximum images per message (required, must be > 0) |
| `ai.vision.description_prompt` | Prompt for hybrid mode (required only when mode is "hybrid") |

### Environment Variables

| Variable | Config Path |
|----------|-------------|
| `EZYAPPER_AI_VISION_MODEL` | `ai.vision_model` |
| `EZYAPPER_AI_VISION_MODE` | `ai.vision.mode` (e.g. `text_only`) |

## Decision System Integration

The decision service (if enabled) is **image-aware** and considers images when deciding whether to respond.

**Decision Prompt Includes:**
- Number of images attached to message
- Context: Visual content with N image(s) attached

**Decision Rules Enhanced:**
- Respond when user shares relevant image
- Respond when image requires analysis or comment
- See: `internal/ai/decision.go`

## Memory System Integration

**Current Behavior:**
- Images are **automatically described** during memory consolidation
- Vision model generates text descriptions of all images
- Descriptions are included in the conversation analysis
- Memories extracted will reference image content (e.g., "User showed their pet cat")
- Only text descriptions stored - actual image URLs are not preserved

**How It Works:**
1. During consolidation, each message is processed
2. If message contains images, vision model describes them
3. Descriptions appended as: `[Image N: description]`
4. LLM analyzes full context including image descriptions
5. Extracted memories include visual context from images

## Performance Comparison

| Metric | text_only | hybrid | multimodal |
|--------|-----------|-------|------------|
| API Calls | 1 | 2 | 1 |
| Latency | Low | Medium | Medium |
| Cost per Image | $0 | vision | vision model |
| Tool Support | �?Full | �?Full | �?Full |
| Visual Fidelity | None | Low | High |

## Best Practices

### Choosing a Mode

1. **Start with `multimodal`**
   - Easiest to configure
   - Best experience for visual understanding
   - Tools work with images

2. **Switch to `hybrid` for cost savings**
   - Main text model can be cheaper (e.g., gpt-4o-mini vs gpt-4o)
   - Still get tool support
   - Accept slight latency increase

3. **Use `text_only` for budget**
   - Skip vision model entirely
   - Fastest responses
   - Best for CPU-only or cost-constrained environments

### Model Recommendations

| Mode | Vision Model | Main Model |
|------|-------------|------------|
| `text_only` | Any (not used) | Any text model |
| `hybrid` | `gpt-4o` (vision) | `gpt-4o-mini` (chat+tools) |
| `multimodal` | `gpt-4o` | `gpt-4o` (combined) |

### Common Providers

| Provider | Recommended for Mode | Reason |
|----------|-------------------|--------|
| OpenAI | All modes | Native support for both |
| DeepSeek | `text_only`, `hybrid` | Cost-effective chat, no vision model |
| Qwen | `text_only`, `hybrid` | Good Chinese AI, no vision model |
| Azure OpenAI | `multimodal` | Enterprise multimodal support |
| Local (Ollama) | `text_only` | CPU/GPU local LLMs |

## Troubleshooting

### Bot ignores images
- Check `ai.vision.mode` is not set to `text_only`
- Verify `ai.vision_model` is configured
- Check logs for vision API errors

### Slow responses with images
- Try switching to `text_only` mode
- Consider reducing `ai.vision.max_images`
- Use faster vision model if available

### Tools not seeing images
- Hybrid mode: Tools only receive descriptions (not raw images)
- Multimodal mode: Tools receive full visual context
- Log vision model usage to confirm images are being sent

### Consolidation misses image context
- By design: Memory system is text-only
- Images are only in short-term Discord context (20 messages default)
- Consider increasing `memory.short_term_limit` to keep image URLs longer
