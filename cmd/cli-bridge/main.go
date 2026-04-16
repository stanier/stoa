// Command cli-bridge connects to a running agentd server and allows multi-turn
// conversations from the terminal or programmatic use (e.g. cron jobs).
//
// Interactive mode (default when stdin is a TTY):
//
//	cli-bridge [flags]
//	> Hello!                  (type messages at the prompt)
//	> /sessions               (list active sessions)
//	> /quit                   (exit)
//
// Single-shot mode (stdin is not a TTY, or -message flag is used):
//
//	cli-bridge -message "What is 2+2?"
//	echo "What is 2+2?" | cli-bridge
//	cli-bridge "What is 2+2?"   (positional arg also works)
//
// JSON event mode (for scripting):
//
//	cli-bridge -json -message "Hello"   # each event is a JSON line on stdout
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"golang.org/x/term"
)

const (
	defaultAddress = "http://localhost:7777"
	permTimeout    = 120 * time.Second
	connectTimeout = 15 * time.Second
)

func main() {
	address := flag.String("address", "", "agentd URL (env: AGENT_ADDRESS, default: http://localhost:7777)")
	persona := flag.String("persona", "", "LLM persona (e.g. coder, analyzer)")
	sessionID := flag.String("session", "", "Reuse existing session ID")
	message := flag.String("message", "", "Message to send (enables single-shot mode)")
	noStream := flag.Bool("no-stream", false, "Suppress streaming; print only final response")
	autoApprove := flag.Bool("auto-approve", false, "Auto-approve all permission requests")
	autoDeny := flag.Bool("auto-deny", false, "Auto-deny all permission requests")
	jsonMode := flag.Bool("json", false, "Output events as JSON lines (implies -no-stream)")
	quiet := flag.Bool("quiet", false, "Suppress tool invocation and mention notices")
	thinking := flag.Bool("thinking", false, "Show model reasoning in output")
	helpFlag := flag.Bool("help", false, "Show help")

	flag.Parse()

	if *helpFlag {
		printHelp()
		os.Exit(0)
	}

	// Resolve address: flag → env → default.
	addr := *address
	if addr == "" {
		addr = os.Getenv("AGENT_ADDRESS")
	}
	if addr == "" {
		addr = defaultAddress
	}

	// Positional args are treated as a message.
	if flag.NArg() > 0 && *message == "" {
		*message = strings.Join(flag.Args(), " ")
	}

	// JSON mode implies no-stream (we print structured events, not incremental text).
	if *jsonMode {
		*noStream = true
	}

	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	singleShot := !isTTY || *message != ""

	cfg := runConfig{
		address:     addr,
		persona:     *persona,
		sessionID:   *sessionID,
		message:     *message,
		noStream:    *noStream,
		autoApprove: *autoApprove,
		autoDeny:    *autoDeny,
		jsonMode:    *jsonMode,
		quiet:       *quiet,
		thinking:    *thinking,
		singleShot:  singleShot,
	}

	// In non-interactive mode without explicit perm policy, default to auto-approve.
	if singleShot && !cfg.autoApprove && !cfg.autoDeny {
		cfg.autoApprove = true
		if !cfg.jsonMode {
			fmt.Fprintln(os.Stderr, "[cli-bridge] non-interactive mode: auto-approving permission requests")
		}
	}

	client := agentproto.NewWSClientWithToken(addr, os.Getenv("AGENTD_API_KEY"))

	// Create or reuse session.
	sid := cfg.sessionID
	if sid == "" {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		var err error
		sid, err = client.CreateSession(ctx, agentproto.SessionConfig{Persona: cfg.persona})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cli-bridge: create session: %v\n", err)
			os.Exit(1)
		}
		if !cfg.jsonMode {
			fmt.Fprintf(os.Stderr, "[cli-bridge] session %s\n", sid)
		}
	}

	// Connect WebSocket.
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	conn, err := client.Connect(ctx, sid)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cli-bridge: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if singleShot {
		os.Exit(runSingleShot(cfg, client, sid, conn))
	}
	os.Exit(runInteractive(cfg, client, sid, conn))
}

// runConfig holds parsed flag values.
type runConfig struct {
	address     string
	persona     string
	sessionID   string
	message     string
	noStream    bool
	autoApprove bool
	autoDeny    bool
	jsonMode    bool
	quiet       bool
	thinking    bool
	singleShot  bool
}

// ---- Single-shot mode -------------------------------------------------------

