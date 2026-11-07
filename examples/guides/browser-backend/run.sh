#!/usr/bin/env bash
# GUIDE: browser-backend — use ACP from a web page without the page ever holding a token.
#
# A small backend on the page's own site logs people in with an ACP invite (redeemed by the backend), keeps
# each person's own ACP login server-side, and forwards an allowlist of routes to coordd, adding that login.
# The page uses the TypeScript SDK's browser build with NO token.
#
# This script, self-checking, on a SCRATCH coordd (temp data folder, 127.0.0.1, torn down at the end):
#   1. starts coordd with one login per agent (an admin, and a command-line agent "reviewer"),
#   2. creates two invites (alice, bob) with `acp invite`,
#   3. starts the backend (node backend.mjs) against coordd, verifying its certificate,
#   4. runs journey.mjs: two "tabs" load the page's code FROM the backend and chat, mail, co-edit a text
#      document (with the command-line agent too), share a board, save files, show presence,
#   5. runs the backend's named tests (test/backend.test.mjs: allowlist, credential handling, revocation, …).
# Needs: node >= 20, curl, and:
#   - acp + coordd: ACP_BIN=<a folder holding both>, or found on PATH. In a checkout of the ACP sources they come from
#     dist/ or are built with go.
#   - the SDK's browser build: `npm install @ab0t/acp` in this folder (done here if it is missing). In a checkout, sdk/ts.
# It stands alone: copy this folder anywhere and it runs.
# Run: ./run.sh

set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECKOUT="$(cd "$HERE/../../.." && pwd)"
_PASS=0; _FAIL=0
WORK="$(mktemp -d "${TMPDIR:-/tmp}/acp-example.XXXXXX")"
COORDD_PID=""; BACKEND_PID=""
teardown() {
  [ -n "$BACKEND_PID" ] && kill "$BACKEND_PID" >/dev/null 2>&1
  [ -n "$COORDD_PID" ] && kill "$COORDD_PID" >/dev/null 2>&1
  wait 2>/dev/null
  rm -rf "$WORK"
}
trap teardown EXIT
check() { local desc="$1"; shift; if "$@"; then _PASS=$((_PASS+1)); echo "  PASS: $desc"; else _FAIL=$((_FAIL+1)); echo "  FAIL: $desc" >&2; fi; }
contains() { grep -Fq -- "$2" "$1"; }
as() { local who="$1"; shift; ( cd "$WORK" && ACP_AGENT="$who" ACP_HARNESS=example "$BINDIR/acp" "$@" ); }

command -v node >/dev/null || { echo "SKIP: node is not installed (needs node >= 20)"; exit 0; }

# acp + coordd
platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) arch="$(uname -m)" ;; esac
  echo "$os-$arch"
}
BINDIR=""
if [ -n "${ACP_BIN:-}" ]; then
  [ -x "$ACP_BIN/acp" ] && [ -x "$ACP_BIN/coordd" ] || { echo "ACP_BIN=$ACP_BIN does not hold acp and coordd" >&2; exit 1; }
  BINDIR="$ACP_BIN"
elif [ -x "$CHECKOUT/dist/$(platform)/acp" ] && [ -x "$CHECKOUT/dist/$(platform)/coordd" ]; then
  BINDIR="$CHECKOUT/dist/$(platform)"
elif [ -f "$CHECKOUT/go.mod" ] && [ -d "$CHECKOUT/cmd/coordd" ] && command -v go >/dev/null; then
  echo "building acp and coordd from source..."
  BINDIR="$WORK/bin"; mkdir -p "$BINDIR"
  ( cd "$CHECKOUT" && go build -o "$BINDIR/acp" ./cmd/acp && go build -o "$BINDIR/coordd" ./cmd/coordd ) || { echo "build failed" >&2; exit 1; }
elif command -v coordd >/dev/null && command -v acp >/dev/null; then
  BINDIR="$(dirname "$(command -v coordd)")"
  [ -x "$BINDIR/acp" ] || { echo "acp and coordd are in different folders: set ACP_BIN to a folder holding both" >&2; exit 1; }
else
  echo "acp and coordd were not found: install them, or set ACP_BIN to a folder holding both" >&2; exit 1
fi

