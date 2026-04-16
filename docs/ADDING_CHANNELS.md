# Adding New Chat Platform Integrations

> **NOTE:** The `cmd/server` Channel interface approach described in this document is **deprecated**. New chat platform integrations should be implemented as standalone bridge processes (like `cmd/zulip-bridge`) that connect to `agentd` over the WebSocket protocol. See `cmd/zulip-bridge/bridge.go` as the reference implementation and [docs/AGENTD_REFACTOR.md](AGENTD_REFACTOR.md) for the protocol design.

---

## Current Approach (Recommended): Bridge Process

Implement a new bridge as a standalone binary in `cmd/<platform>-bridge/`. The bridge:
1. Connects to `agentd` via WebSocket (`GET /sessions/:id/ws`)
2. Listens for messages from the chat platform
3. Forwards messages to agentd via `send_message` ClientMessage
4. Forwards agentd `Chunk`/`Done` events back to the chat platform

See `cmd/zulip-bridge/bridge.go` for a complete working example.

---

## Legacy Approach (Deprecated): cmd/server Channel Interface

This guide explains how to add support for new chat platforms (Slack, Discord, Matrix, etc.) to the server.

## Architecture Overview

The server uses a `Channel` interface to abstract different chat platforms. This allows the core server logic to be completely platform-agnostic.

```
┌─────────────┐
│   Server    │  ← Channel-agnostic business logic
└──────┬──────┘
       │ uses
       ▼
┌─────────────┐
│   Channel   │  ← Interface (internal/channels/interface.go)
│  Interface  │
└──────┬──────┘
       │ implemented by
       ├──────────────┬──────────────┬──────────────┐
       ▼              ▼              ▼              ▼
┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐
│   Zulip    │ │   Slack    │ │  Discord   │ │  Matrix    │
│  Channel   │ │  Channel   │ │  Channel   │ │  Channel   │
└────────────┘ └────────────┘ └────────────┘ └────────────┘
     ✅              🚧             🚧             🚧
```

## Channel Interface

Every channel must implement this interface (`internal/channels/interface.go`):

```go
type Channel interface {
    // Start initializes and starts the channel connection
    Start(ctx context.Context) error

    // Stop gracefully shuts down the channel
    Stop(ctx context.Context) error

    // IsConnected returns whether the channel is currently connected
    IsConnected() bool

    // Messages returns a channel for receiving incoming messages
    Messages() <-chan Message

    // SendResponse sends a response back to the channel
    SendResponse(originalMsg Message, response string, err error) error

    // Name returns the channel type name (e.g., "zulip", "slack")
    Name() string
}
```

## Universal Message Format

Messages from all channels are converted to this generic format:

```go
type Message struct {
    ID         string      // Unique message ID
    Content    string      // Message text content
    SenderID   string      // Sender unique ID
    SenderName string      // Sender display name
    Type       string      // Message type: "direct", "channel", "thread", etc.
    Recipient  string      // Channel/stream/room name or user ID
    Topic      string      // Thread topic (for threaded channels)
    Raw        interface{} // Original message object for channel-specific operations
}
```

The `Raw` field stores the original platform-specific message object, which is needed when sending responses back.

## Step-by-Step Guide

### 1. Create Configuration

Add config fields in `internal/config/config.go`:

```go
// SlackConfig holds Slack-related configuration
type SlackConfig struct {
    Enabled  bool   `mapstructure:"enabled"`
    Token    string `mapstructure:"token"`
    AppToken string `mapstructure:"app_token"`
}

// Add to Config struct
type Config struct {
    // ... existing fields ...
    Slack SlackConfig `mapstructure:"slack"`
}

// Add defaults in Load()
v.SetDefault("slack.enabled", false)

// Add environment variable bindings
v.BindEnv("slack.enabled", "SLACK_ENABLED")
v.BindEnv("slack.token", "SLACK_TOKEN")
v.BindEnv("slack.app_token", "SLACK_APP_TOKEN")
```

### 2. Implement the Channel

Create `internal/channels/slack.go`:

