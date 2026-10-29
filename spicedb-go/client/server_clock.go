package client

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// errServerWait is the cancellation cause when a read's server clock runs out.
var errServerWait = errors.New("spicedb: waiting on the server exceeded the client default deadline")

// serverClock bounds the time a finite read spends waiting on the server by
// the client default deadline, and never the time the caller spends holding
// an item: an iterator yields from inside its stream, so a plain context
// deadline on that stream would keep running through the caller's loop body,
// and a caller doing slow work per item would fail a healthy read.
//
// The clock runs while the client waits on the server (opening the stream,
// receiving, retry backoff) and is paused by hold while the caller has an
// item. Paged reads use a cumulative clock: the default bounds one page's
// total server time, from opening it to its last item. A single long stream
// uses a per-wait clock, which bounds each wait for the next message.
//
// A nil *serverClock (the default deadline disabled) does nothing.
type serverClock struct {
	timer      *time.Timer
	full       time.Duration
	remaining  time.Duration
	started    time.Time
	cumulative bool
	running    bool
}

// serverClock starts a clock under ctx, which carries the operation's total
// deadline and still wins when earlier. The returned context is cancelled
// when the clock runs out; release stops the clock and cancels the context.
func (c *Client) serverClock(ctx context.Context, cumulative bool) (context.Context, *serverClock, context.CancelFunc) {
	if c.noDefaultDeadline {
		x, cancel := context.WithCancel(ctx)
		return x, nil, cancel
	}
	d := c.defaultTimeout
	if d == 0 {
		d = 30 * time.Second
	}
	x, cancel := context.WithCancelCause(ctx)
	k := &serverClock{full: d, remaining: d, cumulative: cumulative, started: time.Now(), running: true}
	k.timer = time.AfterFunc(d, func() { cancel(errServerWait) })
	return x, k, func() {
		k.timer.Stop()
		cancel(context.Canceled)
	}
}

// hold pauses the clock while the caller has an item.
func (k *serverClock) hold() {
	if k == nil || !k.running {
		return
	}
	k.running = false
	if k.timer.Stop() && k.cumulative {
		k.remaining -= time.Since(k.started)
	}
}

// wait resumes the clock when the client waits on the server again.
func (k *serverClock) wait() {
	if k == nil || k.running {
		return
	}
	if !k.cumulative {
		k.remaining = k.full
	}
	k.running, k.started = true, time.Now()
	k.timer.Reset(max(k.remaining, 0))
}

// err reports a stream failure caused by the clock running out as the
// client's DEADLINE_EXCEEDED error; any other error passes through.
func (k *serverClock) err(ctx context.Context, op string, e error) error {
	if k == nil || !errors.Is(context.Cause(ctx), errServerWait) {
		return e
	}
	what := "a page"
	if !k.cumulative {
		what = "the next message"
	}
	return &Error{
		Code:    CodeDeadlineExceeded,
		Message: fmt.Sprintf("spicedb: %s: the server did not deliver %s within the client default deadline (%s)", op, what, k.full),
		// Not e: the stream reports the clock's cancellation as Canceled,
		// which must not match ErrCanceled.
		err: errors.Join(context.DeadlineExceeded, errServerWait),
	}
}
