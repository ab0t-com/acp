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
	for {
		head, err := c.eventsHead()
		if err != nil {
			return nil, err
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
		if err := c.waitMailEvent(ctx, head+1, g.Agent, from); err != nil {
			return nil, err
		}
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

// eventsHead is the space's current event-log head (the stats "events" counter).
func (c *Client) eventsHead() (uint64, error) {
	st, err := c.Stats()
	if err != nil {
		return 0, err
	}
	n, ok := st["events"].(float64)
	if !ok {
		return 0, fmt.Errorf("stats: no events head in the daemon's response")
	}
	return uint64(n), nil
}

// waitMailEvent follows chat.sent events from seq until one is addressed to me
// (and from from, when set), reconnecting after a dropped stream. A real API
// error (auth, a missing space) is returned rather than retried. If the deadline
// passes while the stream is DOWN (the daemon is unreachable), that is an error,
// not ErrNoMail: "nothing yet" and "can't tell" must stay distinguishable.
func (c *Client) waitMailEvent(ctx context.Context, seq uint64, me, from string) error {
	f := &EventFilter{Actions: []string{"chat.sent"}}
	var down error // the last stream failure; cleared whenever a stream opens
	opts := &FollowOptions{Context: ctx, OnOpen: func() { down = nil }}
	noMail := func() error {
		if down != nil {
			return fmt.Errorf("waiting for mail: lost the daemon's event stream: %w", down)
		}
		return ErrNoMail
	}
	for {
		err := c.followStream(seq, f, opts, func(e wire.Event) error {
			seq = e.Seq + 1
			if e.Entity == me && (from == "" || e.Actor == from) {
				return errGotMail
			}
			return nil
		})
		if errors.Is(err, errGotMail) {
			return nil
		}
		if ctx.Err() != nil {
			return noMail()
		}
		var ae *APIError
		if errors.As(err, &ae) {
			return err
		}
		down = err
		select {
		case <-ctx.Done():
			return noMail()
		case <-time.After(time.Second):
		}
	}
}
