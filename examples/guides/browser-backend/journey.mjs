// journey.mjs — two browser "tabs" (alice and bob) and a command-line agent work together through the backend.
//
// The tabs run the page's OWN code: the SDK bundle and page-core.js are DOWNLOADED FROM THE BACKEND (exactly what
// a browser would load) and imported. Each tab has its own cookie, like two browsers. Nothing here holds an ACP
// token: the tabs only have their session cookie; the agent has its own login (passed by run.sh).
//
// Usage (run.sh does this): node journey.mjs <backend-url>   with env INVITE_ALICE, INVITE_BOB, WORK,
//   and for the agent step: ACP_BIN_DIR, ACP_SERVER, ACP_CERT, AGENT_TOKEN.
// Prints one line per check: "JOURNEY ok: <name>" or "JOURNEY FAIL: <name>: <why>". Exit 1 if any failed.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const origin = process.argv[2];
const work = process.env.WORK;
if (!origin || !work || !process.env.INVITE_ALICE || !process.env.INVITE_BOB) {
  console.error("usage: INVITE_ALICE=… INVITE_BOB=… WORK=<dir> node journey.mjs <backend-url>");
  process.exit(2);
}

let failed = 0;
async function check(name, fn) {
  try {
    await fn();
    console.log(`JOURNEY ok: ${name}`);
  } catch (err) {
    failed++;
    console.log(`JOURNEY FAIL: ${name}: ${err?.message ?? err}`);
  }
}
async function until(what, cond, ms = 10_000) {
  const t0 = Date.now();
  while (!(await cond())) {
    if (Date.now() - t0 > ms) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 50));
  }
}

// ---- load what the page loads, from the backend ----
async function download(p, file) {
  const r = await fetch(origin + p);
  assert.equal(r.status, 200, `GET ${p}`);
  const f = path.join(work, file);
  writeFileSync(f, await r.text());
  return import(pathToFileURL(f).href);
}
const ACP = await download("/sdk/acp.esm.js", "acp.esm.mjs");
const core = await download("/page-core.js", "page-core.mjs");

/** A browser tab: a fetch with this tab's own cookie jar, recording every request header the page sent. */
function newTab() {
  let cookie = "";
  const sent = [];
  const http = async (url, init = {}) => {
    const headers = { ...(init.headers ?? {}) };
    sent.push({ url: String(url), headers: { ...headers } });
    if (cookie) headers.Cookie = cookie;
    const r = await fetch(url, { ...init, headers });
    const sc = r.headers.get("set-cookie");
    if (sc) cookie = /acp_session=[^;]*/.exec(sc)?.[0] ?? cookie;
    return r;
  };
  return { http, sent };
}

const A = newTab();
const B = newTab();
let alice;
let bob;

await check("both people log in with their invite (the backend redeems it)", async () => {
  alice = await core.login(A.http, origin, process.env.INVITE_ALICE);
  bob = await core.login(B.http, origin, process.env.INVITE_BOB);
  assert.equal(alice.name, "alice");
  assert.equal(bob.name, "bob");
  assert.deepEqual(await core.whoAmI(A.http, origin), alice);
});

const ca = core.connect(ACP, A.http, origin, alice);
const cb = core.connect(ACP, B.http, origin, bob);

await check("each tab acts as its own person (identity comes from the login, not the page)", async () => {
  assert.equal((await ca.whoami()).agent, "alice");
  assert.equal((await cb.whoami()).agent, "bob");
});

await check("chat: a message from one tab reaches the other, signed by its sender", async () => {
  const got = [];
  const chatB = core.openChat(ACP, cb, bob, (m) => got.push(m));
  const chatA = core.openChat(ACP, ca, alice, () => {});
  await new Promise((r) => setTimeout(r, 300)); // both streams open
  await chatA.say("hello from alice");
  await until("bob's chat", () => got.some((m) => m.text === "hello from alice"));
  assert.equal(got.find((m) => m.text === "hello from alice").from, "alice");
  chatA.stop();
  chatB.stop();
});

await check("mail: alice writes to bob; bob's page is told without polling", async () => {
  let inbox = [];
  const stop = core.watchMail(ACP, cb, bob, (msgs) => (inbox = msgs));
  await new Promise((r) => setTimeout(r, 300));
  await core.sendMail(ca, "bob", "lunch?", "at noon");
  await until("bob's inbox", () => inbox.some((m) => m.subject === "lunch?"));
  assert.equal(inbox.find((m) => m.subject === "lunch?").from, "alice");
  stop();
});

let notesA;
let notesB;
await check("notes: two tabs type into one live document and converge", async () => {
  notesA = await core.openNotes(ACP, ca, () => {}, () => {});
  notesB = await core.openNotes(ACP, cb, () => {}, () => {});
  notesA.setText("Plan\n");
  await notesA.flush();
  await until("bob sees alice's text", () => notesB.text() === "Plan\n");
  notesA.setText("# Plan\n");
  notesB.setText("Plan\n- ship it\n");
  await Promise.all([notesA.flush(), notesB.flush()]);
  await until("both tabs converge", () => notesA.text() === notesB.text() && notesA.text() === "# Plan\n- ship it\n");
});

