#!/usr/bin/env node
// browser-backend — the small backend that lets a web page use ACP without ever holding a token.
//
//   browser page ──(same site, a session cookie)──▶ this backend ──(the person's own ACP login)──▶ coordd
//
// What it does, and why each rule exists (docs/BROWSER.md has the long form):
//   1. People log in with an ACP INVITE CODE, redeemed HERE (POST /login). The redemption makes the person's own
//      ACP login; the backend keeps that token server-side and gives the browser only an HttpOnly session cookie.
//      One login per person = their own name in the log, their own rate budget, their own scope.
//   2. It forwards an ALLOWLIST of ACP routes (/acp/v1/...) and nothing else: never /v1/admin/*, never the invite
//      endpoints. It REPLACES Authorization with the person's token, SETS X-ACP-Space to the person's workspace,
//      and DROPS X-ACP-Agent and every other header the page sent.
//   3. Event streams pass through unbuffered (a page follows live events through it).
//   4. It verifies coordd's TLS certificate (a CA file, or the system store) — there is no switch to turn that off.
//   5. It refuses a coordd that runs with one shared key (identity there is a header anyone can set).
//
// No dependencies (Node >= 20). Run:  node backend.mjs --upstream https://127.0.0.1:8443 --ca <data>/cert.pem
// This is a reference example: a production backend keeps the per-person tokens in its secret store, sits behind
// HTTPS, and uses its own user login (SSO) instead of — or in addition to — invite codes.

import http from "node:http";
import https from "node:https";
import { randomBytes } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

/** The ACP routes a page may use, by method. Exact paths, except blobs (a content hash). Nothing else is forwarded. */
const ROUTES = {
  GET: [
    "/v1/healthz", "/v1/whoami", "/v1/stats", "/v1/events", "/v1/channels", "/v1/mail", "/v1/mail/thread",
    "/v1/crdt/ops", "/v1/crdt/doc", "/v1/crdt/list", "/v1/crdt/json/ops", "/v1/crdt/json/doc", "/v1/crdt/json/list",
    "/v1/manifest", "/v1/space/config", "/v1/fs/tree", "/v1/fs/trash", "/v1/history", "/v1/awareness", "/v1/agents",
    "/v1/lease",
  ],
  POST: [
    "/v1/events", "/v1/mail", "/v1/mail/ack", "/v1/crdt/ops", "/v1/crdt/json/ops", "/v1/blobs", "/v1/blobs/has",
    "/v1/commit", "/v1/fs/ops", "/v1/awareness", "/v1/agents/beat", "/v1/lease/acquire", "/v1/lease/renew",
    "/v1/lease/release",
  ],
};
const BLOB_PATH = /^\/v1\/blobs\/[0-9a-f]{64}$/;

/** True when a page may send `method path` (path WITHOUT the /acp prefix and without the query). */
export function routeAllowed(method, p) {
  if (typeof p !== "string" || p.length > 256) return false;
  if (method === "GET" && BLOB_PATH.test(p)) return true;
  return (ROUTES[method] ?? []).includes(p);
}

/** Decodes an "acpj1_" invite code (only the fields the backend needs). Throws on anything malformed. */
export function decodeInvite(code) {
  const s = String(code ?? "").replace(/\s+/g, "");
  if (!s.startsWith("acpj1_") || s.length > 4096) throw new Error('not an invite code (it starts with "acpj1_")');
  let obj;
  try {
    obj = JSON.parse(Buffer.from(s.slice(6).replace(/=+$/, ""), "base64url").toString("utf8"));
  } catch {
    throw new Error("the invite code is incomplete or was changed");
  }
  if (!obj || typeof obj.i !== "string" || !/^[0-9a-f]{32}$/.test(obj.i) || typeof obj.k !== "string" || !/^[A-Za-z0-9_-]{20,90}$/.test(obj.k) || typeof obj.w !== "string") {
    throw new Error("the invite code is incomplete or was changed");
  }
  return { id: obj.i, secret: obj.k, space: obj.w, name: typeof obj.n === "string" ? obj.n : "" };
}

