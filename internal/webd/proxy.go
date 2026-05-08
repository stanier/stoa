package webd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gorilla/websocket"
	xws "golang.org/x/net/websocket"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/logging"
)

// handleWSProxy upgrades the browser connection to WebSocket and proxies
// bidirectionally to agentd, injecting the session Bearer token.
//
// URL pattern: /ws/{sessionID}
func (s *Server) handleWSProxy(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/ws/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}

	token := s.tokenFromCookie(r)
	if token == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	// Dial agentd using x/net/websocket — must match what agentd serves with.
	agentdWS := strings.Replace(s.cfg.AgentdURL, "http://", "ws://", 1)
	agentdWS = strings.Replace(agentdWS, "https://", "wss://", 1)
	agentdWS += "/sessions/" + sessionID + "/ws"

	wsCfg, err := xws.NewConfig(agentdWS, s.cfg.AgentdURL)
	if err != nil {
		logging.Errorf("webd: ws proxy: build agentd config: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	wsCfg.Header = http.Header{}
	wsCfg.Header.Set("Authorization", "Bearer "+token)

	agentConn, err := xws.DialConfig(wsCfg)
	if err != nil {
		logging.Errorf("webd: ws proxy: dial agentd %s: %v", agentdWS, err)
		http.Error(w, "could not connect to agent", http.StatusBadGateway)
		return
	}
	defer agentConn.Close()

	// Upgrade the browser connection.
	browserConn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Errorf("webd: ws proxy: upgrade browser connection: %v", err)
		return
	}
	defer browserConn.Close()

	// Proxy bidirectionally until either side closes.
	errc := make(chan error, 2)

	// agentd → browser
	go func() {
		for {
			var msg string
			if err := xws.Message.Receive(agentConn, &msg); err != nil {
				errc <- err
				return
			}
			if err := browserConn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
				errc <- err
				return
			}
		}
	}()

	// browser → agentd
	go func() {
		for {
			_, data, err := browserConn.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			data = s.processAttachments(r.Context(), data)
			if err := xws.Message.Send(agentConn, string(data)); err != nil {
				errc <- err
				return
			}
		}
	}()

	if err := <-errc; err != nil {
		logging.Debugf("webd: ws proxy: session %s closed: %v", sessionID, err)
	}
}

// handleSessionsProxy proxies /api/sessions[/...] to agentd.
// For POST /api/sessions, injects user_id from the auth context into the body.
func (s *Server) handleSessionsProxy(w http.ResponseWriter, r *http.Request) {
	token := s.tokenFromCookie(r)
	agentPath := strings.TrimPrefix(r.URL.Path, "/api")
	targetURL := s.cfg.AgentdURL + agentPath
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// Inject user_id and user's MCP prefs into session creation requests.
	if r.Method == http.MethodPost && agentPath == "/sessions" {
		userID := UserIDFromContext(r.Context())
		if userID != "" {
			// Read the original body, merge user_id, and replace r.Body.
			var body map[string]interface{}
			if r.Body != nil {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
					http.Error(w, "invalid request body", http.StatusBadRequest)
					return
				}
			}
			if body == nil {
				body = make(map[string]interface{})
			}
			body["user_id"] = userID

			// Inject the per-user LLM key so agentd routes calls through inference-gateway
			// with user-scoped quota enforcement.
			if llmKey := s.llmKeyFromCookie(r); llmKey != "" {
				body["llm_key"] = llmKey
			}

			// Apply the user's saved MCP prefs unless the caller already supplied
			// an explicit allowed_tools list.
			if _, alreadySet := body["allowed_tools"]; !alreadySet {
				token := s.tokenFromCookie(r)
				if allowed := s.computeAllowedTools(r.Context(), token); allowed != nil {
					body["allowed_tools"] = allowed
				}
			}

			enriched, err := json.Marshal(body)
			if err == nil {
				r = r.Clone(r.Context())
				r.Body = io.NopCloser(bytes.NewReader(enriched))
				r.ContentLength = int64(len(enriched))
				r.Header.Set("Content-Type", "application/json")
			}
		}
	}

	s.proxyToAgentd(w, r, targetURL, token)
}

// handleModelsProxy proxies GET /api/models → agentd /models.
func (s *Server) handleModelsProxy(w http.ResponseWriter, r *http.Request) {
	token := s.tokenFromCookie(r)
	s.proxyToAgentd(w, r, s.cfg.AgentdURL+"/models", token)
}

