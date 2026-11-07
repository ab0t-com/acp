package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// ErrNoMail is WaitForMail's result when its context ends before mail arrives.
var ErrNoMail = errors.New("no new mail before the deadline")

// waitAfterInbox is a test hook run after WaitForMail has read an empty inbox and
// before it starts following (the race window); nil in production.
var waitAfterInbox func()

// errGotMail stops the follow stream once a matching chat.sent event is seen.
var errGotMail = errors.New("mail event")

// WaitForMail blocks until this client's agent has unread mail (only from sender
// from, when it is non-empty) and returns it in inbox order — at once if some is
// already unread (read or reply to it to mark it read). It cannot miss a
// message sent while it starts: it notes the event-log head, returns any mail
// already unread, and only then follows the log from that head for the daemon's
// chat.sent event addressed to this agent. Bound the wait with ctx (a timeout);
// when ctx ends first it returns ErrNoMail. Nothing is marked read.
func (c *Client) WaitForMail(ctx context.Context, from string) ([]wire.Message, error) {
	g, err := c.Whoami() // the server's view of who we are (per-agent mode: the token's identity)
	if err != nil {
		return nil, err
	}
	again := false // the last wait ended on another history: the next one pauses first
	for {
		// The head and the log it belongs to come from one answer (D55), and the event at the
		// head from the same log (D57): a stream that later names another log whose event
		// there differs (a restore, even between these reads and the stream) sends the wait
		// back to read the inbox again; the same event (a restart, a leader change) does not.
		head, logID, err := c.logPosition(ctx)
		if err != nil {
			return nil, err
		}
		digest := ""
		if logID != "" && head > 0 {
			if e, id, err := c.EventAt(ctx, head); err == nil && id == logID && e != nil {
				digest = EventHash(*e)
			}
		}
		msgs, err := c.Inbox(true)
		if err != nil {
			return nil, err
		}
		if msgs = fromSender(msgs, from); len(msgs) > 0 {
			return msgs, nil
		}
		if waitAfterInbox != nil {
			waitAfterInbox()
		}
		if again && !sleepCtx(ctx, time.Second) { // two histories in turn (P2-1): not a hot loop
			return nil, ErrNoMail
		}
		changed, err := c.waitMailEvent(ctx, head+1, logID, digest, g.Agent, from)
		if err != nil {
			return nil, err
		}
		again = changed
		// A matching event arrived: loop and re-read the inbox (another session of
		// this agent may have read it meanwhile, in which case we keep waiting).
	}
}

func fromSender(msgs []wire.Message, from string) []wire.Message {
	if from == "" {
		return msgs
	}
	out := msgs[:0:0]
	for _, m := range msgs {
		if m.From == from {
			out = append(out, m)
		}
	}
	return out
}

// waitMailEvent follows chat.sent events from seq until one is addressed to me
// (and from from, when set), reconnecting after a dropped stream. It also returns
// when a stream names another log than logID (D55): the inbox is read again then. A real API
// error (auth, a missing space) is returned rather than retried. If the deadline
// passes while the stream is DOWN (the daemon is unreachable), that is an error,
// not ErrNoMail: "nothing yet" and "can't tell" must stay distinguishable.
func (c *Client) waitMailEvent(ctx context.Context, seq uint64, logID, digest, me, from string) (changed bool, err error) {
	f := &EventFilter{Actions: []string{"chat.sent"}}
	var down error // the last stream failure; cleared whenever a stream opens
	markSeq := seq - 1
	var markEvent *wire.Event // the last event seen; its digest is taken only when a check needs it
	noMail := func() error {
		if down != nil {
			return fmt.Errorf("waiting for mail: lost the daemon's event stream: %w", down)
		}
		return ErrNoMail
	}
	for {
		conn, drop := context.WithCancel(ctx)
		var checkErr error
		named := false // this stream names the log this wait has checked (R-C30 N4)
		opts := &FollowOptions{Context: conn, OnOpen: func() { down = nil },
			OnStreamOpen: func(o StreamOpen) {
				if logID != "" && o.LogID != "" && o.LogID != logID {
					if markEvent != nil {
						digest, markEvent = EventHash(*markEvent), nil
					}
					same, err := c.continues(ctx, markSeq, digest, o.LogID, o.Head)
					switch {
					case err != nil:
						checkErr = err // can't tell now: ask again on the next stream
						drop()
						return
					case !same:
						changed = true // a restore: mail may have come under seqs behind seq
						drop()
					}
				}
				if o.LogID != "" {
					logID = o.LogID
				}
				named = o.LogID != "" && o.LogID == logID
			}}
		err := c.followStream(seq, f, opts, func(e wire.Event) error {
			if err := conn.Err(); err != nil {
				return err
			}
			seq = e.Seq + 1
			if named { // only a named stream's events move the mark the next id is checked at
				ev := e
				markSeq, markEvent = e.Seq, &ev
			}
			if e.Entity == me && (from == "" || e.Actor == from) {
				return errGotMail
			}
			return nil
		})
		drop()
		if errors.Is(err, errGotMail) || (changed && ctx.Err() == nil) {
			return changed, nil // the caller reads the inbox again (and, on another log, waits from its head)
		}
		if ctx.Err() != nil {
			return false, noMail()
		}
		var ae *APIError
		if errors.As(err, &ae) {
			return false, err
		}
		if checkErr != nil {
			err = checkErr
		}
		down = err
		select {
		case <-ctx.Done():
			return false, noMail()
		case <-time.After(time.Second):
		}
	}
}
