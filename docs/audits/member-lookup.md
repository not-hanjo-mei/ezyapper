# Audit: server info, user info, member search, mention conversion

Status: investigation only. Nothing below has been changed yet.
Scope: `internal/ai/tools/discord.go` (get_server_info, get_user_info,
search_guild_members, get_channel_members) and `internal/bot/mentions.go`
plus its caller in `internal/bot/handlers_history.go`.

## 1. Member search (search_guild_members)

The tool runs three stages: (1) the local state cache, (2) a REST fetch of
guild members matched locally, (3) Discord's `GuildMembersSearch`.

| # | Finding | Severity | Can return the wrong member? |
|---|---------|----------|------------------------------|
| 1.1 | Stage 1 short-circuits stage 2. The state cache is partial for guilds above the gateway large threshold (250 members) because only members seen in events are cached. If any cached member *substring*-matches, stages 2 and 3 never run, so an exact match that is not cached is never found. Query `alex` returns cached `alexander`, never the real `Alex`. | High | Yes |
| 1.2 | No ranking. Matches are returned in cache/API order and cut at `limit`. An exact username match is not preferred over a prefix or substring match. With `limit=1` (the model often asks for one) the result is effectively arbitrary: query `al` may return `Natalie`. | High | Yes |
| 1.3 | Stage 3 does not use the same fields or display name as stages 1 and 2. Discord's search matches username and nickname by prefix only (not global name, not substring), and its results are returned unfiltered with display name `Nick > Username` (global name ignored). Stages 1 and 2 match username, nickname and global name by substring and use `Nick > GlobalName > Username`. The same person can appear under two different display names depending on which stage answered. | Medium | No, but inconsistent |
| 1.4 | Stage 2 fetches only the first 1000 members (one page, no pagination). In larger guilds, members past 1000 are invisible to stage 2; stage 3 then only finds prefix matches. | Medium | Indirectly (falls back to weaker matching) |
| 1.5 | Matching is plain `strings.ToLower` + `Contains`. No Unicode normalization or case folding (`strings.EqualFold` semantics), so names with full-width characters, accents or decorative Unicode do not match their plain-text spelling. | Low | No (misses) |
| 1.6 | The ID returned (`member.User.ID`, mention `<@ID>`) is the same ID the mention path uses. No mismatch found here. | OK | No |

## 2. Mention conversion

| # | Finding | Severity |
|---|---------|----------|
| 2.1 | ID to name (inbound, `ReplaceMentions`): replaces `<@ID>` / `<@!ID>` with `@username`, using only users seen in the fetched history plus the current message's mentions. Mentions of anyone else stay as raw `<@ID>`. It uses the username, while the rest of the prompt (and the target prompt layout) identifies people by display name, so the model sees two names for one person. | Medium |
| 2.2 | Role mentions `<@&ID>` and `@everyone`/`@here` are not converted or labeled. | Low |
| 2.3 | Name to ID (outbound) does not exist. The bot relies on the model typing `<@ID>` itself. A reply containing `@alice` or `<@Alice Mana>` (the form used in the target context layout) does not ping anyone. "Both directions" is currently one direction. | High (feature gap) |
| 2.4 | `strings.ReplaceAll` in a loop over all mappings is O(users x length) and order-dependent; harmless at current sizes. | Low |

## 3. Server and user info

| # | Finding | Severity |
|---|---------|----------|
| 3.1 | `get_server_info` uses a REST `Guild` call, which returns `member_count` 0 unless counts are requested; it also reports the deprecated `region` field. The state cache (or `GuildWithCounts`) has the real numbers. | Medium |
| 3.2 | `get_user_info` reports `display_name` as the nickname only, empty when the member has no nick (ignores global name). Inconsistent with search results. | Medium |
| 3.3 | `get_channel_members` actually lists guild members (no channel filter) and is capped by `limit`, unordered. Name is misleading. | Low |
| 3.4 | All tools accept any `guild_id` / `channel_id` the model supplies. The bot can be talked into reading channels and member lists of other servers it is in. | Medium (privacy) |

## Proposed fixes (pending approval)

1. One matcher for every stage: fields username, nickname, global name;
   Unicode-folded; ranked exact > prefix > substring, then by field
   (nick/global name before username); stage 3 results filtered and named by
   the same matcher.
2. Run stage 2 (paginated, full list) whenever stage 1 has no *exact* match,
   instead of only when stage 1 is empty; cache the full list briefly.
3. One display-name rule everywhere: nick > global name > username.
4. Mentions both ways: inbound `<@ID>` to `@DisplayName` for any resolvable
   member (state cache, then a cached `GuildMember` lookup); outbound
   `<@Name>` / `@Name` to `<@ID>` only on an unambiguous exact match against
   participants of the current conversation, never on a fuzzy match.
5. `get_server_info` from state with counts; drop `region`.
6. Restrict tool guild/channel arguments to the guild the message came from
   (configurable).