// handleToolsProxy proxies GET /api/tools → agentd /tools.
func (s *Server) handleToolsProxy(w http.ResponseWriter, r *http.Request) {
	token := s.tokenFromCookie(r)
	s.proxyToAgentd(w, r, s.cfg.AgentdURL+"/tools", token)
}

// handleAgentsProxy proxies /api/agents[/*] → agentd /agents[/*].
func (s *Server) handleAgentsProxy(w http.ResponseWriter, r *http.Request) {
	token := s.tokenFromCookie(r)
	tail := strings.TrimPrefix(r.URL.Path, "/api/agents")
	targetURL := s.cfg.AgentdURL + "/agents" + tail
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}
	s.proxyToAgentd(w, r, targetURL, token)
}

// processAttachments intercepts send_message WebSocket frames that carry attachment
// refs from the browser. Image attachments are fetched and inlined as base64
// ImageParts; other file types get a text note appended so the agent can call a
// retrieval tool. The Attachments field is cleared before forwarding to agentd.
// Non-send_message frames are returned unchanged.
func (s *Server) processAttachments(ctx context.Context, frame []byte) []byte {
	var msg agentproto.ClientMessage
	if err := json.Unmarshal(frame, &msg); err != nil {
		return frame
	}
	if msg.Type != agentproto.MessageTypeSendMessage || msg.SendMessage == nil || len(msg.SendMessage.Attachments) == 0 {
		return frame
	}

	payload := msg.SendMessage
	var textNotes []string

	for _, ref := range payload.Attachments {
		isImage := strings.HasPrefix(ref.MimeType, "image/")
		if isImage && s.cfg.AutoEmbedAttachments {
			data, err := s.fetchAttachmentBytes(ctx, ref)
			if err != nil {
				logging.Warnf("webd: attachment embed failed for %q: %v", ref.Name, err)
				textNotes = append(textNotes, attachmentTextNote(ref))
				continue
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			payload.ImageParts = append(payload.ImageParts, agentproto.ImagePart{
				URL: fmt.Sprintf("data:%s;base64,%s", ref.MimeType, encoded),
			})
		} else {
			textNotes = append(textNotes, attachmentTextNote(ref))
		}
	}

	payload.Attachments = nil

	if len(textNotes) > 0 {
		note := "[Attachments]\n" + strings.Join(textNotes, "\n")
		if payload.Content != "" {
			payload.Content = note + "\n\n" + payload.Content
		} else {
			payload.Content = note
		}
	}

	out, err := json.Marshal(msg)
	if err != nil {
		return frame
	}
	return out
}

// fetchAttachmentBytes retrieves the raw bytes for an attachment ref.
func (s *Server) fetchAttachmentBytes(ctx context.Context, ref agentproto.AttachmentRef) ([]byte, error) {
	if ref.AttachmentID != "" {
		return s.resolveS3Attachment(ctx, ref.AttachmentID)
	}
	if ref.Path != "" {
		return resolveLocalAttachment(ref.Path)
	}
	return nil, fmt.Errorf("attachment has neither attachment_id nor path")
}

// attachmentTextNote formats a retrieval hint for non-embedded attachments.
// For S3 attachments it names the read_attachment tool explicitly.
// For local-filesystem attachments it converts the absolute webd path to the
// path the MCP filesystem tool sees (same volume, different mount point).
func attachmentTextNote(ref agentproto.AttachmentRef) string {
	if ref.AttachmentID != "" {
		return fmt.Sprintf("- %s (call read_attachment with attachment_id=%s, size: %d bytes)",
			ref.Name, ref.AttachmentID, ref.Size)
	}
	// Local filesystem mode: webd writes to WEBD_UPLOAD_DIR which sits inside
	// the mcp-workspace volume. The MCP filesystem container mounts the same
	// volume at /mcp/root, so strip the workspace root to get the relative path.
	uploadDir := envOr("WEBD_UPLOAD_DIR", "/tmp/mcp-workspace/"+uploadSubDir)
	workspaceDir := filepath.Dir(uploadDir)
	relPath := strings.TrimPrefix(ref.Path, workspaceDir+string(filepath.Separator))
	return fmt.Sprintf("- %s (call mcp_filesystem_read_file with path=%s, size: %d bytes)",
		ref.Name, relPath, ref.Size)
}

// proxyToAgentd forwards an HTTP request to agentd, injecting the Bearer token.
func (s *Server) proxyToAgentd(w http.ResponseWriter, r *http.Request, targetURL, token string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		logging.Errorf("webd: proxy to agentd %s: %v", targetURL, err)
		http.Error(w, "agent unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