// runSingleShot sends one message, waits for EventTypeDone, and exits.
// It returns an exit code (0=ok, 1=error).
func runSingleShot(cfg runConfig, client *agentproto.WSClient, sid string, conn agentproto.ClientConn) int {
	msg := cfg.message
	if msg == "" {
		// Read all of stdin as the message.
		scanner := bufio.NewScanner(os.Stdin)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		msg = strings.Join(lines, "\n")
	}
	if strings.TrimSpace(msg) == "" {
		fmt.Fprintln(os.Stderr, "cli-bridge: no message provided")
		return 1
	}

	if err := conn.Send(agentproto.ClientMessage{
		Type:        agentproto.MessageTypeSendMessage,
		SendMessage: &agentproto.SendMessagePayload{Content: msg},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "cli-bridge: send: %v\n", err)
		return 1
	}

	var (
		streamBuf strings.Builder
		exitCode  int
	)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	for {
		select {
		case <-sigCh:
			fmt.Fprintln(os.Stderr, "\ncli-bridge: interrupted")
			return 1

		case e, ok := <-conn.Events():
			if !ok {
				// Channel closed.
				if !cfg.jsonMode && streamBuf.Len() > 0 {
					fmt.Print(streamBuf.String())
				}
				return exitCode
			}

			if cfg.jsonMode {
				emitJSON(e)
			}

			switch e.Type {
			case agentproto.EventTypeChunk:
				if e.Chunk != nil {
					if cfg.thinking && e.Chunk.Thinking != "" && !cfg.noStream {
						fmt.Fprint(os.Stderr, e.Chunk.Thinking)
					}
					if !cfg.noStream {
						fmt.Print(e.Chunk.Content)
					}
					streamBuf.WriteString(e.Chunk.Content)
				}

			case agentproto.EventTypeDone:
				content := streamBuf.String()
				if e.Done != nil && e.Done.FullContent != "" {
					content = e.Done.FullContent
				}
				if cfg.noStream && !cfg.jsonMode {
					if cfg.thinking && e.Done != nil && e.Done.FullThinking != "" {
						fmt.Fprintf(os.Stderr, "[thinking]\n%s\n[/thinking]\n", e.Done.FullThinking)
					}
					fmt.Print(content)
				} else if !cfg.noStream {
					// Streaming already printed; ensure newline.
					if len(content) > 0 && !strings.HasSuffix(content, "\n") {
						fmt.Println()
					}
				}
				return exitCode

			case agentproto.EventTypePermRequest:
				exitCode = handlePermSingleShot(cfg, conn, e)
				if exitCode != 0 {
					return exitCode
				}

			case agentproto.EventTypePermMention:
				if !cfg.quiet && !cfg.jsonMode && e.PermMention != nil {
					fmt.Fprintf(os.Stderr, "[tool] %s\n", e.PermMention.ToolName)
				}

			case agentproto.EventTypeToolInvoked:
				if !cfg.quiet && !cfg.jsonMode && e.ToolInvoked != nil {
					fmt.Fprintf(os.Stderr, "[tool] %s ...\n", e.ToolInvoked.ToolName)
				}

			case agentproto.EventTypeToolResult:
				if !cfg.quiet && !cfg.jsonMode && e.ToolResult != nil {
					status := "ok"
					if e.ToolResult.IsError {
						status = "error"
					}
					fmt.Fprintf(os.Stderr, "[tool] %s → %s\n", e.ToolResult.ToolName, status)
				}

			case agentproto.EventTypeCompaction:
				if !cfg.jsonMode && e.Compaction != nil {
					fmt.Fprintf(os.Stderr, "[compaction] %d → %d messages\n",
						e.Compaction.MessagesBefore, e.Compaction.MessagesAfter)
				}

			case agentproto.EventTypeError:
				if e.Error != nil {
					fmt.Fprintf(os.Stderr, "cli-bridge: error: %s\n", e.Error.Message)
					if e.Error.Fatal {
						return 1
					}
				}

			case agentproto.EventTypeAgentSpawned:
				if !cfg.quiet && !cfg.jsonMode && e.AgentSpawned != nil {
					fmt.Fprintf(os.Stderr, "[agent] spawned %s (%s)\n",
						e.AgentSpawned.AgentID, e.AgentSpawned.Persona)
				}

			case agentproto.EventTypeAgentCompleted:
				if !cfg.quiet && !cfg.jsonMode && e.AgentCompleted != nil {
					fmt.Fprintf(os.Stderr, "[agent] completed %s\n", e.AgentCompleted.AgentID)
				}
			}
		}
	}
}

