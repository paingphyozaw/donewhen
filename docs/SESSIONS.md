# Sessions and token usage

DoneWhen can record every Claude Code session: its project, branch, tickets, tokens and cost. You see where tokens go without reading local files.

## What is sent

`donewhen sessions push` reads Claude Code transcripts (`~/.claude/projects/*/*.jsonl`). It reads only these fields of each line:

| Field | Used for |
|---|---|
| `sessionId` | the session id |
| `cwd` | the project path (from the first line) |
| `gitBranch` | the branch of each message, and so its ticket key (`feature/biz-277-home` → `BIZ-277`) |
| `timestamp` | start and end time, and the time of each message |
| `message.id` (or the line `uuid` when there is no id) | counting each message once |
| `message.model`, `message.usage` | the tokens of each message |

For each subagent transcript `<session>/subagents/**/agent-<id>.jsonl` it also reads `agent-<id>.meta.json` next to it (BIZ-282), and only these keys:

| Key | Used for |
|---|---|
| `agentType` | the run's type: `implementer`, `reviewer`, `tester`, `general-purpose`, `Explore`, `Plan`, `fork`… |
| `description` | the short task title the coordinator gave the run ("Build BIZ-278 sessions backend"), cut at 300 characters |
| `model` | the run's model, when the file has it |

The run id is the `<id>` of the file name. A run without a meta file is still sent, with its id only. `description` is the **only free text** that leaves the machine.

It **never sends message text, tool input or tool output.** The parser does not even decode them. Message ids are opaque ids, not content.

- Claude Code writes one line per content block of a message. The same `message.id` can appear on several lines. The importer sends each message once.
- Subagent transcripts are under `<session>/subagents/`, at any depth (also `workflows/wf_*/`). Only `agent-*.jsonl` files are transcripts; `journal.jsonl` is not. Their messages go to the parent session.
- Lines with the model `<synthetic>` are not model calls. The importer skips them.

## Each message counts once

The server stores one row per message (`ai_session_messages`). A message id is unique in a workspace: **the first session that sends it keeps it.**

- A resumed session copies earlier lines (same message ids) under its new session id. Those messages stay with the first session and are not counted again.
- A push is **additive and idempotent**: it adds the messages the server does not have yet. Sending the same push again changes nothing. Sending only part of a session (for example one subagent file) never reduces it.
- After each push the server rebuilds the session's per-model sums (`ai_session_usage`), its message count, its start and end (the times of its first and last message), and its `issueKeys` (the ticket keys of its messages' branches).
- Each message keeps the branch of its own line and the ticket key that branch names. A long session that moves from `feature/biz-10-a` to `feature/biz-11-b` counts each message for its own ticket.
- `--dir` sends the oldest files first, so a first session gets its messages before the session that resumed it.
- With `--file` (and the hook), the session pushed first keeps the shared messages. If you push a resumed session's file before the original session's file, the resumed session keeps the copied messages. The totals are still right; only which session shows them differs. Push with `--dir` to get the oldest-first order.

## Who made each message (BIZ-282)

Every message carries `agentId`: the subagent run whose transcript holds it. A message of the main transcript has none: it is the **coordinator's**. When a fork copies lines of the main transcript, the copy keeps the coordinator (the first file that holds a message id wins, and the main transcript is read first).

A message's ticket key:

| Message from | Key |
|---|---|
| a run whose `description` names a key (`BIZ-278`, upper case) | that key |
| a run with no key in its description | the key of the message's branch |
| the coordinator | the key of the message's branch |

- Runs are stored in `ai_session_agents` (one row per run, unique per workspace; migration `0053_session_agents.sql`). A run belongs to the session that first sent it.
- **Re-push refreshes:** pushing a session again sets `agent_id` and `issue_key` on the messages that session already holds. Tokens, cost and which session owns a message never change. A push from an older importer (no `agentId`) keeps the stored agent. To fix data saved before BIZ-282, run `donewhen sessions push --dir …` again.

### Coordinator overhead

A hub coordinator sits on one branch (say `feature/biz-102-app-list`) for days while its runs build other tickets. Its own messages would all count for BIZ-102. So the `ticket` summary keeps them apart:

