# Prompt Layout and Caching

LLM providers cache the longest common *prefix* of consecutive requests.
EZyapper orders the request so that prefix is as long as possible.

## System prompt (static)

```
<instruction>
{core.ai.system_prompt}
</instruction>

<{tag}>              one section per plugin implementing SystemPromptProvider
...                  (queried once at plugin load)
</{tag}>
```

It contains nothing that varies per request: no names, IDs or times. It is
byte-identical for every request of a running bot, so it is always cached.

## User prompt (one message)

Sections are ordered from "changes least" to "changes most":

```
<server_info>        server name, ID, description        stable per channel
<channel_info>       channel name, ID, topic             stable per channel
<context>            history, oldest first               append-only (see below)
<participants>       Name (@username, ID:id) — role      changes when someone new speaks
<{plugin tag}>       AfterDecision plugin blocks          per reply
<user_profile>       traits, interests, facts, preferences of the author
<key_facts>          retrieved memories
<recent_memories>
<currentMessage>     the message being answered
```

Each message is one line:

```
[2026-11-01T05:20:11Z] [User] [reply to Bob: "text"] Alice Mana: "content"
```

- The timestamp is the Discord message timestamp (UTC), also for the current message.
- Roles are `User`, `Bot` (another bot) and `Assistant` (this bot).
- User mentions are shown as `<@DisplayName>` and channel mentions as `#name`.
  When the model writes `<@Name>` for an unambiguous participant, it is
  converted back to a real `<@id>` ping before sending.
- Text is not HTML-escaped.
- Image descriptions, audio transcripts and video descriptions (describe/asr modalities) are appended as `[Image N: ...]`, `[Audio N transcript (...): ...]`, `[Video N (...): ...]`.

## Anchored context window

With `memory_pipeline.memory.context_flush_window` > 0 the history window does not slide
by one message per request (which would change the first line of `<context>`
and break the cache every time). It is anchored at its oldest message and only
grows until it holds `short_term_limit + context_flush_window` messages; then
the oldest `context_flush_window` (or more) messages are dropped at once and a
new anchor is set. Between flushes every request shares the previous
request's prefix up to the end of the old context.

## Checking the real request

Set `EZYAPPER_CAPTURE_DIR` to capture the exact JSON sent to the providers
(see DEPLOYMENT.md).
