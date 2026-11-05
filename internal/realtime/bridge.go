package realtime

import (
	"context"
	"errors"
)

// ClientConn is the browser side of a bridge (a WebSocket in agentd).
type ClientConn interface {
	ReadEvent() (ClientEvent, error)
	WriteEvent(ServerEvent) error
}

// Bridge pumps events between a browser connection and a model session
// until either side closes or ctx ends. Malformed client events are reported
// back to the browser and dropped; they never reach the model server.
// It returns the first error that ended the bridge.
func Bridge(ctx context.Context, client ClientConn, sess Session) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)

	// model → browser
	go func() {
		for {
			ev, err := sess.Recv()
			if err != nil {
				_ = client.WriteEvent(ServerEvent{Type: ServerError, Message: "voice session ended", Fatal: true})
				errc <- err
				return
			}
			if err := client.WriteEvent(ev); err != nil {
				errc <- err
				return
			}
		}
	}()

	// browser → model
	go func() {
		for {
			ev, err := client.ReadEvent()
			if err != nil {
				errc <- err
				return
			}
			if err := ev.Validate(); err != nil {
				if werr := client.WriteEvent(ServerEvent{Type: ServerError, Message: "ignored malformed event"}); werr != nil {
					errc <- werr
					return
				}
				continue
			}
			if err := sess.Send(ev); err != nil {
				errc <- err
				return
			}
		}
	}()

	var err error
	select {
	case err = <-errc:
	case <-ctx.Done():
		err = ctx.Err()
	}
	_ = sess.Close()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
