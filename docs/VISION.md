# Media Modalities (images, audio, video, stickers)

Text is always handled by the base model (`core.ai`), which is treated as text-only: the reply request never carries media. Images, audio, video and stickers are optional modalities configured under `core.ai.modalities`, each with its own on/off switch and its own model. Field reference: [CONFIGURATION.md](CONFIGURATION.md#coreaimodalities-optional).

## Modes

| Modality | Mode | What happens | Extra model calls |
|----------|------|--------------|-------------------|
| image | (always describe) | A vision model describes each image; `[Image N: ...]` is added to the message. | 1 per image (cached) |
| audio | `asr` | Speech is transcribed by an `/audio/transcriptions` endpoint; `[Audio N transcript (...): ...]` is added. | 1 per audio (cached) |
| audio | `describe` | An audio-capable chat model gets the audio as an `audio_url` data URI part (ogg/opus voice messages work as is) and returns a transcript or description; `[Audio N (...): ...]` is added. | 1 per audio (cached) |
| video | (always describe) | A video-capable model describes each video (`video_url` part); `[Video N (...): ...]` is added. | 1 per video (cached) |
| sticker | `text` | `[Sticker: name — description (tags: ...)]` from Discord's sticker details. | none (1 Discord API call per sticker, cached) |
| sticker | `vision` | A vision model describes the sticker image: `[Sticker: name — ...]`. Lottie stickers fall back to text. | 1 per sticker ID (cached) |

Every modality turns media into text with its own model, and the result is reused on later turns (cached by attachment ID, which survives Discord re-signing CDN URLs). The base model only sees the text. There is no "direct" mode that sends media to the reply model: omni models given media inside the long, tool-carrying reply request tended to ignore it. A config with `mode: direct` (or any `image.mode` / `video.mode`) fails validation and asks you to remove it.

For audio, `asr` needs a transcription endpoint (e.g. `IFM/Jais-ASR`); `describe` works with any audio-capable chat model (e.g. `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning`, which needs `max_tokens` around 12000) and can also describe music or sounds.

## Prompt layout

Everything media-related goes into the user prompt; the system prompt is byte-identical on every request. A current message with a voice message in `asr` mode looks like:

```
<currentMessage>
[Mon 2026-11-02 9:20 AM (UTC+08:00)] [User] Alice: "what do you think?
[Audio 1 transcript (voice message, 4s): should we meet at noon]"
</currentMessage>
```

## History enrichment

History messages reuse cached descriptions/transcripts. A model call for a history message happens only for the most recent message of that kind, and only when the user replied to it or mentions it ("image", "photo", "voice", "audio", "video", "clip", ...). This keeps a busy channel from re-describing media on every reply.

## Limits

`max_items` caps media per message; `max_bytes` and `max_duration_sec` skip oversized audio/video (the duration is known for Discord voice messages). Images use `core.ai.max_image_bytes`. Skipped media still shows a placeholder line so the model knows it was there.

## ffmpeg

Optional. Only `audio.transcode` (mp3/wav, e.g. for an ASR endpoint that rejects ogg/opus, or for `part_type: input_audio`) and `video.transcode` (trim + downscale) use it. When one of them is enabled and ffmpeg is missing, the bot refuses to start with a clear error instead of failing on the first message.

## Migrating from `core.ai.vision`

The old block still loads, with a warning, and is mapped onto `core.ai.modalities.image`:

| Old `vision.mode` | New |
|-------------------|-----|
| `text_only` | `image.enabled: false` |
| `hybrid` | `image.enabled: true` (`vision.model` describes images) |
| `multimodal` | `image.enabled: true`, same as `hybrid`: images are described, never sent to the reply model |

`description_prompt` → `prompt`, `max_images` → `max_items`, `base64` → `transport` (`base64`/`url`); endpoint fields carry over. If `core.ai.modalities` is present, `core.ai.vision` is ignored.