func handlePermSingleShot(cfg runConfig, conn agentproto.ClientConn, e agentproto.Event) int {
	if e.PermRequest == nil {
		return 0
	}
	req := e.PermRequest

	if !cfg.jsonMode {
		fmt.Fprintf(os.Stderr, "[perm] %s: %s\n", req.ToolName, req.Description)
	}

	approved := cfg.autoApprove
	if cfg.autoDeny {
		approved = false
	}

	if err := conn.Send(agentproto.ClientMessage{
		Type: agentproto.MessageTypePermResponse,
		PermResponse: &agentproto.PermResponsePayload{
			RequestID: req.RequestID,
			Approved:  approved,
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "cli-bridge: perm response: %v\n", err)
		return 1
	}
	return 0
}

// ---- Interactive mode -------------------------------------------------------

type interactiveState int

const (
	stateIdle        interactiveState = iota
	stateStreaming                     // waiting for EventTypeDone
	statePermPending                   // waiting for y/n from user
)

type pendingPerm struct {
	req *agentproto.PermRequestEvent
}

// runInteractive runs the multi-turn REPL. Returns exit code.
func runInteractive(cfg runConfig, client *agentproto.WSClient, sid string, conn agentproto.ClientConn) int {
	fmt.Fprintf(os.Stderr, "Connected. Session: %s  (type /help for commands)\n", sid)
	printPrompt()

	// Read stdin lines in a goroutine so the event select loop isn't blocked.
	inputCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputCh <- scanner.Text()
		}
		close(inputCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var (
		state         = stateIdle
		pending       *pendingPerm
		streamBuf     strings.Builder
		lastWasStream bool // track whether we need a newline before the next prompt
	)

	for {
		select {
		case <-sigCh:
			fmt.Fprintln(os.Stderr, "\nInterrupted.")
			return 0

		case line, ok := <-inputCh:
			if !ok {
				// EOF on stdin.
				return 0
			}

			switch state {
			case stateIdle:
				line = strings.TrimSpace(line)
				if line == "" {
					printPrompt()
					continue
				}
				if strings.HasPrefix(line, "/") {
					handled, exitCode := handleCommand(line, cfg, client, &sid, &conn, &state)
					if handled {
						if exitCode < 0 {
							// /quit
							return 0
						}
						if state == stateIdle {
							printPrompt()
						}
						continue
					}
					// Falls through as a regular message if not a known command.
				}
				if err := sendMessage(conn, line); err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					printPrompt()
					continue
				}
				state = stateStreaming
				streamBuf.Reset()
				lastWasStream = false

			case statePermPending:
				answer := strings.ToLower(strings.TrimSpace(line))
				approved := answer == "y" || answer == "yes"
				if pending != nil {
					if err := conn.Send(agentproto.ClientMessage{
						Type: agentproto.MessageTypePermResponse,
						PermResponse: &agentproto.PermResponsePayload{
							RequestID: pending.req.RequestID,
							Approved:  approved,
						},
					}); err != nil {
						fmt.Fprintf(os.Stderr, "error sending perm response: %v\n", err)
					}
					pending = nil
				}
				state = stateStreaming
				streamBuf.Reset()

			case stateStreaming:
				// Ignore input while streaming; agent is busy.
				fmt.Fprintln(os.Stderr, "(busy — waiting for response)")
			}

		case e, ok := <-conn.Events():
			if !ok {
				fmt.Fprintln(os.Stderr, "Connection closed.")
				return 1
			}

			switch e.Type {
			case agentproto.EventTypeChunk:
				if e.Chunk != nil {
					if cfg.thinking && e.Chunk.Thinking != "" {
						if !cfg.noStream {
							fmt.Fprint(os.Stderr, e.Chunk.Thinking)
						}
					}
					if !cfg.noStream {
						fmt.Print(e.Chunk.Content)
						lastWasStream = true
					}
					streamBuf.WriteString(e.Chunk.Content)
				}

			case agentproto.EventTypeDone:
				content := streamBuf.String()
				if e.Done != nil && e.Done.FullContent != "" {
					content = e.Done.FullContent
				}
				if cfg.noStream {
					if cfg.thinking && e.Done != nil && e.Done.FullThinking != "" {
						fmt.Fprintf(os.Stderr, "\n[thinking]\n%s\n[/thinking]\n", e.Done.FullThinking)
					}
					fmt.Println(content)
				} else if lastWasStream {
					if len(content) > 0 && !strings.HasSuffix(content, "\n") {
						fmt.Println()
					}
				}
				lastWasStream = false
				streamBuf.Reset()
				state = stateIdle
				printPrompt()

			case agentproto.EventTypePermRequest:
				if e.PermRequest == nil {
					continue
				}
				req := e.PermRequest

				if cfg.autoApprove || cfg.autoDeny {
					approved := cfg.autoApprove
					_ = conn.Send(agentproto.ClientMessage{
						Type: agentproto.MessageTypePermResponse,
						PermResponse: &agentproto.PermResponsePayload{
							RequestID: req.RequestID,
							Approved:  approved,
						},
					})
					if !cfg.quiet {
						verdict := "approved"
						if !approved {
							verdict = "denied"
						}
						fmt.Fprintf(os.Stderr, "\n[perm] %s auto-%s\n", req.ToolName, verdict)
					}
				} else {
					// Prompt user interactively.
					paramsStr := ""
					if len(req.Params) > 0 {
						paramsStr = string(req.Params)
					}
					fmt.Fprintf(os.Stderr,
						"\n\033[33m[permission request]\033[0m\n  tool: %s\n  desc: %s\n  params: %s\nApprove? [y/N]: ",
						req.ToolName, req.Description, paramsStr)
					pending = &pendingPerm{req: req}
					state = statePermPending
				}

			case agentproto.EventTypePermMention:
				if !cfg.quiet && e.PermMention != nil {
					fmt.Fprintf(os.Stderr, "\n[tool] %s\n", e.PermMention.ToolName)
					if state == stateIdle {
						printPrompt()
					}
				}

			case agentproto.EventTypeToolInvoked:
				if !cfg.quiet && e.ToolInvoked != nil {
					fmt.Fprintf(os.Stderr, "\n[tool] calling %s...\n", e.ToolInvoked.ToolName)
				}

			case agentproto.EventTypeToolResult:
				if !cfg.quiet && e.ToolResult != nil {
					status := "ok"
					if e.ToolResult.IsError {
						status = "error"
					}
					fmt.Fprintf(os.Stderr, "[tool] %s → %s\n", e.ToolResult.ToolName, status)
				}

			case agentproto.EventTypeCompaction:
				if e.Compaction != nil {
					fmt.Fprintf(os.Stderr, "\n[compaction] history summarized (%d → %d messages)\n",
						e.Compaction.MessagesBefore, e.Compaction.MessagesAfter)
					if state == stateIdle {
						printPrompt()
					}
				}

			case agentproto.EventTypeAgentSpawned:
				if !cfg.quiet && e.AgentSpawned != nil {
					fmt.Fprintf(os.Stderr, "\n[agent] spawned %s (%s): %s\n",
						e.AgentSpawned.AgentID, e.AgentSpawned.Persona, e.AgentSpawned.Task)
				}

			case agentproto.EventTypeAgentCompleted:
				if !cfg.quiet && e.AgentCompleted != nil {
					status := "completed"
					if e.AgentCompleted.IsError {
						status = "failed"
					}
					fmt.Fprintf(os.Stderr, "[agent] %s %s\n", e.AgentCompleted.AgentID, status)
				}

			case agentproto.EventTypeError:
				if e.Error != nil {
					fmt.Fprintf(os.Stderr, "\n\033[31merror: %s\033[0m\n", e.Error.Message)
					if e.Error.Fatal {
						fmt.Fprintln(os.Stderr, "Session ended.")
						return 1
					}
					state = stateIdle
					printPrompt()
				}
			}
		}
	}
}

