# Sessions and token usage

DoneWhen can record every Claude Code session: its project, branch, tickets, tokens and cost. You see where tokens go without reading local files.

## What is sent

`donewhen sessions push` reads Claude Code transcripts (`~/.claude/projects/*/*.jsonl`). It reads only these fields of each line:

| Field | Used for |
|---|---|
| `sessionId` | the session id |
| `cwd` | the project path (from the first line) |
| `gitBranch` | the branch (the last one) and the ticket keys (`feature/biz-277-home` → `BIZ-277`) |
| `timestamp` | start and end time, and the time of each message |
| `message.id` (or the line `uuid` when there is no id) | counting each message once |
| `message.model`, `message.usage` | the tokens of each message |

It **never sends message text, tool input or tool output.** The parser does not even decode them. Message ids are opaque ids, not content.

- Claude Code writes one line per content block of a message. The same `message.id` can appear on several lines. The importer sends each message once.
- Subagent transcripts are under `<session>/subagents/`, at any depth (also `workflows/wf_*/`). Only `agent-*.jsonl` files are transcripts; `journal.jsonl` is not. Their messages go to the parent session.
- Lines with the model `<synthetic>` are not model calls. The importer skips them.

## Each message counts once

The server stores one row per message (`ai_session_messages`). A message id is unique in a workspace: **the first session that sends it keeps it.**

- A resumed session copies earlier lines (same message ids) under its new session id. Those messages stay with the first session and are not counted again.
- A push is **additive and idempotent**: it adds the messages the server does not have yet. Sending the same push again changes nothing. Sending only part of a session (for example one subagent file) never reduces it.
- After each push the server rebuilds the session's per-model sums (`ai_session_usage`), its message count, and its start and end (the times of its first and last message).
- `--dir` sends the oldest files first, so a first session gets its messages before the session that resumed it.
- With `--file` (and the hook), the session pushed first keeps the shared messages. If you push a resumed session's file before the original session's file, the resumed session keeps the copied messages. The totals are still right; only which session shows them differs. Push with `--dir` to get the oldest-first order.

## Cost

The server prices each model when it saves a session. The prices are in `internal/models/prices.go` (US dollars per million tokens).

- Check them against https://www.anthropic.com/pricing. Prices change.
- The cost is stored. A later price change does not rewrite old sessions.
- An unknown model costs 0. The session is still saved.
- Cache writes are priced by cache lifetime: 5-minute writes at 1.25 × input, 1-hour writes at 2 × input. The importer reads `usage.cache_creation.ephemeral_5m_input_tokens` and `ephemeral_1h_input_tokens`. When a line has no split, the whole `cache_creation_input_tokens` counts as 5-minute.

## Send sessions by hand

```sh
export DONEWHEN_URL=https://donewhen.example.com
export DONEWHEN_TOKEN=donewhen_...        # or put the token in ~/.config/donewhen/token
export DONEWHEN_WORKSPACE=biz             # needed when the token is not pinned to one workspace

donewhen sessions push --file ~/.claude/projects/<project>/<session>.jsonl
donewhen sessions push --dir ~/.claude/projects --since 2026-11-01
```

- `--dir` reads every `*.jsonl` file under the folder. `--since` skips sessions whose files all last changed before that date.
- `--file` on a subagent file (`<session>/subagents/…/agent-*.jsonl`) sends it to its parent session. Any other file under `subagents/` (such as `journal.jsonl`) is refused with exit code `2`.
- A session goes in chunks of 2,000 messages per request, so a long session stays under the body size limit.
- It prints one line per session: `sent <id> <tokens> $<cost>`.
- The exit code is `1` if any session failed.
- Sending the same session again changes nothing. The numbers never double.

## Send each session when it ends (hook)

`scripts/claude-session-end.sh` is a Claude Code `SessionEnd` hook. It reads `transcript_path` from the hook input and runs `donewhen sessions push --file <path>` in the background. It always exits `0`, so a session never fails because of DoneWhen.

Add this to `~/.claude/settings.json`. The build does not change your settings: you add it yourself.

```json
{
  "env": {
    "DONEWHEN_URL": "https://donewhen.example.com",
    "DONEWHEN_WORKSPACE": "biz"
  },
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/donewhen/scripts/claude-session-end.sh"
          }
        ]
      }
    ]
  }
}
```

- Keep the token in `~/.config/donewhen/token`, not in `settings.json`.
- The `donewhen` binary must be on `PATH`, or set `DONEWHEN_BIN` to its full path.
- The hook writes the push output to `~/.cache/donewhen/sessions.log` (change it with `DONEWHEN_SESSIONS_LOG`).

## API

All three routes are behind the workspace guard. `PUT` needs an API token or a browser session.

| Method | Path | Input | Result |
|---|---|---|---|
| PUT | `/api/sessions/{externalId}` | `projectPath`, `gitBranch`, `issueKeys`, `startedAt`, `endedAt`, `messages[]` (`id`, `model`, `at`, `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWrite5mTokens`, `cacheWrite1hTokens`), at most 5,000 per request | `200` the whole saved session with `tokens` and `costUsd` |
| GET | `/api/sessions` | `from`, `to`, `project`, `issue`, `limit` (default 100, max 500) | sessions, newest first, with usage rows and totals; `messages` is the number of messages the session holds |
| GET | `/api/sessions/summary` | `from`, `to`, `group=day\|project\|model\|issue` | rows `{key, sessions, tokens, costUsd}` |

- PUT is additive: the server keeps messages it already has (see above), merges the metadata (earliest start, latest end, union of ticket keys, the branch of the latest push), then rebuilds the sums.
- A usage row has `cacheWriteTokens` (the sum) and `cacheWrite5mTokens`, `cacheWrite1hTokens`.
- A bad body → `400` with `"code": "invalid_session"`. Unknown fields are refused. A message id may appear only once per request.
- The server adds the ticket keys of `gitBranch` to `issueKeys`.
- `from` and `to` take a date (`YYYY-MM-DD`, UTC) or an RFC 3339 time. A date in `to` includes that whole day. Both filter on the start time.
- `group=day` uses UTC days. `group=issue` counts a session under each of its keys; sessions with no key are under `""`. `group=model` counts the sessions that used the model and sums only that model's rows.
- Saving publishes a `session.saved` event. It writes no activity entry.
- The tables are `ai_sessions`, `ai_session_messages` and `ai_session_usage` (migration `0051_agent_sessions.sql`). Costs are `numeric(14,6)`.