const SESSION_COOKIE = "acp_session";
const HEADER_OK = { "x-acp-protocol": /^[a-z0-9/._-]{1,32}$/i, "x-acp-session": /^[A-Za-z0-9._-]{1,128}$/, "idempotency-key": /^[\x21-\x7e]{1,256}$/ };
const PASS_BACK = /^(content-type|content-length|retry-after|x-acp-[a-z0-9-]+)$/;

function json(res, status, body, extra = {}) {
  const b = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store", ...extra });
  res.end(b);
}

function readBody(req, max) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let n = 0;
    req.on("data", (c) => {
      n += c.length;
      if (n > max) {
        reject(new Error("body too large"));
        req.destroy();
      } else chunks.push(c);
    });
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

function cookieOf(req, name) {
  for (const part of (req.headers.cookie ?? "").split(";")) {
    const [k, ...v] = part.trim().split("=");
    if (k === name) return v.join("=");
  }
  return "";
}

/**
 * Creates the backend. Options:
 *   upstream   coordd's base URL — https only (required)
 *   ca         PEM bytes of coordd's certificate/CA (default: the system store)
 *   sdkFile    path of the SDK browser bundle (acp.esm.js) served at /sdk/acp.esm.js
 *   onForward  (method, path, person) => void — called for every request forwarded (audit / tests)
 *   log        (line) => void (default: console.log)
 */