```go
package channels

import (
    "context"
    "fmt"

    "github.com/slack-go/slack"
    "github.com/slack-go/slack/socketmode"
    "github.com/stanier/stoa/internal/logging"
)

type SlackChannel struct {
    client   *slack.Client
    socket   *socketmode.Client
    msgChan  chan Message
    stopChan chan struct{}
}

type SlackConfig struct {
    Token    string
    AppToken string
}

func NewSlackChannel(config SlackConfig) *SlackChannel {
    client := slack.New(
        config.Token,
        slack.OptionAppLevelToken(config.AppToken),
    )

    socket := socketmode.New(client)

    return &SlackChannel{
        client:   client,
        socket:   socket,
        msgChan:  make(chan Message, 10),
        stopChan: make(chan struct{}),
    }
}

func (sc *SlackChannel) Start(ctx context.Context) error {
    go sc.handleEvents()
    go sc.socket.Run()

    logging.Info("Slack channel started")
    return nil
}

func (sc *SlackChannel) Stop(ctx context.Context) error {
    close(sc.stopChan)
    close(sc.msgChan)
    logging.Info("Slack channel stopped")
    return nil
}

func (sc *SlackChannel) IsConnected() bool {
    return sc.socket != nil
}

func (sc *SlackChannel) Messages() <-chan Message {
    return sc.msgChan
}

func (sc *SlackChannel) SendResponse(originalMsg Message, response string, err error) error {
    // Extract original Slack message from Raw field
    slackMsg, ok := originalMsg.Raw.(*slack.MessageEvent)
    if !ok {
        return fmt.Errorf("invalid message type for Slack channel")
    }

    if err != nil {
        response = fmt.Sprintf("Error: %v", err)
    }

    _, _, err = sc.client.PostMessage(
        slackMsg.Channel,
        slack.MsgOptionText(response, false),
        slack.MsgOptionTS(slackMsg.ThreadTimestamp),
    )

    return err
}

func (sc *SlackChannel) Name() string {
    return "slack"
}

func (sc *SlackChannel) handleEvents() {
    for {
        select {
        case <-sc.stopChan:
            return
        case event := <-sc.socket.Events:
            switch event.Type {
            case socketmode.EventTypeEventsAPI:
                eventsAPI := event.Data.(slackevents.EventsAPIEvent)

                switch innerEvent := eventsAPI.InnerEvent.Data.(type) {
                case *slackevents.MessageEvent:
                    // Convert to generic message
                    genericMsg := Message{
                        ID:         innerEvent.TimeStamp,
                        Content:    innerEvent.Text,
                        SenderID:   innerEvent.User,
                        SenderName: innerEvent.Username,
                        Type:       "channel",
                        Recipient:  innerEvent.Channel,
                        Raw:        innerEvent, // Store original
                    }

                    select {
                    case sc.msgChan <- genericMsg:
                    case <-sc.stopChan:
                        return
                    }
                }

                sc.socket.Ack(*event.Request)
            }
        }
    }
}
```

### 3. Update the Factory

Add the new channel to `internal/channels/factory.go`:

```go
func NewChannel(cfg *config.Config) (Channel, error) {
    // Check in priority order
    if cfg.Zulip.Enabled {
        return newZulipFromConfig(cfg), nil
    }

    // Add your new channel
    if cfg.Slack.Enabled {
        return newSlackFromConfig(cfg), nil
    }

    if cfg.Discord.Enabled {
        return newDiscordFromConfig(cfg), nil
    }

    return nil, fmt.Errorf("no channel configured")
}

func newSlackFromConfig(cfg *config.Config) Channel {
    return NewSlackChannel(SlackConfig{
        Token:    cfg.Slack.Token,
        AppToken: cfg.Slack.AppToken,
    })
}
```

### 4. Update Documentation

Add your channel to:
- `docs/ZULIP_SERVER.md` - Mark as implemented
- `cmd/server/main.go` - Update help text

### 5. Test

Build and test your channel:

```bash
make build-server

# Test with your channel
SLACK_ENABLED=true \
SLACK_TOKEN=xoxb-xxx \
SLACK_APP_TOKEN=xapp-xxx \
LLM_PROVIDER=anthropic \
ANTHROPIC_API_KEY=sk-ant-xxx \
./server --log-level debug
```

## Channel-Specific Features

Some channels have unique features that don't map to the generic interface:

### Reactions (Zulip)

Zulip supports emoji reactions. The `ZulipChannel` handles this internally:
- Adds 👀 when message received
- Changes to ✏️ while generating
- Removes when done

Other channels without this feature simply don't implement it.

### Threads (Slack, Discord)

For threaded messages, use the `Topic` field to track thread IDs:

```go
genericMsg := Message{
    Type:   "thread",
    Topic:  threadID,  // Slack thread_ts, Discord thread ID, etc.
    // ...
}
```

When sending responses, check for the `Topic` field to reply in-thread.

### Status Updates (Optional)

If your channel needs to report connection status, you can add a `StatusUpdates()` method like Zulip does:

```go
type StatusUpdate struct {
    ChannelName string
    Connected   bool
    Error       error
}

func (sc *SlackChannel) StatusUpdates() <-chan StatusUpdate {
    return sc.statusCh
}
```

Then update `cmd/server/main.go` to handle status updates for your channel type.

## Best Practices

1. **Error Handling**: Always handle API errors gracefully with retries/backoff
2. **Rate Limiting**: Respect platform rate limits
3. **Concurrency**: Use goroutines and channels for event handling
4. **Logging**: Use `internal/logging` for consistent logging
5. **Context**: Honor context cancellation for clean shutdown
6. **Testing**: Write unit tests for your channel implementation

## Example Channels to Add

### Slack
- SDK: `github.com/slack-go/slack`
- Auth: OAuth tokens
- Events: Socket mode for real-time events

### Discord
- SDK: `github.com/bwmarrin/discordgo`
- Auth: Bot token
- Events: Gateway WebSocket

### Matrix
- SDK: `maunium.net/go/mautrix`
- Auth: Access token
- Events: Sync API

### Microsoft Teams
- SDK: Microsoft Graph API
- Auth: Azure AD OAuth
- Events: Change notifications webhooks

## Questions?

See `internal/channels/zulip.go` for a complete reference implementation.
