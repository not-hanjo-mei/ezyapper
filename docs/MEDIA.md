# Media Modalities (images, audio, video, stickers)

Text is always handled by the base model (`core.ai`), which is treated as text-only: the reply request never carries media. Images, audio, video and stickers are the four modalities configured under `core.ai.modalities`, each with its own on/off switch and its own model (for stickers the switch only decides whether the image is read; they are always shown as text otherwise, so the sticker item cap and cache keys are always required). There are no built-in defaults: an enabled modality must set its prompt, limits and transport; `examples/config.yaml.example` carries suggested values. Field reference: [CONFIGURATION.md](CONFIGURATION.md#coreaimodalities).

**Model settings inherit by presence.** Each modality's model fields (`timeout`, `max_tokens`, `temperature`, `retry_count`, `extra_params`, and the endpoint strings) follow one rule: **absent → inherits `core.ai` (stickers: `modalities.image`, then `core.ai`; consolidation image descriptions: `consolidation.vision`, then `modalities.image`, then `core.ai`); an explicit value, including `0`, is used as is.** So `image.temperature: 0` sends temperature 0 even when `core.ai.temperature` is 0.8, `retry_count: 0` means no retries, and `extra_params: {}` sends no extra parameters. An explicit `timeout: 0` or `max_tokens: 0` is rejected at startup (remove the key to inherit). See [Inheritance between keys](CONFIGURATION.md#inheritance-between-keys).

## Pipeline ("fake multimodal")

The bot is multimodal from the user's point of view, but no model ever gets media and text together in the reply request:

1. **Media to text.** For each attachment or sticker, a dedicated model turns the media into text: a vision model describes images and stickers, an ASR endpoint transcribes audio (`asr`) or an audio-capable chat model transcribes or describes it (`describe`), and a video-capable model describes videos.
2. **Text into the prompt.** The descriptions and transcripts are appended to the message's text as lines such as `[Image 1: ...]`, `[Audio 1 transcript (voice message, 4s): ...]` or `[Video 1 (clip.mp4): ...]`.
3. **Text-only reply.** The chat model (`core.ai.model`) receives that text in the user prompt, with no image, audio or video parts, and writes the reply.

The same text is reused for the memory search query of the current message and for memory consolidation, so a voice message is searchable and memorable through its transcript.

## Modes

| Modality | Mode | What happens | Extra model calls |
|----------|------|--------------|-------------------|
| image | (always describe) | A vision model describes each image; `[Image N: ...]` is added to the message. | 1 per image (cached) |
| audio | `asr` | Speech is transcribed by an `/audio/transcriptions` endpoint; `[Audio N transcript (...): ...]` is added. | 1 per audio (cached) |
| audio | `describe` | An audio-capable chat model gets the audio as an `audio_url` data URI part (ogg/opus voice messages work as is) and returns a transcript or description; `[Audio N (...): ...]` is added. | 1 per audio (cached) |
| video | (always describe) | A video-capable model describes each video (`video_url` part); `[Video N (...): ...]` is added. | 1 per video (cached) |
| sticker | `enabled: false` (or absent) | `[Sticker: name — description (tags: ...)]` from Discord's sticker details. | none (1 Discord API call per sticker, cached) |
| sticker | `enabled: true` | A vision model describes the sticker image: `[Sticker: name — ...]`. Lottie stickers fall back to the text. | 1 per sticker ID (cached) |

Every modality turns media into text with its own model, and the result is reused on later turns (cached by attachment ID or CDN path, which survives Discord re-signing CDN URLs; see [Shared media cache](#shared-media-cache)). The base model only sees the text. There is no "direct" mode that sends media to the reply model: omni models given media inside the long, tool-carrying reply request tended to ignore it. `image.mode` and `video.mode` were removed: a config that sets either fails validation and asks you to remove the key. `audio.mode` accepts only `asr` and `describe`; `direct` fails validation.

Stickers have no mode: `sticker.mode` was removed and is ignored with a deprecation warning; `sticker.enabled` alone decides whether a vision model reads the image.

For audio, `asr` needs a transcription endpoint (e.g. `IFM/Jais-ASR`); `describe` works with any audio-capable chat model (e.g. `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning`, which needs `max_tokens` around 12000) and can also describe music or sounds.

## Prompt layout

Everything media-related goes into the user prompt; the system prompt is byte-identical on every request. Each message line starts with its timestamp in RFC3339 format, in UTC, followed by the author's role (`User`, `Bot` for other bots, `Assistant` for the bot itself). The message text is quoted as a Go string literal, so the line breaks before media lines appear as `\n`. A current message with a voice message in `asr` mode looks like:

```
<currentMessage>
[2026-11-02T01:20:00Z] [User] Alice: "what do you think?\n[Audio 1 transcript (voice message, 4s): should we meet at noon]"
</currentMessage>
```

History lines in `<context>` use the same format.

## History enrichment

History messages reuse cached descriptions/transcripts. A model call for a history message happens only for the most recent message of that kind, and only when the user replied to it or mentions it ("image", "photo", "voice", "audio", "video", "clip", ...). This keeps a busy channel from re-describing media on every reply.

## Shared media cache

One cache is created at startup and shared by the reply prompt, history enrichment and memory consolidation. It holds image descriptions, audio transcripts and video descriptions under a stable key (the attachment ID, or the CDN path without its signed query; never the signed URL), and it lets only one caller compute a given attachment at a time, so each attachment is processed once while it is in context, even when the reply and a consolidation run ask for it together. Plain-text messages never re-process history media.

- **Pinning.** Media still in a channel's prompt window (history plus the current message) is pinned and never expires. When it scrolls out of the window it drops back to the idle TTL.
- **Idle TTL** (`core.discord.image_cache_ttl_min`). Unpinned entries expire after this many minutes unused; each use refreshes them.
- **Size bound** (`core.discord.image_cache_max_entries`). Over it, the least recently used unpinned entries are dropped first; pinned entries are never evicted.
- **Failures.** Permanent failures (over the size or duration limit, wrong content type, a download 4xx other than 408/429, a model 400/404/413/415/422, a transcode error) are remembered for 5 minutes, so a broken attachment is not retried on every message. Timeouts, 429, 5xx and empty model output are not cached and are retried.
- **Hot reload.** The TTL and size are updated in place. A kind's cache is dropped only when its model, endpoint, prompt or transcode settings change; other kinds keep theirs.

Sticker details and descriptions use a separate cache keyed by sticker ID that follows the same rules, with `sticker.cache_ttl_min` and `sticker.cache_max_entries`. Both caches are in memory only and start empty after a restart.

## Limits

`max_items` caps media per message; `max_bytes` and `max_duration_sec` skip oversized audio/video (all three are required when the modality is enabled) (the duration is known for Discord voice messages). Images use `core.ai.max_image_bytes`. Skipped media still shows a placeholder line so the model knows it was there.

## ffmpeg

Optional. Only `audio.transcode` (mp3/wav, e.g. for an ASR endpoint that rejects ogg/opus, or for `part_type: input_audio`) and `video.transcode` (trim + downscale) use it. When one of them is enabled, `core.ai.modalities.ffmpeg_path` must be set (for example `ffmpeg`, looked up in `PATH`), and if ffmpeg is missing the bot refuses to start with a clear error instead of failing on the first message.

The Docker image built from the repository's `Dockerfile` does not include ffmpeg. To use transcoding in Docker, add it to the runtime stage; see [DEPLOYMENT.md](DEPLOYMENT.md#ffmpeg-in-docker).

## Migrating from `core.ai.vision`

The old block still loads, with a warning, and is mapped onto `core.ai.modalities.image`:

| Old `vision.mode` | New |
|-------------------|-----|
| `text_only` | `image.enabled: false` |
| `hybrid` | `image.enabled: true` (`vision.model` describes images) |
| `multimodal` | `image.enabled: true`, same as `hybrid`: images are described, never sent to the reply model |

`description_prompt` → `prompt`, `max_images` → `max_items`, `base64` → `transport` (`base64`/`url`); endpoint fields carry over with the same presence rule (an absent key inherits `core.ai`, an explicit `0` is kept). With no built-in defaults, an enabled legacy block must set `description_prompt` and `max_images`. If `core.ai.modalities.image` is present, `core.ai.vision` is ignored.
