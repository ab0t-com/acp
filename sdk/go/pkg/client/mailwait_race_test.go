package client

// The "cannot miss a message" guarantee (tickets/acp-client-chat-quickwins-20261029,
// verifier finding 2026-10-29): mail sent AFTER the inbox read but BEFORE the follow
// starts must still be delivered, because the follow starts from the head noted
// BEFORE the inbox read. Deterministic: the hook sends at exactly that moment.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// raceDaemon: one space, one event log, one inbox for "me". The follow stream replays
// events with seq >= from, then holds the connection open.
type raceDaemon struct {
	mu    sync.Mutex
	evs   []wire.Event
	inbox []wire.Message
}

func (d *raceDaemon) send(to, body string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.evs = append(d.evs, wire.Event{Seq: uint64(len(d.evs) + 1), Actor: "peer", Action: "chat.sent", Entity: to})
	d.inbox = append(d.inbox, wire.Message{ID: strconv.Itoa(len(d.inbox) + 1), From: "peer", To: to, Body: body})
}

func (d *raceDaemon) server(t *testing.T) *httptest.Server {
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, r *http.Request) { js(w, map[string]any{"agent": "me"}) })
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		js(w, map[string]any{"events": len(d.evs)})
	})
	mux.HandleFunc("/v1/mail", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		js(w, append([]wire.Message{}, d.inbox...))
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.Atoi(r.URL.Query().Get("from"))
		w.Header().Set("Content-Type", "application/x-ndjson")
		d.mu.Lock()
		for _, e := range d.evs {
			if int(e.Seq) >= from {
				json.NewEncoder(w).Encode(e)
			}
		}
		d.mu.Unlock()
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestWaitForMailCatchesMailSentInTheRaceWindow(t *testing.T) {
	d := &raceDaemon{}
	ts := d.server(t)
	c, err := New(ts.URL, "tok", "me", "", true)
	if err != nil {
		t.Fatal(err)
	}
	waitAfterInbox = func() { d.send("me", "sent in the window") } // after the (empty) inbox read, before the follow
	t.Cleanup(func() { waitAfterInbox = nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msgs, err := c.WaitForMail(ctx, "")
	if err != nil || len(msgs) != 1 || msgs[0].Body != "sent in the window" {
		t.Fatalf("mail sent between the inbox read and the follow must be delivered: %v %+v", err, msgs)
	}
}
