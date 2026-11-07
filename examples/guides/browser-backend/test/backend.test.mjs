// Named tests for the browser backend (DESIGN W12): the route allowlist, and what happens to the credential —
// the person's own login replaces whatever the page sent, the workspace is forced, the agent header is dropped,
// admin routes never reach coordd, a revoked person is cut off, a shared-key coordd and an unverified
// certificate are refused, and event streams pass through live.
//
// Fail-safe: SCRATCH coordd daemons only (temp data folders, 127.0.0.1, free ports, torn down after); never a
// real server or ~/.acp. Skipped when no coordd binary is found (ACP_COORDD, $BINDIR, or the repo's dist/).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { randomBytes } from "node:crypto";
import https from "node:https";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createBackend, decodeInvite, routeAllowed } from "../backend.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "..", "..", "..", "..");

function coorddBin() {
  for (const c of [process.env.ACP_COORDD, process.env.BINDIR && path.join(process.env.BINDIR, "coordd"), path.join(repo, "dist", `${process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`, "coordd")]) {
    if (c && existsSync(c)) return c;
  }
  return null;
}
const BIN = coorddBin();

const freePort = () =>
  new Promise((r) => {
    const s = net.createServer();
    s.listen(0, "127.0.0.1", () => {
      const p = s.address().port;
      s.close(() => r(p));
    });
  });

/** Raw HTTPS JSON call to a scratch coordd (the test's own admin hand; the backend never sees these). */
function api(d, method, p, token, body, space) {
  return new Promise((resolve, reject) => {
    const headers = { Authorization: `Bearer ${token}`, ...(space ? { "X-ACP-Space": space } : {}) };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const r = https.request(d.base + p, { method, headers, ca: d.ca }, (res) => {
      let s = "";
      res.on("data", (c) => (s += c));
      res.on("end", () => resolve({ status: res.statusCode, body: s ? JSON.parse(s) : null }));
    });
    r.on("error", reject);
    r.end(body === undefined ? undefined : JSON.stringify(body));
  });
}

/** Starts a scratch coordd: per-agent (an admin login minted first) unless `shared`. */
async function scratch({ shared = false } = {}) {
  const data = mkdtempSync(path.join(os.tmpdir(), "acp-bb-test-"));
  let admin = "";
  if (shared) writeFileSync(path.join(data, "token"), randomBytes(24).toString("hex") + "\n", { mode: 0o600 });
  else {
    const out = spawnSync(BIN, ["-data", data, "-mkagent", "admin", "-role", "admin"], { encoding: "utf8" });
    admin = /token: ([0-9a-f]+)/.exec(out.stdout + out.stderr)?.[1] ?? "";
    assert.ok(admin, "minted an admin login");
  }
  for (let attempt = 0; attempt < 5; attempt++) {
    const port = await freePort();
    const child = spawn(BIN, ["-addr", `127.0.0.1:${port}`, "-data", data, ...(shared ? ["-shared-token-role", "writer"] : [])], { stdio: "ignore" });
    const d = { base: `https://127.0.0.1:${port}`, data, child, ca: null, admin };
    for (let i = 0; i < 80 && child.exitCode === null; i++) {
      await new Promise((r) => setTimeout(r, 250));
      if (!existsSync(path.join(data, "cert.pem"))) continue;
      d.ca = readFileSync(path.join(data, "cert.pem"));
      try {
        const h = await api(d, "GET", "/v1/healthz", "x");
        if (h.status === 200) {
          if (shared) d.admin = readFileSync(path.join(data, "token"), "utf8").trim();
          return d;
        }
      } catch {
        /* not up yet */
      }
    }
    child.kill("SIGKILL");
  }
  throw new Error("scratch coordd would not start");
}
async function stop(d) {
  if (!d) return;
  d.child.kill("SIGTERM");
  await new Promise((r) => (d.child.exitCode !== null ? r() : d.child.on("exit", r)));
  rmSync(d.data, { recursive: true, force: true });
}

