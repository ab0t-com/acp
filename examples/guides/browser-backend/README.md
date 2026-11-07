# browser-backend — your team's workspace in a web page, with no token in the page

**What:** a small backend that lets people use an ACP workspace from a browser: shared notes they type
into together, a task board, chat, mail, shared files and "who's here" — next to the agents working in
the same workspace. The page never holds an ACP token.

**Why:** a web page is the easiest way to bring people into an agents' workspace, but a page is the
worst place for a long-lived credential: any script on it, any browser extension and any injected code
can read it. Here the page only has a session cookie the browser keeps to itself (`HttpOnly`); the
person's ACP login lives in the backend, and the backend decides which ACP routes a page may use.

**Who:** anyone building a web front end on ACP — a team dashboard, a review page, a chat room where
people and agents meet.

## Run it

```bash
cd examples/guides/browser-backend
./run.sh            # self-checking: scratch coordd, the backend, two "tabs" + an agent, the backend's tests
```

It needs Node 20+, `acp` and `coordd` (on your `PATH`, or `ACP_BIN=<folder holding both>`), and the
TypeScript SDK's browser build: `npm install @ab0t/acp` in this folder (`run.sh` does it if it is missing).
This folder stands alone: copy it anywhere and it runs. Everything runs on `127.0.0.1` against a throwaway
server and is removed afterwards.

To click through it yourself against a server you run:

```bash
npm install @ab0t/acp
node backend.mjs --upstream https://127.0.0.1:8443 --ca <coordd-data>/cert.pem --port 8080
acp invite --name alice --space team     # an admin makes an invite; paste its acpj1_… code into the page
# open http://127.0.0.1:8080
```

## How it works

```
browser page ──(same site; session cookie)──▶ backend.mjs ──(the person's own ACP login; TLS verified)──▶ coordd
```

1. **Log in with an invite.** The person pastes the invite code an admin made (`acp invite`). The
   backend redeems it at its own coordd — the redemption creates the person's **own** ACP login —
   checks that coordd names people by their login (it refuses a server running with one shared key)
   and that the login is not an admin, keeps the token, and answers with a session cookie
   (`HttpOnly; Secure; SameSite=Strict`). The answer never contains the token.
2. **Forward only what a page needs.** Requests to `/acp/v1/…` are forwarded only if the route is on
   the allowlist in `backend.mjs` (events, mail, documents, files, presence, leases). Admin routes and
   the invite routes are refused by the backend and never reach coordd. On the way it **replaces**
   `Authorization` with the person's token, **sets** `X-ACP-Space` to the person's workspace and
   **drops** `X-ACP-Agent` and every other header the page sent. Writes from another site are refused.
3. **Live streams pass straight through**, unbuffered, and close upstream when the page goes away.
4. **A revoked person is signed out**: when coordd answers 401 for their login, the backend drops it
   and every session of that person.
5. **The page** (`public/`) uses the SDK's browser build with **no token**:
   `new Client({ baseUrl: location.origin + "/acp" })`. All its ACP code is in `public/page-core.js`
   (no DOM): `TextDocSession` for the notes, `JSONDocSession` for the board, a room on the event log for
   chat (the shared [room-message shape](../../../docs/ROOM-MESSAGES.md)), mail, `mountVirtualFS` with
   `conflict: "throw"` for files, presence over plain HTTP. It shows "unsaved changes" and asks before
   the tab closes while anything is not yet saved.

## What the test proves

`run.sh` runs `journey.mjs`: two "tabs" download the page's code **from the backend** (the SDK bundle
and `page-core.js`, exactly what a browser loads) and, each with its own cookie:

- log in with their invites and act as themselves (coordd records `alice` and `bob`, not the backend);
- chat and mail each other, live, without polling;
- type into the same notes at the same moment and converge — and a command-line agent (`acp crdt
  sync`) edits the same notes, which both tabs see;
- add tasks to the board at the same moment, both kept;
- save files; a teammate's newer save is never overwritten (the late save fails and keeps its bytes);
- never send an `Authorization` header, and cannot reach admin routes.

Then it runs `test/backend.test.mjs` (12 named tests on scratch servers of their own): the allowlist;
malformed invite codes; `http://` and unverifiable certificates refused; the token never reaching the
browser; the page's `Authorization`/`X-ACP-Agent`/`X-ACP-Space` replaced; admin routes never forwarded;
no-session, forged-session and cross-site requests refused; live stream pass-through; a revoked person
cut off; a shared-key server refused.

The page itself (`index.html` + `app.js`) is checked to parse and to carry no token; it is not driven in
a real browser by this script.

## Making it yours

- **Your own login.** Replace (or front) the invite login with your app's sign-in (SSO). Keep one ACP
  login per person: their name in the log, their own rate budget, and a stolen session worth one person.
- **Store the logins** in your secret store; this example keeps them in memory (a restart signs
  everyone out, and a used invite cannot be used again).
- **Put HTTPS in front** with a name browsers trust (see "Browser front door" in
  [`docs/OPERATIONS.md`](../../../docs/OPERATIONS.md)), and keep the backend on the same site as the page.
- **Narrow the allowlist** to the routes your page uses.
- Read [`docs/BROWSER.md`](../../../docs/BROWSER.md) for the rules that keep people's work safe in a
  page (nothing is saved until the server says so, documents for things people type together, …).