# the SDK's browser build, served to the page as /sdk/acp.esm.js
SDK_BUNDLE="$HERE/node_modules/@ab0t/acp/dist/browser/acp.esm.js"
if [ ! -f "$SDK_BUNDLE" ] && [ -f "$CHECKOUT/sdk/ts/package.json" ]; then
  SDK_BUNDLE="$CHECKOUT/sdk/ts/dist/browser/acp.esm.js"
  if ! grep -q "TextDocSession" "$SDK_BUNDLE" 2>/dev/null; then
    echo "building the TypeScript SDK (its browser bundle is missing or older than this example)..."
    ( cd "$CHECKOUT/sdk/ts" && { [ -d node_modules ] || npm ci --no-audit --no-fund >/dev/null; } && npm run build >/dev/null ) \
      || { echo "SDK build failed" >&2; exit 1; }
  fi
elif [ ! -f "$SDK_BUNDLE" ]; then
  echo "installing the TypeScript SDK..."
  ( cd "$HERE" && npm install --no-save --no-audit --no-fund @ab0t/acp@latest >/dev/null ) || { echo "npm install @ab0t/acp failed" >&2; exit 1; }
fi
grep -q "TextDocSession" "$SDK_BUNDLE" 2>/dev/null || { echo "$SDK_BUNDLE is older than this example (it has no TextDocSession): update @ab0t/acp" >&2; exit 1; }

# One login per agent: coordd derives who you are from your login (the backend refuses a shared-key server).
mint() { local d="$1" n="$2"; shift 2; "$BINDIR/coordd" -data "$d" -mkagent "$n" "$@" 2>/dev/null | sed -nE 's/.*token: ([0-9a-f]+).*/\1/p'; }
coordd_prestart() {
  ADMIN_TOKEN="$(mint "$1" admin -role admin)"
  AGENT_TOKEN="$(mint "$1" reviewer -role writer -spaces team)"
}

# A scratch coordd: its own data folder under $WORK and a free 127.0.0.1 port; removed at the end.
DATA="$WORK/coordd-data"; mkdir -p "$DATA"
coordd_prestart "$DATA"
export ACP_HOME="$WORK/.acp-home"; mkdir -p "$ACP_HOME"
for tries in 1 2 3 4 5; do
  PORT=$(( (RANDOM % 20000) + 20000 ))
  "$BINDIR/coordd" -addr "127.0.0.1:$PORT" -data "$DATA" >"$WORK/coordd.log" 2>&1 &
  COORDD_PID=$!
  for _ in $(seq 1 40); do
    [ -f "$DATA/cert.pem" ] && curl -s --cacert "$DATA/cert.pem" "https://127.0.0.1:$PORT/v1/healthz" >/dev/null 2>&1 && break 2
    kill -0 "$COORDD_PID" 2>/dev/null || break
    sleep 0.25
  done
  kill "$COORDD_PID" >/dev/null 2>&1; wait "$COORDD_PID" 2>/dev/null; COORDD_PID=""
done
[ -n "$COORDD_PID" ] || { echo "coordd would not start:" >&2; tail -n 20 "$WORK/coordd.log" >&2; exit 1; }
export ACP_SERVER="https://127.0.0.1:$PORT" ACP_CERT="$DATA/cert.pem"
echo "coordd up on $ACP_SERVER"
O="$WORK/o"

echo
echo "### 1. the admin invites two people into workspace 'team' (the codes go to them; acp never stores them)"
ACP_TOKEN="$ADMIN_TOKEN" ACP_SPACE=team as admin invite --name alice --space team >"$O.inv.alice" 2>&1
ACP_TOKEN="$ADMIN_TOKEN" ACP_SPACE=team as admin invite --name bob --space team >"$O.inv.bob" 2>&1
INVITE_ALICE="$(grep -oE 'acpj1_[A-Za-z0-9_-]+' "$O.inv.alice" | head -1)"
INVITE_BOB="$(grep -oE 'acpj1_[A-Za-z0-9_-]+' "$O.inv.bob" | head -1)"
echo "  alice: ${INVITE_ALICE:0:14}…   bob: ${INVITE_BOB:0:14}…"

echo
echo "### 2. start the backend (it checks coordd's certificate against $ACP_CERT; no option turns that off)"
node "$HERE/backend.mjs" --upstream "$ACP_SERVER" --ca "$ACP_CERT" --sdk "$SDK_BUNDLE" --port 0 >"$O.backend" 2>&1 &
BACKEND_PID=$!
BACKEND=""
for _ in $(seq 1 40); do
  BACKEND="$(grep -oE 'http://127\.0\.0\.1:[0-9]+' "$O.backend" | head -1)"
  [ -n "$BACKEND" ] && break
  kill -0 "$BACKEND_PID" 2>/dev/null || break
  sleep 0.25