/** An invite code for `name` in `space`, made the way `acp invite` makes one. */
async function invite(d, space, name) {
  const r = await api(d, "POST", "/v1/admin/invites", d.admin, { space, name, role: "writer" });
  assert.equal(r.status, 201, JSON.stringify(r.body));
  const code = "acpj1_" + Buffer.from(JSON.stringify({ s: [d.base], w: space, t: "sha256:" + "0".repeat(64), i: r.body.invite.id, k: r.body.secret, n: name })).toString("base64url");
  return { code, id: r.body.invite.id };
}

let d = null; // the per-agent scratch daemon
let be = null; // the backend under test
let base = ""; // the backend's URL
const forwarded = []; // [method, path, person] the backend sent to coordd
before(async () => {
  if (!BIN) return;
  d = await scratch();
  be = createBackend({ upstream: d.base, ca: d.ca, onForward: (m, p, who) => forwarded.push([m, p, who]), log: () => {} });
  const a = await be.listen(0);
  base = `http://127.0.0.1:${a.port}`;
});
after(async () => {
  await be?.close();
  await stop(d);
});
const skip = (t) => (BIN ? false : (t.skip("no coordd binary (set ACP_COORDD)"), true));

/** A "tab": logs in through the backend and keeps its cookie. */
async function tab(name, space = "team") {
  const inv = await invite(d, space, name);
  const r = await fetch(base + "/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ invite: inv.code }) });
  const body = await r.json();
  assert.equal(r.status, 200, JSON.stringify(body));
  const cookie = /acp_session=[^;]+/.exec(r.headers.get("set-cookie") ?? "")?.[0];
  assert.ok(cookie, "a session cookie was set");
  const call = (p, init = {}) => fetch(base + p, { ...init, headers: { ...(init.headers ?? {}), Cookie: cookie } });
  return { name, inv, cookie, body, setCookie: r.headers.get("set-cookie"), call };
}

// ---------------------------------------------------------------- pure

test("routeAllowed: admin, invite, unknown and traversal paths are never forwarded; the page's routes are", () => {
  for (const [m, p] of [
    ["GET", "/v1/admin/grants"], ["POST", "/v1/admin/invites"], ["DELETE", "/v1/admin/grants"], ["POST", "/v1/invites/redeem"],
    ["GET", "/v1/admin/compact"], ["POST", "/v1/admin/fs/mode"], ["GET", "/v1/mail/../admin/grants"], ["GET", "/v1//admin"],
    ["GET", "/v1/blobs/../admin"], ["GET", "/v1/blobs/%2e%2e%2fadmin"], ["GET", "/v1/blobs/" + "A".repeat(64)], ["DELETE", "/v1/mail"],
    ["PUT", "/v1/space/config"], ["GET", "/v1/cap/blob/x"], ["GET", ""], ["GET", "/"],
  ]) assert.equal(routeAllowed(m, p), false, `${m} ${p} must not be forwarded`);
  for (const [m, p] of [
    ["GET", "/v1/events"], ["POST", "/v1/events"], ["GET", "/v1/mail"], ["POST", "/v1/crdt/ops"], ["GET", "/v1/crdt/json/doc"],
    ["POST", "/v1/commit"], ["GET", "/v1/blobs/" + "a".repeat(64)], ["POST", "/v1/fs/ops"], ["GET", "/v1/whoami"], ["GET", "/v1/stats"],
  ]) assert.equal(routeAllowed(m, p), true, `${m} ${p} is a page route`);
});

test("decodeInvite: a malformed or truncated code is refused before anything is sent", () => {
  for (const bad of ["", "hello", "acpj1_", "acpj1_!!!", "acpj2_" + Buffer.from("{}").toString("base64url"), "acpj1_" + Buffer.from('{"i":"x","k":"y","w":"t"}').toString("base64url")]) {
    assert.throws(() => decodeInvite(bad), `${bad} must be refused`);
  }
  const ok = decodeInvite("acpj1_" + Buffer.from(JSON.stringify({ s: ["https://h"], w: "team", t: "webpki", i: "a".repeat(32), k: "b".repeat(22), n: "zoe" })).toString("base64url"));
  assert.deepEqual(ok, { id: "a".repeat(32), secret: "b".repeat(22), space: "team", name: "zoe" });
});

test("an http:// coordd address is refused (the token would cross the network in clear)", () => {
  assert.throws(() => createBackend({ upstream: "http://127.0.0.1:8443" }), /https/);
});

// ---------------------------------------------------------------- against a scratch coordd

test("a coordd certificate the backend cannot verify is refused at start (no switch turns verification off)", async (t) => {
  if (skip(t)) return;
  const b = createBackend({ upstream: d.base, log: () => {} }); // no CA: the scratch daemon's self-signed cert is untrusted
  await assert.rejects(b.listen(0), /self.signed|certificate|verify/i);
  await b.close();
});

test("login by invite: the person gets their OWN ACP login, kept server-side — the browser gets only an HttpOnly session cookie", async (t) => {
  if (skip(t)) return;
  const alice = await tab("alice");
  assert.deepEqual(alice.body, { name: "alice", space: "team", role: "writer" });
  assert.doesNotMatch(JSON.stringify(alice.body), /token/i, "no token in the login answer");
  assert.match(alice.setCookie, /HttpOnly/);
  assert.match(alice.setCookie, /SameSite=Strict/);
  assert.match(alice.setCookie, /Secure/);
  const me = await (await alice.call("/me")).json();
  assert.deepEqual(me, { name: "alice", space: "team", role: "writer" });
  // The same invite cannot be used twice.
  const again = await fetch(base + "/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ invite: alice.inv.code }) });
  assert.equal(again.status, 410);
});

test("the page's Authorization and X-ACP-Agent are REPLACED: even an admin token sent by the page acts as the logged-in person", async (t) => {
  if (skip(t)) return;
  const bob = await tab("bob");
  const r = await bob.call("/acp/v1/whoami", { headers: { Authorization: `Bearer ${d.admin}`, "X-ACP-Agent": "admin" } });
  const who = await r.json();
  assert.equal(who.agent, "bob");
  assert.equal(who.role, "writer");
  // An event the page posts is stamped with the PERSON's name, whatever agent header it sent.
  const ev = await bob.call("/acp/v1/events", { method: "POST", headers: { "Content-Type": "application/json", "X-ACP-Agent": "alice" }, body: JSON.stringify({ action: "note.test", entity: "who-am-i" }) });
  assert.equal(ev.status, 200, await ev.clone().text());
  assert.equal((await ev.json()).actor, "bob");
});

test("the workspace is FORCED to the person's own: a page asking for another workspace still lands in its own", async (t) => {
  if (skip(t)) return;
  const carol = await tab("carol");
  const r = await carol.call("/acp/v1/events", { method: "POST", headers: { "Content-Type": "application/json", "X-ACP-Space": "elsewhere" }, body: JSON.stringify({ action: "note.where", entity: "space-test" }) });
  assert.equal(r.status, 200);
  const team = await api(d, "GET", "/v1/events?from=1", d.admin, undefined, "team");
  const elsewhere = await api(d, "GET", "/v1/events?from=1", d.admin, undefined, "elsewhere");
  assert.ok(team.body.some((e) => e.action === "note.where" && e.actor === "carol"), "landed in carol's workspace");
  assert.equal(elsewhere.body?.code, "space_not_found", "never in the other workspace: it was not even created");
});

test("admin and invite routes are refused by the backend itself and never reach coordd", async (t) => {
  if (skip(t)) return;
  const dave = await tab("dave");
  const before = forwarded.length;
  for (const [m, p] of [["GET", "/acp/v1/admin/grants"], ["POST", "/acp/v1/admin/invites"], ["POST", "/acp/v1/invites/redeem"], ["GET", "/acp/v1/admin/../admin/grants"], ["PUT", "/acp/v1/space/config"]]) {
    const r = await dave.call(p, { method: m, headers: { "Content-Type": "application/json" }, body: m === "GET" ? undefined : "{}" });
    assert.equal(r.status, 404, `${m} ${p}`);
  }
  assert.equal(forwarded.length, before, `nothing forwarded: ${JSON.stringify(forwarded.slice(before))}`);
  assert.ok(!forwarded.some(([, p]) => p.startsWith("/v1/admin") || p.startsWith("/v1/invites")));
});

test("no session, or a forged one, gets 401 and nothing is forwarded; a cross-site write is refused", async (t) => {
  if (skip(t)) return;
  const before = forwarded.length;
  assert.equal((await fetch(base + "/acp/v1/whoami")).status, 401);
  assert.equal((await fetch(base + "/acp/v1/whoami", { headers: { Cookie: "acp_session=" + randomBytes(32).toString("base64url") } })).status, 401);
  assert.equal((await fetch(base + "/me")).status, 401);
  assert.equal(forwarded.length, before);
  const erin = await tab("erin");
  const xs = await erin.call("/acp/v1/events", { method: "POST", headers: { "Content-Type": "text/plain", Origin: "https://evil.example" }, body: JSON.stringify({ action: "note.csrf" }) });
  assert.equal(xs.status, 403);
  const xs2 = await erin.call("/acp/v1/events", { method: "POST", headers: { "Content-Type": "application/json", "Sec-Fetch-Site": "cross-site" }, body: JSON.stringify({ action: "note.csrf" }) });
  assert.equal(xs2.status, 403);
  const lx = await fetch(base + "/login", { method: "POST", headers: { "Content-Type": "application/json", Origin: "https://evil.example" }, body: "{}" });
  assert.equal(lx.status, 403);
  const lt = await fetch(base + "/login", { method: "POST", headers: { "Content-Type": "text/plain" }, body: "{}" });
  assert.equal(lt.status, 415, "a login must be JSON (a plain form post cannot make one)");
});

test("an event stream passes through LIVE (unbuffered): an event appended after the stream opened arrives while it stays open", async (t) => {
  if (skip(t)) return;
  const fay = await tab("fay");
  const stats = await (await fay.call("/acp/v1/stats")).json();
  const ac = new AbortController();
  const r = await fay.call(`/acp/v1/events?follow=true&from=${stats.events + 1}&actions=note.live`, { signal: ac.signal });
  assert.equal(r.status, 200);
  assert.match(r.headers.get("content-type") ?? "", /ndjson/);
  const reader = r.body.getReader();
  const got = (async () => {
    let buf = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return null;
      buf += new TextDecoder().decode(value);
      const line = buf.split("\n").find((l) => l.includes("note.live"));
      if (line) return JSON.parse(line);
    }
  })();
  await new Promise((res) => setTimeout(res, 300)); // the stream is open and idle
  const post = await api(d, "POST", "/v1/events", d.admin, { action: "note.live", entity: "x" }, "team");
  assert.equal(post.status, 200);
  const e = await Promise.race([got, new Promise((_, rej) => setTimeout(() => rej(new Error("the event did not arrive through the backend within 5 s")), 5000))]);
  assert.equal(e.action, "note.live");
  ac.abort();
});

test("a person whose login is revoked is cut off: coordd's 401 ends every session of that person in the backend", async (t) => {
  if (skip(t)) return;
  const gus = await tab("gus");
  assert.equal((await gus.call("/acp/v1/whoami")).status, 200);
  const rm = await api(d, "DELETE", `/v1/admin/invites/${gus.inv.id}/member?name=gus`, d.admin);
  assert.equal(rm.status, 200, JSON.stringify(rm.body));
  assert.equal((await gus.call("/acp/v1/whoami")).status, 401);
  assert.equal((await gus.call("/me")).status, 401, "the backend signed the person out");
  assert.equal(be.people.has("gus"), false, "the dead login is not kept");
});

test("a coordd running with ONE SHARED KEY is refused (identity there is a header anyone can set)", async (t) => {
  if (skip(t)) return;
  const sd = await scratch({ shared: true });
  try {
    const b = createBackend({ upstream: sd.base, ca: sd.ca, log: () => {} });
    await assert.rejects(b.checkLogin(sd.admin, "default"), /shared key/);
    await b.close();
  } finally {
    await stop(sd);
  }
});