// handleCommand processes slash commands. Returns (handled bool, exitCode int).
// exitCode -1 means quit.
func handleCommand(
	line string,
	cfg runConfig,
	client *agentproto.WSClient,
	sid *string,
	conn *agentproto.ClientConn,
	state *interactiveState,
) (bool, int) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false, 0
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/quit", "/exit", "/q":
		fmt.Fprintln(os.Stderr, "Goodbye.")
		return true, -1

	case "/help":
		fmt.Fprint(os.Stderr, interactiveHelp)
		return true, 0

	case "/sessions":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		sessions, err := client.ListSessions(ctx)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return true, 0
		}
		if len(sessions) == 0 {
			fmt.Fprintln(os.Stderr, "(no active sessions)")
		}
		for _, s := range sessions {
			marker := " "
			if s.ID == *sid {
				marker = "*"
			}
			fmt.Fprintf(os.Stderr, " %s [%s] persona=%-10s msgs=%-3d created=%s\n",
				marker, s.ID, s.Persona, s.MessageCount, s.CreatedAt.Format(time.RFC3339))
		}
		return true, 0

	case "/session":
		if len(parts) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /session <id>")
			return true, 0
		}
		newID := parts[1]
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		newConn, err := client.Connect(ctx, newID)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error connecting to session %s: %v\n", newID, err)
			return true, 0
		}
		(*conn).Close()
		*conn = newConn
		*sid = newID
		fmt.Fprintf(os.Stderr, "Switched to session %s\n", newID)
		return true, 0

	case "/new":
		persona := cfg.persona
		if len(parts) >= 2 {
			persona = parts[1]
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		newSID, err := client.CreateSession(ctx, agentproto.SessionConfig{Persona: persona})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating session: %v\n", err)
			return true, 0
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), connectTimeout)
		newConn, err := client.Connect(ctx2, newSID)
		cancel2()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error connecting to new session: %v\n", err)
			return true, 0
		}
		(*conn).Close()
		*conn = newConn
		*sid = newSID
		fmt.Fprintf(os.Stderr, "New session %s (persona: %q)\n", newSID, persona)
		return true, 0

	case "/clear":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.ClearSession(ctx, *sid)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "Conversation cleared.")
		}
		return true, 0

	case "/compact":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		result, err := client.CompactSession(ctx, *sid)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Compaction done: %d → %d messages (%d → %d tokens)\n",
				result.MessagesBefore, result.MessagesAfter,
				result.TokensBefore, result.TokensAfter)
		}
		return true, 0

	case "/tokens":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		tok, err := client.GetTokens(ctx, *sid)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "tokens: total=%d messages=%d\n", tok.TotalTokens, tok.TotalMessages)
		}
		return true, 0
	}

	return false, 0
}