done
echo "  backend: ${BACKEND:-<did not start>}"; [ -n "$BACKEND" ] || cat "$O.backend"

echo
echo "### 3. the page itself: served by the backend, with a strict content policy and no token in it"
curl -s -D "$O.page.h" "$BACKEND/" >"$O.page"
curl -s "$BACKEND/app.js" "$BACKEND/page-core.js" >"$O.page.js"
curl -s -o /dev/null -w '%{http_code}' "$BACKEND/acp/v1/whoami" >"$O.anon"
curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{}' "$BACKEND/acp/v1/admin/invites" >"$O.adm"
echo "  GET / → $(head -1 "$O.page.h" | tr -d '\r')   anonymous whoami → $(cat "$O.anon")   admin route → $(cat "$O.adm")"

echo
echo "### 4. two tabs and a command-line agent work together through the backend"
WORK="$WORK" INVITE_ALICE="$INVITE_ALICE" INVITE_BOB="$INVITE_BOB" ACP_BIN_DIR="$BINDIR" AGENT_TOKEN="$AGENT_TOKEN" \
  timeout 120 node "$HERE/journey.mjs" "$BACKEND" >"$O.journey" 2>&1
sed 's/^/  /' "$O.journey"

echo
echo "### 5. the backend's named tests (scratch daemons of their own)"
( cd "$HERE" && ACP_COORDD="$BINDIR/coordd" timeout 300 node --test test/backend.test.mjs ) >"$O.tests" 2>&1
grep -E '^(ok|not ok) ' "$O.tests" | sed 's/^/  /'

echo
echo "### 6. what coordd recorded: every change under the PERSON's own name"
api_events() { curl -s --cacert "$ACP_CERT" -H "Authorization: Bearer $ADMIN_TOKEN" -H "X-ACP-Space: team" "$ACP_SERVER/v1/events?from=1"; }
api_events >"$O.events"
grep -oE '"actor":"[a-z]+","action":"[a-z.]+"' "$O.events" | sort | uniq -c | sed 's/^/  /' | head -12

# ---- ASSERTIONS ------------------------------------------------------------------------
echo
echo "### verifying"
check "two invite codes were made"                          bash -c "[ -n '$INVITE_ALICE' ] && [ -n '$INVITE_BOB' ]"
check "the backend started against coordd's verified cert"  bash -c "[ -n '$BACKEND' ]"
check "the page is served with a content security policy"   bash -c "grep -qi 'content-security-policy:.*script-src .self.' '$O.page.h'"
check "the page and its code hold no token"                 bash -c "! grep -q '$ADMIN_TOKEN' '$O.page' '$O.page.js' && ! grep -qi 'Authorization' '$O.page.js'"
check "the page's scripts parse as browser modules"        bash -c "node --input-type=module --check < '$HERE/public/app.js' && node --input-type=module --check < '$HERE/public/page-core.js'"
check "no session: 401"                                     contains "$O.anon" "401"
check "admin routes are not forwarded: 404"                 contains "$O.adm" "404"
for step in \
  "both people log in with their invite" \
  "each tab acts as its own person" \
  "chat: a message from one tab reaches the other" \
  "mail: alice writes to bob" \
  "notes: two tabs type into one live document and converge" \
  "notes: a command-line agent edits the same document" \
  "board: two tabs add tasks at the same moment" \
  "files: a file one tab saves shows up in the other" \
  "files: a teammate's newer save is NEVER overwritten" \
  "presence: alice says she is here" \
  "the page cannot reach admin routes" \
  "no token ever left a tab" \
  "closing: every document saved"; do
  check "journey: $step" grep -qF "JOURNEY ok: $step" "$O.journey"
done
check "journey: nothing failed"                             contains "$O.journey" "JOURNEY done: all ok"
check "backend tests: all passed"                           bash -c "grep -q '^# fail 0' '$O.tests' && ! grep -q '^# pass 0' '$O.tests'"
check "coordd logged the tabs' changes as alice and bob"    bash -c "grep -q '\"actor\":\"alice\"' '$O.events' && grep -q '\"actor\":\"bob\"' '$O.events'"
check "the agent's edit is logged under its own name"       bash -c "grep -q '\"actor\":\"reviewer\"' '$O.events'"

echo
if [ "$_FAIL" = 0 ]; then echo "=== RESULT: PASS ($_PASS checks) ==="; else echo "=== RESULT: FAIL ($_FAIL failed, $_PASS passed) ===" >&2; exit 1; fi
