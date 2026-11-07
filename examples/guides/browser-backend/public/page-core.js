// page-core.js — everything the page does with ACP, with no DOM in it. The page (app.js) wires it to buttons and
// text boxes; the example's test journey (journey.mjs) runs the SAME code in Node through the same backend, so
// what is tested is what the page runs.
//
// The page never holds a token: the SDK client has NO token and its base URL is this site's /acp/ — the backend
// adds the person's own login to every request. The browser sends the session cookie by itself (same site).
//
// `ACP` is the SDK's browser bundle (import * as ACP from "/sdk/acp.esm.js"); `http` is a fetch for this site
// (the page passes the browser's fetch; Node passes one that carries the tab's cookie).

export const CHAT_CHANNEL = "room";
export const NOTES_DOC = "team-notes.md";
export const BOARD_DOC = "team-board";

/** Log in with an invite code: the backend redeems it and keeps the login. Returns { name, space, role }. */
export async function login(http, origin, invite, name) {
  const r = await http(origin + "/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(name ? { invite, name } : { invite }),
  });
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(body.error ?? `login failed (${r.status})`);
  return body;
}

/** Who this tab is logged in as, or null. */
export async function whoAmI(http, origin) {
  const r = await http(origin + "/me");
  return r.ok ? r.json() : null;
}

/** The SDK client for this tab: no token, the backend's /acp/ as its server. */
export function connect(ACP, http, origin, me) {
  // `agent` is only a label here (the backend drops the header; coordd names you by your login). Passing your own
  // name lets the SDK recognise your own changes when they come back on the stream.
  return new ACP.Client({ baseUrl: origin + "/acp", agent: me.name, space: me.space, fetchImpl: http });
}

/**
 * Chat: a room on the event log, in the shared room-message shape (docs/ROOM-MESSAGES.md), so CLI and MCP
 * agents in the same room see these messages and theirs show here. `onMessage({from, text, at, id})` for every
 * message of the room, oldest first (its history, then live); each id is shown once. `from` is the sender's
 * LOGIN (the event's actor, set by the server), not the name they typed. Returns { say, stop }.
 */
export function openChat(ACP, client, me, onMessage) {
  const shown = new Set();
  let n = 0;
  const stop = client.subscribe(
    (e) => {
      const c = e.context ?? {};
      if (typeof c.id !== "string" || typeof c.text !== "string" || shown.has(c.id)) return;
      shown.add(c.id);
      onMessage({ id: c.id, from: e.actor, text: c.text, at: e.at });
    },
    { filter: new ACP.EventFilter({ channels: [CHAT_CHANNEL], actions: ["chat.msg"] }), from: 1 },
  );
  return {
    say: (text) => {
      const ts = Date.now();
      const id = `c-${ts}-${me.name}-${++n}`;
      // The same id doubles as the idempotency key: a retried post is never shown twice.
      return client.appendIdem({ action: "chat.msg", channel: CHAT_CHANNEL, context: { id, from: me.name, text, ts, kind: "human" } }, id);
    },
    stop,
  };
}

/** Mail: `onMail(messages)` with the unread inbox whenever new mail for this person arrives. Returns stop(). */
export function watchMail(ACP, client, me, onMail) {
  const check = async () => onMail(await client.inbox(true));
  check().catch(() => {});
  return client.subscribe(
    (e) => {
      if (e.entity === me.name) check().catch(() => {});
    },
    { filter: new ACP.EventFilter({ actions: ["chat.sent"] }), onResync: () => check().catch(() => {}) },
  );
}

export function sendMail(client, to, subject, body) {
  return client.send({ to, type: "inform", subject, body });
}

/** The shared notes: a live text document. Render `onRemote(text)` straight into the editor. */
export function openNotes(ACP, client, onRemote, onPending) {
  return ACP.TextDocSession.open(client, NOTES_DOC, { onRemote, onPending });
}

/** The shared board: a live JSON document { todo: [...] }. */
export async function openBoard(ACP, client, onChange) {
  const s = await ACP.JSONDocSession.open(client, BOARD_DOC, { onChange });
  const v = s.value();
  if (!v || !Array.isArray(v.todo)) await s.apply([{ t: "set", path: ["todo"], value: [] }]).catch(() => {});
  return {
    session: s,
    items: () => s.value()?.todo ?? [],
    // Array elements: lins / ldel (never a whole-array set — a teammate's concurrent change would be lost).
    add: (text) => s.apply([{ t: "lins", path: ["todo"], idx: 0, value: text }]),
    remove: (idx) => s.apply([{ t: "ldel", path: ["todo"], idx }]),
  };
}

/**
 * The shared files: a mount of the workspace's files/ folder, kept in memory (a page has no disk). `conflict:
 * "throw"` — a file a teammate changed since you opened it is NOT overwritten: flush() throws, carrying your
 * bytes, and the page asks what to do. Nothing is saved until flush() resolves (hasPending() tells).
 */
export function openFiles(ACP, client, host, me, onChange, onPending) {
  const fs = ACP.mountVirtualFS({ uri: `acp://${host}/${me.space}/files/`, client, conflict: "throw", onPending });
  fs.watch("", (ev) => onChange(ev));
  return fs;
}

/** Presence: say "I'm here" (expires on its own if the tab goes away). */
export function announce(client, me, state) {
  return client.setAwareness({ name: me.name, ...state }, 30, "tab");
}

/** True while anything this tab changed is not yet saved on the server — warn before the tab closes. */
export function unsaved(...parts) {
  return parts.some((p) => p && typeof p.hasPending === "function" && p.hasPending());
}
