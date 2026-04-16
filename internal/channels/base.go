package channels

// baseAdapter holds the shared message channel and stop signal used by all
// channel adapter implementations (Slack, Discord, Matrix). Embed this struct
// and call baseAdapter.Messages() / baseAdapter.IsConnected() to satisfy those
// parts of the Channel interface without duplication.
type baseAdapter struct {
	msgChan  chan Message
	stopChan chan struct{}
}

func newBaseAdapter(bufSize int) baseAdapter {
	return baseAdapter{
		msgChan:  make(chan Message, bufSize),
		stopChan: make(chan struct{}),
	}
}

// Messages implements Channel.Messages.
func (b *baseAdapter) Messages() <-chan Message { return b.msgChan }

// IsConnected implements Channel.IsConnected.
// Always returns true — the underlying socket-mode / WebSocket clients handle
// reconnection internally.
func (b *baseAdapter) IsConnected() bool { return true }