await check("notes: a command-line agent edits the same document and both tabs see it", async () => {
  const bin = process.env.ACP_BIN_DIR;
  assert.ok(bin && process.env.AGENT_TOKEN, "run.sh passes the agent's login");
  const file = path.join(work, "agent-notes.md");
  const env = { ...process.env, ACP_TOKEN: process.env.AGENT_TOKEN, ACP_AGENT: "reviewer", ACP_SPACE: "team", ACP_HOME: path.join(work, ".acp-agent") };
  const acp = (verb) => spawnSync(path.join(bin, "acp"), ["crdt", verb, core.NOTES_DOC, file], { cwd: work, env, encoding: "utf8" });
  let r = acp("pull");
  assert.equal(r.status, 0, r.stderr);
  assert.equal(readFileSync(file, "utf8"), "# Plan\n- ship it\n", "the agent got the tabs' text");
  writeFileSync(file, "# Plan\n- ship it\n- reviewed by the agent\n");
  r = acp("sync");
  assert.equal(r.status, 0, r.stderr);
  await until("the tabs see the agent's line", () => notesA.text().includes("reviewed by the agent") && notesB.text() === notesA.text());
});

await check("board: two tabs add tasks at the same moment; both tasks are kept", async () => {
  const bA = await core.openBoard(ACP, ca, () => {});
  const bB = await core.openBoard(ACP, cb, () => {});
  await Promise.all([bA.add("write tests"), bB.add("fix the bug")]);
  await until("both boards hold both tasks", () => bA.items().length === 2 && bB.items().length === 2);
  assert.deepEqual([...bA.items()].sort(), ["fix the bug", "write tests"]);
  bA.session.close();
  bB.session.close();
});

await check("files: a file one tab saves shows up in the other; unsaved until the save is acknowledged", async () => {
  const seen = [];
  const fa = core.openFiles(ACP, ca, "backend.local", alice, () => {}, () => {});
  const fb = core.openFiles(ACP, cb, "backend.local", bob, (ev) => seen.push(ev), () => {});
  await fb.readdir();
  await fa.writeFile("plan.txt", "v1");
  assert.equal(core.unsaved(fa), true, "a written-but-not-flushed file is unsaved");
  await fa.flush();
  assert.equal(core.unsaved(fa), false);
  await until("bob's mount hears of plan.txt", () => seen.some((e) => e.path === "plan.txt"));
  assert.equal(await fb.readTextFile("plan.txt"), "v1");
  await fa.close();
  await fb.close();
});

await check("files: a teammate's newer save is NEVER overwritten — the late save fails and keeps its bytes", async () => {
  const fa = core.openFiles(ACP, ca, "backend.local", alice, () => {}, () => {});
  const fb = core.openFiles(ACP, cb, "backend.local", bob, () => {}, () => {});
  assert.equal(await fa.readTextFile("plan.txt"), "v1");
  assert.equal(await fb.readTextFile("plan.txt"), "v1");
  await fa.writeFile("plan.txt", "v2 from alice");
  await fa.flush();
  await fb.writeFile("plan.txt", "v2 from bob");
  const err = await fb.flush().then(() => null, (e) => e);
  assert.ok(err && err.code === "ECONFLICT", `bob's save must be refused, got ${err}`);
  assert.equal(new TextDecoder().decode(err.conflicts[0].ours.bytes), "v2 from bob", "bob's bytes are on the error, not lost");
  const fresh = core.openFiles(ACP, cb, "backend.local", bob, () => {}, () => {});
  assert.equal(await fresh.readTextFile("plan.txt"), "v2 from alice", "alice's save stands");
  await fa.close();
  await fresh.close();
});

await check("presence: alice says she is here; bob sees her (plain HTTP, no WebSocket)", async () => {
  await core.announce(ca, alice, { page: "workspace" });
  const snap = await cb.awareness();
  assert.ok(snap.entries.some((e) => e.actor === "alice"), JSON.stringify(snap.entries));
});

await check("the page cannot reach admin routes through the backend", async () => {
  const err = await ca.compact("").then(() => null, (e) => e);
  assert.ok(err && err.status === 404, `compact must be refused by the backend, got ${err?.status}`);
});

await check("no token ever left a tab: no request the page made carried an Authorization header", async () => {
  for (const t of [A, B]) {
    assert.ok(t.sent.length > 10);
    for (const s of t.sent) assert.equal(Object.keys(s.headers).find((h) => h.toLowerCase() === "authorization"), undefined, `${s.url} sent Authorization`);
  }
});

await check("closing: every document saved, nothing left unsaved", async () => {
  await notesA.close();
  await notesB.close();
  assert.equal(core.unsaved(notesA, notesB), false);
});

console.log(failed === 0 ? "JOURNEY done: all ok" : `JOURNEY done: ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
