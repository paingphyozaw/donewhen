#!/usr/bin/env bash
# Claude Code SessionEnd hook (BIZ-278): send this session's token usage to
# DoneWhen with `donewhen sessions push --file <transcript>`.
#
# It reads the hook JSON from stdin, starts the push in the background and
# always exits 0, so a session never fails because of DoneWhen. The push sends
# numbers and metadata only, never message text. Setup: docs/SESSIONS.md.
#
# Env: DONEWHEN_URL, DONEWHEN_TOKEN (or ~/.config/donewhen/token),
# DONEWHEN_WORKSPACE, and optionally DONEWHEN_BIN (default: donewhen on PATH)
# and DONEWHEN_SESSIONS_LOG (default: ~/.cache/donewhen/sessions.log).

{
	input=$(cat)

	path=""
	if command -v jq >/dev/null 2>&1; then
		path=$(printf '%s' "$input" | jq -r '.transcript_path // empty' 2>/dev/null)
	fi
	if [ -z "$path" ]; then
		path=$(printf '%s' "$input" | sed -n 's/.*"transcript_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
	fi

	bin=${DONEWHEN_BIN:-donewhen}
	if [ -n "$path" ] && command -v "$bin" >/dev/null 2>&1; then
		log=${DONEWHEN_SESSIONS_LOG:-$HOME/.cache/donewhen/sessions.log}
		mkdir -p "$(dirname "$log")" 2>/dev/null || log=/dev/null
		nohup "$bin" sessions push --file "$path" </dev/null >>"$log" 2>&1 &
	fi
} 2>/dev/null

exit 0
