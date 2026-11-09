# Sessions and token usage

DoneWhen can record every Claude Code session: its project, branch, tickets, tokens and cost. You see where tokens go without reading local files.

## What is sent

`donewhen sessions push` reads Claude Code transcripts (`~/.claude/projects/*/*.jsonl`). It reads only these fields of each line:

| Field | Used for |
|---|---|
| `sessionId` | the session id |
| `cwd` | the project path (from the first line) |
| `gitBranch` | the branch (the last one) and the ticket keys (`feature/biz-277-home` → `BIZ-277`) |
| `timestamp` | start and end time |
| `message.id`, `message.model`, `message.usage` | the token sums per model |

It **never sends message text, tool input or tool output.** The parser does not even decode them.

- Claude Code writes one line per content block of a message. The same `message.id` can appear on several lines. The importer counts each message once.
- A subagent's transcript (`<session>/subagents/*.jsonl`) carries the same session id. The importer adds it to its parent session.
- Lines with the model `<synthetic>` are not model calls. The importer skips them.

## Cost

The server prices each model when it saves a session. The prices are in `internal/models/prices.go` (US dollars per million tokens).

- Check them against https://www.anthropic.com/pricing. Prices change.
- The cost is stored. A later price change does not rewrite old sessions.
- An unknown model costs 0. The session is still saved.
- Cache writes use the 1-hour cache price (2 × input), because Claude Code uses the 1-hour cache.

## Send sessions by hand

```sh
export DONEWHEN_URL=https://donewhen.example.com
export DONEWHEN_TOKEN=donewhen_...        # or put the token in ~/.config/donewhen/token
export DONEWHEN_WORKSPACE=biz             # needed when the token is not pinned to one workspace

donewhen sessions push --file ~/.claude/projects/<project>/<session>.jsonl
donewhen sessions push --dir ~/.claude/projects --since 2026-11-01
```

- `--dir` reads every `*.jsonl` file under the folder. `--since` skips files that last changed before that date.
- It prints one line per session: `sent <id> <tokens> $<cost>`.
- The exit code is `1` if any session failed.
- Sending the same session again replaces it. The numbers never double.

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
| PUT | `/api/sessions/{externalId}` | `projectPath`, `gitBranch`, `issueKeys`, `startedAt`, `endedAt`, `messages`, `usage[]` (`model`, `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWriteTokens`) | `200` the saved session with `tokens` and `costUsd` |
| GET | `/api/sessions` | `from`, `to`, `project`, `issue`, `limit` (default 100, max 500) | sessions, newest first, with usage rows and totals |
| GET | `/api/sessions/summary` | `from`, `to`, `group=day\|project\|model\|issue` | rows `{key, sessions, tokens, costUsd}` |

- A bad body → `400` with `"code": "invalid_session"`. Unknown fields are refused.
- The server adds the ticket keys of `gitBranch` to `issueKeys`.
- `from` and `to` take a date (`YYYY-MM-DD`, UTC) or an RFC 3339 time. A date in `to` includes that whole day. Both filter on the start time.
- `group=day` uses UTC days. `group=issue` counts a session under each of its keys; sessions with no key are under `""`. `group=model` counts the sessions that used the model and sums only that model's rows.
- Saving publishes a `session.saved` event. It writes no activity entry.
- The tables are `ai_sessions` and `ai_session_usage` (migration `0051_agent_sessions.sql`).
