# Media Modalities (images, audio, video, stickers)

Text is always handled by the base model (`core.ai`), which is treated as text-only: the reply request never carries media. Images, audio, video and stickers are the four optional modalities configured under `core.ai.modalities`, each with its own on/off switch and its own model. Field reference: [CONFIGURATION.md](CONFIGURATION.md#coreaimodalities-optional).

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
| sticker | `text` | `[Sticker: name — description (tags: ...)]` from Discord's sticker details. | none (1 Discord API call per sticker, cached) |
| sticker | `vision` | A vision model describes the sticker image: `[Sticker: name — ...]`. Lottie stickers fall back to text. | 1 per sticker ID (cached) |

Every modality turns media into text with its own model, and the result is reused on later turns (cached by attachment ID, which survives Discord re-signing CDN URLs; see [Shared media cache](#shared-media-cache)). The base model only sees the text. There is no "direct" mode that sends media to the reply model: omni models given media inside the long, tool-carrying reply request tended to ignore it. `image.mode` and `video.mode` were removed: a config that sets either fails validation and asks you to remove the key. `audio.mode` accepts only `asr` and `describe`; `direct` fails validation.

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

One cache is created at startup and shared by the reply path, history enrichment and memory consolidation. It holds image descriptions, audio transcripts and video descriptions keyed by attachment ID (never by the signed CDN URL), and it lets only one caller compute a given attachment at a time, so an attachment is turned into text once even when the reply and a consolidation run ask for it together. Its TTL and size come from `core.discord.image_cache_ttl_min` and `core.discord.image_cache_max_entries` and are updated on hot reload; entries that are still within the new limits are kept. Sticker details and descriptions use a separate cache keyed by sticker ID (`sticker.cache_ttl_min`, `sticker.cache_max_entries`). The cache is in memory only and starts empty after a restart.

## Limits

`max_items` caps media per message; `max_bytes` and `max_duration_sec` skip oversized audio/video (the duration is known for Discord voice messages). Images use `core.ai.max_image_bytes`. Skipped media still shows a placeholder line so the model knows it was there.

## ffmpeg

Optional. Only `audio.transcode` (mp3/wav, e.g. for an ASR endpoint that rejects ogg/opus, or for `part_type: input_audio`) and `video.transcode` (trim + downscale) use it. When one of them is enabled and ffmpeg is missing, the bot refuses to start with a clear error instead of failing on the first message.

The Docker image built from the repository's `Dockerfile` does not include ffmpeg. To use transcoding in Docker, add it to the runtime stage; see [DEPLOYMENT.md](DEPLOYMENT.md#ffmpeg-in-docker).

## Migrating from `core.ai.vision`

The old block still loads, with a warning, and is mapped onto `core.ai.modalities.image`:

| Old `vision.mode` | New |
|-------------------|-----|
| `text_only` | `image.enabled: false` |
| `hybrid` | `image.enabled: true` (`vision.model` describes images) |
| `multimodal` | `image.enabled: true`, same as `hybrid`: images are described, never sent to the reply model |

`description_prompt` → `prompt`, `max_images` → `max_items`, `base64` → `transport` (`base64`/`url`); endpoint fields carry over. If `core.ai.modalities` is present, `core.ai.vision` is ignored.