- A message is **coordinator overhead** when its key comes only from its branch (the coordinator's own message, or a run with no key in its description) **and** its session runs agents for another ticket.
- Those messages form a separate row with `coordinator: true` and the branch key (the UI shows "Coordinator (branch BIZ-102)").
- A session opened on a ticket's own branch with no runs for other tickets stays that ticket's work.
- The `issue` group does not split them: it keys every message by its key, as before.

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
| PUT | `/api/sessions/{externalId}` | `projectPath`, `gitBranch`, `issueKeys`, `startedAt`, `endedAt`, `messages[]` (`id`, `model`, `at`, `branch`, `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWrite5mTokens`, `cacheWrite1hTokens`), at most 5,000 per request | `200` the whole saved session with `tokens` and `costUsd` |
| GET | `/api/sessions` | `from`, `to`, `project`, `issue`, `limit` (default 100, max 500) | sessions, newest first, with usage rows and totals; `messages` is the number of messages the session holds |
| GET | `/api/sessions/summary` | `from`, `to`, `project`, `issue`, `group=day\|project\|model\|issue\|kind\|agent_type\|agent\|ticket` | rows `{key, sessions, messages, tokens, cacheReadTokens, costUsd, …}` |

- PUT also takes `agents[]` (`id`, `type`, `description` at most 300 characters, `model`) and `agentId` per message (BIZ-282).
- PUT is additive: the server keeps messages it already has (see above), merges the metadata (earliest start, latest end, union of ticket keys, the branch of the latest push), then rebuilds the sums.
- A usage row has `cacheWriteTokens` (the sum) and `cacheWrite5mTokens`, `cacheWrite1hTokens`.
- A bad body → `400` with `"code": "invalid_session"`. Unknown fields are refused. A message id may appear only once per request.
- The server adds the ticket keys of `gitBranch` to `issueKeys`.
- `from` and `to` take a date (`YYYY-MM-DD`, UTC) or an RFC 3339 time. A date in `to` includes that whole day.
- **Summary sums messages, not sessions**, so every grouping adds up to the real total:
  - `group=day`: by the UTC date of each message.
  - `group=issue`: by the ticket key of each message's branch; messages with no key are under `""`.
  - `group=model`: by each message's model.
  - `group=project`: by the project of the message's session.
  - `sessions` is the number of sessions with messages in the group.
  - `from`/`to` filter messages by their time; `issue` filters messages by their key.
  - `group=kind`: four rows `input`, `output`, `cache_read`, `cache_write`. Each model's stored cost is split by token count × the price table, then scaled so the four rows add up to the stored cost. No messages → no rows.
  - `group=agent_type`: `coordinator` and each run type; `runs` is the number of runs (for the coordinator: sessions). A run with no meta is `unknown`.
  - `group=agent`: one row per run: `key` (agent id), `type`, `description`, `issue` (the key its description names, else the most common key of its messages), `model`, `session`, `startedAt`, `endedAt`, `messages`.
  - `group=ticket`: one row per ticket key, plus coordinator rows (`coordinator: true`, see above). `costByType` and `runsByType` split the row by agent type; `lastAt` is its last message.
  - `group=day` rows also carry `kinds` (the day's cost by token kind) for the stacked chart.
  - Every row has `messages` and `cacheReadTokens` (the UI's "average context per message" = cache-read tokens ÷ messages).
- **List** keeps per-session totals. Each session also has `kinds` (its cost by token kind) and `agents` (its runs: `id`, `type`, `description`, `issue`, `model`, `messages`, `tokens`, `costUsd`). `from`/`to` return sessions with at least one message in range (a session with no messages: by its start). `issue` returns sessions with at least one message on that ticket's branch.
- Saving publishes a `session.saved` event. It writes no activity entry.
- The tables are `ai_sessions`, `ai_session_messages` and `ai_session_usage` (migration `0051_agent_sessions.sql`), and `ai_session_agents` (`0053_session_agents.sql`). Costs are `numeric(14,6)`.

## The Sessions page

Four views share one filter header (range, project, ticket; kept in the URL):

| View | Path | Shows |
|---|---|---|
| Overview | `/sessions` | 6 tiles, insights, cost per day stacked by token kind, money by kind and by agent type, top 5 tickets |
| Tickets | `/sessions/tickets` | one row per ticket: build (implementer), review (reviewer) and other cost, runs, tokens, last activity; coordinator rows apart. A row opens the Agents view for that ticket |
| Agents | `/sessions/agents` | one row per run; filter by type (`?type=`) and ticket; sort by cost or start |
| Sessions | `/sessions/list` | breakdown by project and model, and the list; the tickets cell is one line (`BIZ-101 +6`); an open row shows usage per model, cost per kind and the runs |

Insights (`insights()` in `web/src/lib/sessions.js`) each have a threshold and stay hidden below it: cache reads ≥ 50% of cost; the split by agent type; average context ≥ 100,000 tokens per message; one ticket ≥ 25% of cost; reviews ≥ 30% of build cost.
