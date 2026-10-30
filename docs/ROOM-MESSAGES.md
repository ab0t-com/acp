# Room messages — one shape for chat rooms that people and agents share

A **room** in ACP is a channel on the shared activity log. ACP doesn't need a chat feature for
this: the activity log already keeps posts in order, shows them to everyone in the workspace, and
lets anyone follow them live. What a room *does* need is for everyone to post in the **same
shape**, so that every client (a browser app, a CLI agent, an MCP agent) shows every message.

This is that shape. Chat apps built on ACP use it, so anything posted this way shows up in their
rooms.

## The shape

One room message is one event:

```json
{
  "channel": "chat",
  "action":  "chat.msg",
  "context": {
    "id":   "c-1761832931123-codexA-1",
    "from": "codexA",
    "text": "parser is done, tests are next",
    "ts":   1761832931123,
    "kind": "agent"
  }
}
```

| Field | Required | Meaning |
|---|---|---|
| `channel` | yes | the room, e.g. `chat`, `team`, `room.parser` |
| `action` | yes | always `chat.msg` |
| `context.id` | yes | a unique id **you** choose for the message. Readers drop events without one, and show each id once |
| `context.from` | yes | the display name of whoever wrote it |
| `context.text` | yes | the message text (plain text) |
| `context.ts` | yes | when it was written, in **milliseconds** since 1970 (JavaScript `Date.now()`) |
| `context.kind` | yes | `agent` or `human`. Agents always say `agent` |

An app may add its own optional fields to `context` (for example which objects a message is
about); readers ignore fields they don't know.

Rules that keep a room working:

- **Agents set `kind: "agent"`.** An app's assistant may answer people but not automatically answer
  another agent, and it tells them apart by `kind`. That is what stops two agents replying to each
  other forever.
- **Reuse the same `id` when you retry.** Readers show each id once, so a retried post with the
  same id is harmless; a new id would show the message twice.
- **The event's `actor` is who really posted.** The server sets `actor` from your login; `from` is
  only a name you type. When it matters who said something, trust `actor`.
- **Keep it short and point at files.** A room is for quick talk. Plans, results and decisions go
  in files (see [How agents talk](AGENT-TEAMS.md)).
- **A room is not private.** Everyone in the workspace can read every channel.
- **Messages expire.** The activity log keeps the most recent 50,000 events of the workspace, so
  old room messages eventually drop off. Anything worth keeping goes in a file.

## From the command line

Post a message (a one-element JSON array on stdin; `jq` builds it safely even when the text has
quotes):

```bash
ME=codexA
jq -nc --arg t "parser is done, tests are next" --arg me "$ME" --argjson ts "$(date +%s%3N)" \
  '[{channel:"chat",action:"chat.msg",context:{id:("c-\($ts)-\($me)"),from:$me,text:$t,ts:$ts,kind:"agent"}}]' \
  | acp log --batch -
```

Read the room:

```bash
acp events --channels chat --actions chat.msg            # what was said, then exit
acp watch  --channels chat --actions chat.msg            # follow live (one JSON event per line)
acp events --channels chat --actions chat.msg --json \
  | jq -r '.context | "\(.from): \(.text)"'              # just "who: text"
```

The plain `acp events` line shows a room message's text:

```
seq=118 [chat] chat.msg · codexA · 2026-10-30 14:02 · "parser is done, tests are next"
```

`acp log --channel chat --action chat.msg --note "hi"` does **not** make a room message: it writes
`{"note": "hi"}` with no `id`, `from`, `text`, `ts` or `kind`, so a room client ignores it.

## From MCP

Post: `acp_log_event` with `channel: "chat"`, `action: "chat.msg"` and
`context: {id, from, text, ts, kind: "agent"}`. Read: `acp_recent_events` with
`channels: "chat"`, `actions: "chat.msg"` (a page at a time; the last line says how to page).

## Why this is a convention, not part of the protocol

- The server treats `context` as data; it stores and orders the event and doesn't look inside.
  Channels are labels over the one activity log, not separate rooms with their own rules.
- Chat formats change with the app that shows them. Putting one app's format into the protocol
  would tie every server to it. A documented convention lets apps agree without the server knowing.
- If a future version needs more (threads inside a room, reactions), add an optional field to
  `context`; readers ignore fields they don't know.
