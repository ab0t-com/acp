# acp:// addresses: a link to your shared folder

An `acp://` address is a link to a place in your team's shared workspace: the whole workspace, a folder
in it, or one file. It works like a web link, but it points into the shared filesystem that you and your
agents use.

```
acp://coord.example/team/docs/plan.md
      └─ server ──┘ └space┘ └─ file ──┘
```

Underneath it is plain `https://`. The address just gives people and agents one short thing to paste into
a chat, an email, a ticket or a prompt, so everyone means the same file on the same server.

You can use an address to:

- **point at something** ("the plan is at `acp://coord.example/team/docs/plan.md`");
- **connect a tool** to a workspace without spelling out the server and the workspace name separately;
- **share one file** with someone who has no login, using a signed link that expires.

> **Is it safe to paste?** An address says *where*, never *who*. It never contains a password or token
> (details at the end). Your own login stays on your machine. A *share link* is the one kind that lets
> a person read a file without a login, so treat it like a key (see [Share a file](#share-a-file)).

---

## What each part means

`acp://` **host[:port]** `/` **space** `/` **path** `?v=N` `#note`

| Part | Example | What it means |
|---|---|---|
| `host[:port]` | `coord.example` | The ACP server. The port is optional; the default is `8443`. |
| `space` | `team` | The workspace on that server. It is the first thing after the host, and it is a hard wall: spaces share nothing. |
| `path` | `docs/plan.md` | A file or folder inside the space. Leave it off to mean the whole space. |
| trailing `/` | `docs/` | The address names a folder, not a file. |
| `?v=N` | `?v=12` | "As it was at version 12" (a past version of the shared folder). Read-only. |
| `#note` | `#section-2` | Your own bookmark. It is never sent to the server. |

A few forms you will meet:

| Looks like | Called | Use it for |
|---|---|---|
| `acp://coord.example/team/` | a workspace address | connecting a tool or an agent to the space |
| `acp://coord.example/team/docs/plan.md` | a file address | pointing at a file; people still need their own login |
| `acp://coord.example/team/docs/plan.md?e=…&h=…&k=…&s=…&sp=team` | a share link | giving one file to someone without a login, until it expires |
| `acp://coord.example/` | a server address | asking the server what it offers (`acp uri discover`) |

Spaces are named with letters, digits, `_` and `-` (up to 64 characters). Paths cannot contain `..`, a
backslash, an empty segment (`//`) or an encoded `/` (`%2F`).

---

## Get your workspace's address

`acp status` prints it (so does `acp whoami`, and `acp join` when you first connect):

```bash
acp status
# server:     https://coord.example:8443   [from profile "me"]
# workspace:  team                          [from profile "me"]
# address:    acp://coord.example/team/
```

It is built from the server and the workspace: drop `https://`, and write the workspace after the host.

`acp uri canonical` tidies it up (it removes the default port, lower-cases the host) and checks it for
you:

```bash
acp uri canonical 'acp://Coord.Example:8443/team/docs/plan.md'
# acp://coord.example/team/docs/plan.md
```

Other ways you may already have one:

- **Someone sent it to you.** Use it as it is (see [Use an address](#use-an-address)).
- **You only know the server's name.** Ask the server what it offers (no login needed):
  `acp uri discover acp://coord.example/`. It prints the server's origin and which features it has.
  Many servers do not list their workspace names here, on purpose; ask whoever invited you, or run
  `acp spaces` once you are logged in.
- **You were invited.** `acp join <code>` saves a login for you. Your workspace is the one the invite named
  (`acp status` shows it).


---

## Use an address

### If you are an AI agent

Rule of thumb: **the address tells you where; your profile tells the server who you are.**

| You were handed | Do this |
|---|---|
| A workspace address `acp://host/space/` | List it: `acp ls acp://host/space/`. To run several commands there: `acp --uri acp://host/space/ who`, or `export ACP_URI=acp://host/space/` and then run any command normally. For the MCP bridge set `ACP_URI` in its environment. |
| A file address `acp://host/space/path/file` | Read it: `acp show acp://host/space/path/file`. Write it: `acp put acp://host/space/path/file ./file`. |
| A folder address `acp://host/space/docs/` | List it: `acp ls acp://host/space/docs/`. Bring it down: `acp pull acp://host/space/docs/ ./work`. |
| A share link (it has `?e=…&h=…&s=…`) | Read it with no login: `acp show '<link>' --out file`. |
| A bare server `acp://host/` | `acp uri discover acp://host/` to see what the server offers, then ask for the workspace name. |
| Something that does not parse | `acp uri parse '<address>'`: the error says what is wrong. |

Where does the login come from? `acp` looks for a **profile** whose server is the address's `host:port`. If
there is none, you get:

```
error: acp uri: no profile for https://coord.example:8443 — run `acp config set <profile> server=https://coord.example:8443 token_file=...`
```

Join the server first (`acp join <code>`) or set the profile, then try again. Do not paste a token into the
address; it will be refused.

Reading and writing with an address. Each command uses your login for that server, whichever is your default:

```bash
acp ls acp://coord.example/team/                         # what is in the shared folder
acp show acp://coord.example/team/docs/plan.md           # print one file
acp show 'acp://coord.example/team/docs/plan.md?v=12'    # as it was at version 12
acp put acp://coord.example/team/docs/notes.md ./notes.md   # add a file
acp pull acp://coord.example/team/ ./work                # bring the folder down
acp pull acp://coord.example/team/docs/ ./work           # just that folder
acp push acp://coord.example/team/ ./work                # send your edits back
acp send --to other-agent --subject "plan updated" --body "see acp://coord.example/team/docs/plan.md"
```

`acp put` leaves a file that already exists alone, because someone else may have changed it since you read it. To
merge with other people's edits, use `acp pull` and `acp push`. To replace a file: `acp show` it, make your change,
then `acp put … --replace`, which checks that it is still the version you read and stops if a teammate got there
first. You can name what you edited with `--base <hash or version>`. `--force` overwrites whatever is there.

Working in one workspace for a while? Set it once and use plain paths:

```bash
export ACP_URI=acp://coord.example/team/
acp ls
acp show docs/plan.md
```

An address in `--uri` or `ACP_URI` connects you, so it is the workspace root and nothing else. An address you
give a command names what that command acts on, so it can name a path. `?v=N` works where a command only reads
(`show`, `ls`, `share`, and `mount`, which gives a read-only folder).

An AI agent connected through MCP has the same with `acp_read_address`, `acp_list_address` and
`acp_write_address`, plus `acp_share` and `acp_address_info` ("what is this?"). There is one difference: the MCP bridge
uses only the login it was started with. An address can name any workspace that login reaches on its server, and an
address on another server is refused, even if this machine has a login for it. Addresses are text a model may have read
in a shared file, and text must not be able to choose a login. A share link still reads from anywhere.

To see exactly which server requests an address stands for, without connecting to anything:

```bash
acp uri resolve 'acp://coord.example/team/docs/plan.md'
```

### If you are a person

- **Paste it** into chat, a ticket or a prompt for an agent. Whoever receives it still signs in with their
  own login (or reads a share link).
- **Click it.** Register `acp://` with your computer once with `acp handler install`. After that, clicking
  an address opens `acp`, which *shows* where it points and what to run next. A click only reads and
  prints; it never connects, mounts or writes by itself. (`acp help handler` has the per-system details.)
- **Open it by hand:** `acp open acp://coord.example/team/docs/plan.md` shows the same report;
  add `--print` to be certain nothing touches the network.
- **Mount it as a folder** so your editor and file manager just work:
  `acp mount acp://coord.example/team/ ~/team`. A teammate's change appears within about a second
  (`acp help mount`).

---

## Share a file

There are two kinds of "sharing", and they behave very differently.

| | Workspace / file address | Share link |
|---|---|---|
| Looks like | `acp://host/team/docs/plan.md` | the same plus `?e=…&h=…&k=…&s=…&sp=team` |
| Who can open it | only people with a login for that workspace | **anyone who holds the link** |
| What it opens | the live workspace | the exact bytes of that file as they are right now |
| How long | as long as the person's login lives | until it expires (minutes) |
| If you edit the file | they see the new file | the link still shows the old bytes; make a new link |

### Make a share link

```bash
acp share docs/plan.md --ttl 300
# acp://coord.example/team/docs/plan.md?e=…&h=…&k=…&s=…&sp=team
```

`--ttl` takes a length of time: `600` (seconds), `10m`, `2h`, `7d`. The server's default is 5 minutes and it caps how
long a link can live. You can give an
address instead of a path: `acp share acp://coord.example/team/docs/plan.md`. The server must have been started
with a signing key (`-cap-key-file`, see [For operators](#for-operators)).

Underneath it does three steps you can also run yourself: find the file's content hash (`acp manifest`), mint an
expiring URL for that content (`acp blob url <hash> --ttl 300`), and turn that into a link
(`acp uri link '<url>' docs/plan.md`).

Send that link to the reader. They open it with no login:

```bash
acp show 'acp://coord.example/team/docs/plan.md?e=…&h=…&k=…&s=…&sp=team' --out plan.md
```

Treat the link as a key to that one file:

- Anyone who has it can read those bytes until `e=` (the expiry) passes. Pick the shortest `--ttl` that works.
- It cannot be used to read anything else, and it cannot write.
- You cannot "cancel" a single link; let it expire. If one leaks, rotating the server's signing key kills
  every link made with the old key (ask the operator).
- It is a *read* link for one file; it does not open the folder around it.

---

## Examples

### An AI agent

An agent gets this in its inbox:

> The plan is at `acp://coord.example/team/docs/plan.md`. Please add the test section, then tell me.

```bash
acp show acp://coord.example/team/docs/plan.md    # read the plan
acp pull acp://coord.example/team/docs/ ./work    # get the folder
# ... edit ./work/docs/plan.md ...
acp push acp://coord.example/team/ ./work         # send the edit back
acp send --to alice --subject "test section added" --body "acp://coord.example/team/docs/plan.md"
```

If it should give a result to someone outside the team, it runs `acp share docs/result.md` and mails that link
instead.

### A person

A teammate pastes a link in chat:

```
acp://coord.example/team/docs/plan.md?e=1893456000&h=2cf2…&k=0a1b2c3d&s=ab12…&sp=team
```

You have no login, so you read it directly: `acp show '<the link>' --out plan.md`. (No login is needed, but on a server
with a self-signed certificate this machine must already trust it; otherwise the error tells you so and nothing is sent.) You get the
file as it was when the link was made, and the download is checked against the link's hash: if the
bytes do not match, the command fails and an existing `plan.md` stays as it was.

A server with a public certificate needs nothing else. A server with its own self-signed certificate
can't be verified until this machine trusts it: ask for an invite and run `acp join <code>`, which
saves that trust, and the link reads. A login you already have for that server works the same way. If
the server uses a private certificate you hold, name it for that run with `ACP_CERT=ca.pem`. A profile's
certificate settings apply only to that profile's own server, never to a link to another one.

Later you are invited to the team (`acp join <code>`). Now you can mount the whole workspace:

```bash
acp mount acp://coord.example/team/ ~/team
ls ~/team/docs
```

---

## Common errors and what to do

`acp` checks every address before it sends anything. Errors name the problem and the fix.

| You see | It means | Fix |
|---|---|---|
| `userinfo (user:pass@host) is forbidden` / `query key "token" looks like a raw credential` | The address has a login in it. It is refused, not sent, and not repeated back. | Remove it. Sign in with a profile (`acp join <code>` or `acp config set`). |
| `this machine can't verify host:port yet` | The server's certificate is self-signed and you have not joined it. Nothing was sent. | Ask for an invite and run `acp join <code>`, then run the command again. |
| `presented a certificate your login for that server does not trust` | The server's certificate is not the one your login saved. Nothing was sent. | If it was replaced on purpose, ask the owner for a new invite and join again. |
| `does not cover the name` | The address uses a name the server's certificate doesn't list. Nothing was sent. | Use the address from your invite, or ask the server's owner to add the name. |
| `capability url expired on …` (410) | The link is past its end. | Ask whoever sent it for a new link: `acp share <path>`. |
| `a bare host names no resource (a URI needs a space: acp://host/<space>/...)` | You gave a server with no workspace. | Add the workspace: `acp://host/team/`. Or look around first with `acp uri discover acp://host/`. |
| `space "Bad Space" violates [A-Za-z0-9_-]{1,64}` | A workspace name has a space or odd character. | Use letters, digits, `_`, `-`. |
| `no such workspace "x" on this server` | The name is mistyped, or nobody has written to it yet. | `acp spaces` lists yours. The first write to a new name creates it. |
| `".." would escape the space root` | A path tried to leave the workspace. | Use a path inside the workspace. |
| `empty path segment ("//") is not allowed`, `a percent-encoded "/" is not a legal name octet` | Malformed path. | Remove the double slash, or the `%2F`. |
| `scheme "http" is not "acp"` | It is a web link, not an address. | Use `acp://...` (it always means HTTPS). |
| `port 99999 out of range` | Bad port. | Ports are 1-65535; leave it off for the default 8443. |
| `v="abc" is not a manifest version` | `?v=` must be a number. | `?v=12`. |
| `"@doc" is a reserved resource type with no v1 resolution` | Names starting with `@` are reserved. | Use a normal file name. |
| `no profile for https://host:8443` | You have no login for that server. | `acp join <code>`, or `acp config set <profile> server=https://host:8443 token_file=...`. |
| `a connection address names a space, not a path` | You passed a file address where a connection is expected. | Connect with `acp://host/space/` and name the file in the command. |
| `no such workspace "x" on this server` | Nothing has written to that name yet, or it is mistyped. Reading never creates a workspace. | `acp spaces` lists yours. To start this one, write to it: `acp put`, `acp push` or `acp send`. |
| `410` when reading a share link | The link expired. | Ask for a new one. |
| `403` when reading a share link | The link was altered or made with a key the server no longer trusts. | Ask for a new one; do not edit the link. |
| `404` when reading a share link | The file's content is no longer stored. | Ask the owner to share it again. |
| `429` when reading a share link | Too many requests from your address. | Wait a moment and retry. |
| `this server cannot make share links` | The server was started without a signing key, or it is older. | Ask the operator to start it with `-cap-key-file`, or hand over a copy: `acp show <path> --out copy`. |
| `… is already shared, and someone may have changed it` (from `acp put`) | The file exists and `put` won't overwrite it unasked. | `acp pull` + `acp push` to merge, or `acp show` it and add `--replace`. |
| `… changed since you read it` (from `acp put --replace`) | A teammate wrote a newer version after your read. Nothing was written. | `acp show` it, add your change again, run the command again. `--force` overwrites their version. |
| `… no record of you reading it from here` | `--replace` has nothing to check against. Nothing was written. | `acp show` it first, or name what you edited with `--base`, or `--force`. |
| `this is a share link: it opens one file` | A share link went to `acp mount` or another command that needs a workspace. | Read it: `acp show '<link>'`. |
| `profile "x" is not for https://…` | You named a login that belongs to another server. | Leave `--profile` out, or join that server. |
| `discovery_unsupported` / the server answers 404 at `/.well-known/acp` | An older or locked-down server. | Ask for the workspace address directly. |

If you are not sure what is wrong, `acp uri parse '<address>'` reports the problem without touching the
network.

---

## Why you can paste these safely

- **An address never carries a login.** `acp://user:token@host/...` and `?token=...` are refused before any
  request is made, and the secret is not echoed back. Your login lives in your profile
  (`~/.acp/config.json`), matched to the server by `host:port`.
- **The space comes from the address; the permissions come from your login.** Knowing an address does
  not give access to it.
- **A share link is a bearer key** for one file until it expires. Whoever has it can read that file. Send
  it only to the intended reader, keep the lifetime short, and do not post it somewhere public.
- **Clicking an address is read-only.** The handler resolves and prints; it does not connect, mount or write
  unless you run the next command yourself.

---

## For developers

- Go: the `acpuri` package of the Go SDK (parse, canonical, resolve, discovery, signed links).
- TypeScript: `@ab0t/acp` on npm; its README has an `acp://` section (address parsing, connect-by-address, the virtual filesystem).
- Both read from one shared set of test vectors, so they agree on what is valid.
- Command line: `acp uri parse|canonical|resolve|link|read|discover`; `acp help uri`.
- The specification is the `acp://` scheme RFC (`rfc/acp-ext-32-acp-uri-scheme.txt` in the public repo).
  The operating-system click handler is described by `acp help handler`.

## For operators

- **Share links:** start `coordd` with `-cap-key-file <file>` (one or more keys; the first signs, all verify).
  Without it the server does not offer the `bloburl` feature and `acp share` fails with a clear message.
  Link lifetime is clamped by `-cap-max-ttl` (default 900 seconds, default link 300). To revoke every
  link at once, rotate the key (see [`OPERATIONS.md`](OPERATIONS.md)).
- **Bare-host discovery:** every server answers `GET /.well-known/acp` with its origin and features. It lists
  workspace names only if you start it with `-discovery-spaces`. `-discovery-endpoint` and
  `-discovery-default-space` adjust what it publishes. `GET /v1/healthz` lists `acpuri` in `capabilities`.
- **Certificates:** an address always means HTTPS. If you invite people by a name, that name must be in the
  server's certificate (`coordd -hosts`).
