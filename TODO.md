# TODO

## MCP

- [ ] Known issue: MCP works, but related runtime logs are not consistently visible.
- [ ] Add startup logs for MCP connect status per server (success/failure + reason).
- [ ] Add summary logs after MCP tool registration (total tools and source server).
- [ ] Add per-call debug logs for MCP tool execution (tool name, latency, result/error).
- [ ] Add a verification checklist/test to confirm logs appear when MCP is enabled.

## Message Reading

- [ ] Add embedded message reading support. (Known as Embed[x] in log)

## Multimodal Support

- [ ] Remove legacy VisionMode (text_only/hybrid/multimodal).
- [ ] Refactor to per-modality model config with individual on/off switches:
  - `text`: always enabled, base model for reasoning/response
  - `image`: optional, describes images via dedicated model
  - `audio`: optional, transcribes audio via dedicated model
  - `video`: optional, describes video via dedicated model
- [ ] Add audio modality support (2 modes):
  - `asr`: transcribe via ASR API (e.g., OpenAI Whisper), feed text to base model
  - `modality`: send audio directly to multimodal LLM
- [ ] Add video modality support.