export function createBackend(opts) {
  const up = new URL(opts.upstream);
  if (up.protocol !== "https:") throw new Error(`upstream must be https:// (got ${up.protocol}) — coordd always serves TLS, and the token must never cross the network in clear`);
  const agent = new https.Agent({ keepAlive: true, ca: opts.ca, rejectUnauthorized: true });
  const log = opts.log ?? ((l) => console.log(l));
  const people = new Map(); // name -> { token, space, role }
  const sessions = new Map(); // session id -> name
  const sdkFile = opts.sdkFile ?? [path.resolve(here, "node_modules/@ab0t/acp/dist/browser/acp.esm.js"), path.resolve(here, "../../../sdk/ts/dist/browser/acp.esm.js")].find((f) => existsSync(f));
  if (!sdkFile) throw new Error("the SDK's browser build was not found: run `npm install @ab0t/acp` here, or pass sdkFile (--sdk)");
  const pub = path.join(here, "public");

  /** One request to coordd with the given headers; resolves with the upstream response (body unread). */
  function upstream(method, p, headers, body) {
    return new Promise((resolve, reject) => {
      const r = https.request(new URL(p, up), { method, headers, agent }, resolve);
      r.on("error", reject);
      if (body === undefined) r.end();
      else r.end(body);
    });
  }
  async function upstreamJSON(method, p, headers, body) {
    const r = await upstream(method, p, { ...headers, ...(body !== undefined ? { "Content-Type": "application/json" } : {}) }, body === undefined ? undefined : JSON.stringify(body));
    const raw = await readBody(r, 1 << 20);
    let v = null;
    try {
      v = raw.length ? JSON.parse(raw.toString("utf8")) : null;
    } catch {
      /* not JSON */
    }
    return { status: r.statusCode, body: v };
  }

  /**
   * Checks a freshly made login before keeping it: coordd must derive identity from the TOKEN (per-agent mode).
   * With one shared key it echoes whatever X-ACP-Agent says, so a random probe name comes back — refuse.
   * A person never gets an admin login through a page.
   */
  async function checkLogin(token, space) {
    const probe = `probe-${randomBytes(6).toString("hex")}`;
    const r = await upstreamJSON("GET", "/v1/whoami", { Authorization: `Bearer ${token}`, "X-ACP-Space": space, "X-ACP-Agent": probe });
    if (r.status !== 200 || !r.body) throw Object.assign(new Error(`coordd refused the new login (${r.status})`), { status: 502 });
    if (r.body.agent === probe) throw Object.assign(new Error("coordd runs with one shared key: identity there is a header anyone can set — run it with one login per agent"), { status: 502 });
    if (r.body.role === "admin") throw Object.assign(new Error("refusing an admin login for a web page"), { status: 403 });
    return r.body;
  }

  function personOf(req) {
    const sid = cookieOf(req, SESSION_COOKIE);
    const name = sid ? sessions.get(sid) : undefined;
    const p = name ? people.get(name) : undefined;
    return p ? { name, ...p } : null;
  }

  /** The browser's own requests must come from this site: a cross-site POST (a form, another page's script) is refused. */
  function sameSite(req) {
    const site = req.headers["sec-fetch-site"];
    if (site !== undefined && site !== "same-origin" && site !== "none") return false;
    const origin = req.headers.origin;
    if (origin === undefined) return true;
    try {
      return new URL(origin).host === req.headers.host;
    } catch {
      return false;
    }
  }

  async function login(req, res) {
    if (!/^application\/json\b/.test(req.headers["content-type"] ?? "")) return json(res, 415, { error: "send JSON" });
    let body;
    try {
      body = JSON.parse((await readBody(req, 16 << 10)).toString("utf8"));
    } catch {
      return json(res, 400, { error: "send JSON: {\"invite\": \"acpj1_…\", \"name\"?: \"…\"}" });
    }
    let inv;
    try {
      inv = decodeInvite(body?.invite);
    } catch (err) {
      return json(res, 400, { error: err.message });
    }
    const name = typeof body.name === "string" && body.name !== "" ? body.name : inv.name;
    // Redeemed at OUR coordd only — never at an address the code names (the backend never connects where a user points it).
    const r = await upstreamJSON("POST", "/v1/invites/redeem", { "X-Forwarded-For": req.socket.remoteAddress ?? "" }, {
      id: inv.id,
      secret: inv.secret,
      ...(name ? { name } : {}),
      redeem_key: randomBytes(16).toString("hex"),
      client: { harness: "browser-backend-example", version: "1" },
    });
    if (r.status !== 200 && r.status !== 201) return json(res, r.status, { error: r.body?.error ?? "the invite was not accepted", reason: r.body?.reason });
    const { token, space, role } = r.body;
    const who = r.body.name;
    try {
      await checkLogin(token, space);
    } catch (err) {
      log(`login refused for ${who}: ${err.message}`);
      return json(res, err.status ?? 502, { error: err.message });
    }
    people.set(who, { token, space, role });
    const sid = randomBytes(32).toString("base64url");
    sessions.set(sid, who);
    log(`login: ${who} (${role}) in workspace ${space}`);
    json(res, 200, { name: who, space, role }, {
      "Set-Cookie": `${SESSION_COOKIE}=${sid}; HttpOnly; Secure; SameSite=Strict; Path=/`,
    });
  }

  function dropPerson(name) {
    people.delete(name);
    for (const [sid, n] of sessions) if (n === name) sessions.delete(sid);
  }

  async function forward(req, res, url) {
    const p = url.pathname.slice("/acp".length);
    if (!routeAllowed(req.method, p)) return json(res, 404, { error: "not available to pages" });
    const person = personOf(req);
    if (!person) return json(res, 401, { error: "log in first" });
    if (req.method !== "GET" && !sameSite(req)) return json(res, 403, { error: "cross-site request refused" });
    const headers = {
      Authorization: `Bearer ${person.token}`,
      "X-ACP-Space": person.space,
      "X-Forwarded-For": req.socket.remoteAddress ?? "",
    };
    for (const [h, re] of Object.entries(HEADER_OK)) {
      const v = req.headers[h];
      if (typeof v === "string" && re.test(v)) headers[h] = v;
    }
    if (req.headers["content-type"]) headers["Content-Type"] = req.headers["content-type"];
    if (req.headers["content-length"]) headers["Content-Length"] = req.headers["content-length"];
    opts.onForward?.(req.method, p, person.name);
    const r = https.request(new URL(p + url.search, up), { method: req.method, headers, agent }, (ur) => {
      if (ur.statusCode === 401) {
        // coordd no longer knows this login (revoked): end every session of this person.
        dropPerson(person.name);
        log(`login of ${person.name} no longer valid upstream: signed out`);
      }
      const out = { "Cache-Control": "no-store" };
      for (const [k, v] of Object.entries(ur.headers)) if (PASS_BACK.test(k) && v !== undefined) out[k] = v;
      res.writeHead(ur.statusCode ?? 502, out);
      res.flushHeaders(); // a follow stream starts now, not when its first event arrives
      ur.pipe(res);
    });
    r.on("error", (err) => {
      if (!res.headersSent) json(res, 502, { error: `coordd unreachable: ${err.message}` });
      else res.destroy();
    });
    // The page went away (closed tab, aborted fetch): end the upstream request too, so no stream outlives it.
    res.on("close", () => r.destroy());
    req.pipe(r);
  }

  function serveFile(res, file, type) {
    let b;
    try {
      b = readFileSync(file);
    } catch {
      return json(res, 404, { error: "not found" });
    }
    res.writeHead(200, {
      "Content-Type": type,
      "Cache-Control": "no-cache",
      "Content-Security-Policy": "default-src 'self'; script-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
      "Referrer-Policy": "no-referrer",
      "X-Content-Type-Options": "nosniff",
    });
    res.end(b);
  }

  const server = http.createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://backend.invalid");
    const route = async () => {
      if (url.pathname.startsWith("/acp/")) return forward(req, res, url);
      if (url.pathname === "/login" && req.method === "POST") {
        if (!sameSite(req)) return json(res, 403, { error: "cross-site request refused" });
        return login(req, res);
      }
      if (url.pathname === "/logout" && req.method === "POST") {
        if (!sameSite(req)) return json(res, 403, { error: "cross-site request refused" });
        sessions.delete(cookieOf(req, SESSION_COOKIE));
        return json(res, 200, { ok: true }, { "Set-Cookie": `${SESSION_COOKIE}=; HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=0` });
      }
      if (url.pathname === "/me" && req.method === "GET") {
        const p = personOf(req);
        return p ? json(res, 200, { name: p.name, space: p.space, role: p.role }) : json(res, 401, { error: "log in first" });
      }
      if (req.method !== "GET") return json(res, 405, { error: "method not allowed" });
      if (url.pathname === "/") return serveFile(res, path.join(pub, "index.html"), "text/html; charset=utf-8");
      if (url.pathname === "/app.js") return serveFile(res, path.join(pub, "app.js"), "text/javascript; charset=utf-8");
      if (url.pathname === "/page-core.js") return serveFile(res, path.join(pub, "page-core.js"), "text/javascript; charset=utf-8");
      if (url.pathname === "/sdk/acp.esm.js") return serveFile(res, sdkFile, "text/javascript; charset=utf-8");
      return json(res, 404, { error: "not found" });
    };
    route().catch((err) => {
      log(`error: ${err.message}`);
      if (!res.headersSent) json(res, 500, { error: "backend error" });
      else res.destroy();
    });
  });

  return {
    server,
    people,
    checkLogin,
    /** Checks coordd is reachable over VERIFIED TLS, then listens. Resolves with the bound address. */
    async listen(port = 0, host = "127.0.0.1") {
      const h = await upstreamJSON("GET", "/v1/healthz", {});
      if (h.status !== 200) throw new Error(`coordd health check failed (${h.status})`);
      await new Promise((r) => server.listen(port, host, r));
      return server.address();
    },
    close() {
      agent.destroy();
      server.closeAllConnections?.();
      return new Promise((r) => server.close(() => r()));
    },
  };
}

// ---- command line ----
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const arg = (k, def) => {
    const i = args.indexOf(k);
    return i >= 0 ? args[i + 1] : def;
  };
  const upstreamURL = arg("--upstream", process.env.ACP_SERVER);
  if (!upstreamURL) {
    console.error("usage: node backend.mjs --upstream https://<coordd>:8443 [--ca <cert.pem>] [--sdk <acp.esm.js>] [--port 8080] [--host 127.0.0.1]");
    process.exit(2);
  }
  const caFile = arg("--ca", process.env.ACP_CERT);
  const sdkFile = arg("--sdk");
  const b = createBackend({ upstream: upstreamURL, ca: caFile ? readFileSync(caFile) : undefined, ...(sdkFile ? { sdkFile } : {}) });
  b.listen(Number(arg("--port", "8080")), arg("--host", "127.0.0.1")).then(
    (a) => console.log(`browser-backend listening on http://${a.address}:${a.port}  (coordd: ${upstreamURL})`),
    (err) => {
      console.error(`browser-backend: ${err.message}`);
      process.exit(1);
    },
  );
}
