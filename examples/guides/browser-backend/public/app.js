// app.js — the page: wires page-core.js (all the ACP code) to the DOM. No token anywhere in this file or the page.
import * as ACP from "/sdk/acp.esm.js";
import * as core from "/page-core.js";

const $ = (id) => document.getElementById(id);
const http = (url, init) => fetch(url, { credentials: "same-origin", ...init });
const origin = location.origin;
const li = (text) => Object.assign(document.createElement("li"), { textContent: text });

async function start(me) {
  $("login").hidden = true;
  $("app").hidden = false;
  $("who").textContent = `— ${me.name} in ${me.space}`;
  const client = core.connect(ACP, http, origin, me);

  // Unsaved changes: shown in the title bar, and the browser asks before the tab closes.
  const parts = [];
  const showSaved = () => {
    const u = core.unsaved(...parts);
    $("saved").textContent = u ? "· unsaved changes" : "· all saved";
    $("saved").className = u ? "status unsaved" : "status";
  };
  addEventListener("beforeunload", (e) => {
    if (core.unsaved(...parts)) {
      e.preventDefault();
      e.returnValue = "";
    }
  });

  // Notes — render the merged text straight into the editor (synchronously), send every edit.
  const notesBox = $("notes");
  const notes = await core.openNotes(
    ACP,
    client,
    (text) => {
      const at = notesBox.selectionStart;
      notesBox.value = text;
      notesBox.selectionStart = notesBox.selectionEnd = Math.min(at, text.length);
    },
    showSaved,
  );
  parts.push(notes);
  notesBox.value = notes.text();
  notesBox.addEventListener("input", () => {
    try {
      notes.setText(notesBox.value);
    } catch (err) {
      // A wipe of most of the shared notes needs a yes.
      if (err.code === "EPERM" && confirm("This deletes almost all of the shared notes for everyone. Continue?")) notes.setText(notesBox.value, { confirm: true });
      else notesBox.value = notes.text();
    }
  });

  // Board
  const board = await core.openBoard(ACP, client, () => drawBoard());
  const drawBoard = () => {
    $("board").replaceChildren(
      ...board.items().map((t, i) => {
        const x = li(t + " ");
        const b = Object.assign(document.createElement("button"), { textContent: "done" });
        b.onclick = () => board.remove(i);
        x.append(b);
        return x;
      }),
    );
  };
  drawBoard();
  $("board-form").onsubmit = async (e) => {
    e.preventDefault();
    if ($("board-new").value) await board.add($("board-new").value);
    $("board-new").value = "";
  };

  // Chat
  const chat = core.openChat(ACP, client, me, (m) => $("chat").append(li(`${m.from}: ${m.text}`)));
  $("chat-form").onsubmit = async (e) => {
    e.preventDefault();
    if ($("chat-text").value) await chat.say($("chat-text").value);
    $("chat-text").value = "";
  };

  // Mail
  core.watchMail(ACP, client, me, (msgs) => $("inbox").replaceChildren(...msgs.map((m) => li(`${m.from}: ${m.subject ?? ""}`))));
  $("mail-form").onsubmit = async (e) => {
    e.preventDefault();
    await core.sendMail(client, $("mail-to").value, $("mail-subject").value, "");
    $("mail-subject").value = "";
  };

  // Files — saved only when flush() resolves; a teammate's newer version is never overwritten.
  const files = core.openFiles(ACP, client, location.host, me, () => drawFiles(), showSaved);
  parts.push(files);
  const drawFiles = async () => $("files").replaceChildren(...(await files.readdir()).map((f) => li(`${f.name}${f.pending ? " (unsaved)" : ""}`)));
  drawFiles().catch(() => {});
  $("file-form").onsubmit = async (e) => {
    e.preventDefault();
    try {
      await files.writeFile($("file-name").value, $("file-text").value);
      await files.flush();
      $("file-status").textContent = "saved";
    } catch (err) {
      $("file-status").textContent = err.code === "ECONFLICT" ? "someone changed this file since you opened it — yours was NOT saved over theirs; reload it and try again" : `not saved: ${err.message}`;
    }
    drawFiles().catch(() => {});
  };

  // Presence (over plain HTTP: browsers cannot authenticate the presence WebSocket)
  const here = async () => {
    await core.announce(client, me, { page: "workspace" });
    const snap = await client.awareness();
    $("presence").replaceChildren(...snap.entries.map((x) => li(x.actor)));
  };
  here().catch(() => {});
  setInterval(() => here().catch(() => {}), 10_000);
}

$("login-form").onsubmit = async (e) => {
  e.preventDefault();
  try {
    await start(await core.login(http, origin, $("invite").value, $("name").value));
  } catch (err) {
    $("login-error").textContent = err.message;
  }
};

const me = await core.whoAmI(http, origin);
if (me) start(me);