// ---- Helpers ----------------------------------------------------------------

func sendMessage(conn agentproto.ClientConn, content string) error {
	return conn.Send(agentproto.ClientMessage{
		Type: agentproto.MessageTypeSendMessage,
		SendMessage: &agentproto.SendMessagePayload{Content: content},
	})
}

func printPrompt() {
	fmt.Fprint(os.Stderr, "> ")
}

func emitJSON(e agentproto.Event) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	fmt.Println(string(data))
}

func printHelp() {
	fmt.Print(fullHelp)
}

const fullHelp = `cli-bridge — terminal client for agentd

USAGE
  cli-bridge [flags]                  Interactive multi-turn REPL
  cli-bridge [flags] "message"        Single-shot (positional arg)
  cli-bridge -message "msg" [flags]   Single-shot (flag)
  echo "msg" | cli-bridge [flags]     Single-shot (piped stdin)

FLAGS
  -address string    agentd URL (env: AGENT_ADDRESS, default: http://localhost:7777)
  -persona string    LLM persona (e.g. coder, analyzer)
  -session string    Reuse an existing session ID
  -message string    Send a single message and exit
  -no-stream         Suppress streaming; print only the final response
  -auto-approve      Auto-approve all permission requests
  -auto-deny         Auto-deny all permission requests
  -json              Output events as newline-delimited JSON (implies -no-stream)
  -quiet             Suppress tool/perm notices
  -thinking          Show model reasoning (written to stderr)
  -help              Show this help

INTERACTIVE COMMANDS (lines starting with /)
  /quit, /exit, /q          Exit
  /sessions                 List active sessions on the server
  /session <id>             Switch to another session
  /new [persona]            Create and switch to a new session
  /clear                    Clear current session conversation
  /compact                  Trigger manual history compaction
  /tokens                   Show token usage for current session
  /help                     Show this help

EXAMPLES
  # Interactive session
  AGENT_ADDRESS=http://localhost:7777 cli-bridge

  # Ask a question and pipe the answer to jq
  cli-bridge -json -message "list all sessions" | jq .

  # Cron job: daily summary
  cli-bridge -quiet -message "Summarize what happened today" >> /var/log/daily.txt

  # Continue an existing session
  cli-bridge -session abc123 -message "continue where we left off"
`

const interactiveHelp = `Commands:
  /quit, /exit, /q     Exit
  /sessions            List sessions on the server
  /session <id>        Switch to another session
  /new [persona]       Create a new session
  /clear               Clear conversation history
  /compact             Compact history
  /tokens              Show token usage
  /help                Show this help
`
