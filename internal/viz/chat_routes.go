package viz

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/chat"
)

// Lazily initializes and returns the chat Manager, broadcasting events to connected WebSocket clients.
func (s *Server) chatManager() (*chat.Manager, error) {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	if !s.chatInit {
		s.chatInit = true
		s.chatMgr, s.chatErr = chat.NewManager(s.dbPath, ".", func(event chat.Event) {
			s.ws.Broadcast(WebSocketEvent{Type: "chat_event", Payload: event})
		})
	}
	return s.chatMgr, s.chatErr
}

// Close shuts the chat manager down, waiting for the runs it started to stop.
//
// The chat manager answers a request by starting work that outlives it, and that work writes to
// the chat store and the topology database throughout. An embedder that is finished with a
// Server -- a test, a host process replacing it, anything that is about to remove or reopen the
// workspace -- has to be able to say "stop, and tell me when you have", and until Manager.Close
// existed there was nothing to call. `Listen` does not call this: it owns the process for its
// whole life and exits with it.
func (s *Server) Close() error {
	s.chatMu.Lock()
	mgr := s.chatMgr
	s.chatMu.Unlock()
	if mgr == nil {
		return nil
	}
	return mgr.Close()
}

// Router for /api/chat endpoints that delegates to provider, agents, sessions, or workflow handlers.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	mgr, err := s.chatManager()
	if err != nil {
		writeError(w, err)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/chat")
	path = strings.Trim(path, "/")
	parts := []string{}
	if path != "" {
		parts = strings.Split(path, "/")
	}

	if len(parts) == 0 {
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	switch parts[0] {
	case "provider":
		s.handleChatProvider(w, r, mgr)
	case "agents":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		profiles, err := mgr.AgentProfiles()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, profiles)
	case "sessions":
		s.handleChatSessions(w, r, mgr, parts[1:])
	case "workflows":
		s.handleChatWorkflow(w, r, mgr)
	default:
		writeError(w, fmt.Errorf("unknown chat route: %s", parts[0]))
	}
}

// HTTP handler for chat provider settings, supporting GET to retrieve and POST/PUT to configure providers.
func (s *Server) handleChatProvider(w http.ResponseWriter, r *http.Request, mgr *chat.Manager) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, mgr.ProviderSettings())
	case http.MethodPost, http.MethodPut:
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeError(w, err)
			return
		}
		if _, ok := raw["providers"]; ok {
			var config chat.ProviderConfig
			data, _ := json.Marshal(raw)
			if err := json.Unmarshal(data, &config); err != nil {
				writeError(w, err)
				return
			}
			state, err := mgr.SetProviderConfig(config)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, state)
			return
		}
		var settings chat.ProviderSettings
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &settings); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, mgr.SetProvider(settings))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// HTTP handler for chat session CRUD operations: list, create, get, delete, update, stop, send messages, regenerate, approve, answer questions, and resume task groups.
func (s *Server) handleChatSessions(w http.ResponseWriter, r *http.Request, mgr *chat.Manager, parts []string) {
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, mgr.ListSessions())
		case http.MethodPost:
			var req chat.CreateSessionRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			session, err := mgr.CreateSession(req.Agent, req.Title)
			if err != nil {
				writeError(w, err)
				return
			}
			if strings.TrimSpace(req.Content) != "" {
				session, err = mgr.Send(session.ID, chat.SendRequest{Content: req.Content, Mode: req.Mode, ApprovalMode: req.ApprovalMode, Provider: req.Provider})
				if err != nil {
					writeError(w, err)
					return
				}
			}
			writeJSON(w, session)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	sessionID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			session, err := mgr.GetSession(sessionID)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, session)
		case http.MethodDelete:
			if err := mgr.DeleteSession(sessionID); err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, map[string]string{"status": "deleted", "id": sessionID})
		case http.MethodPatch:
			var body struct {
				Title  *string `json:"title"`
				Pinned *bool   `json:"pinned"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, err)
				return
			}
			session, err := mgr.UpdateSession(sessionID, body.Title, body.Pinned)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, session)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	switch parts[1] {
	case "stop":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		session, err := mgr.StopSession(sessionID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, session)
	case "messages":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if len(parts) >= 4 && parts[3] == "edit" {
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, err)
				return
			}
			session, err := mgr.RewindAndResend(sessionID, parts[2], body.Content)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, session)
			return
		}
		var req chat.SendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, err)
			return
		}
		session, err := mgr.Send(sessionID, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, session)
	case "regenerate":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		session, err := mgr.RegenerateLast(sessionID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, session)
	case "approvals":
		if r.Method != http.MethodPost || len(parts) < 3 {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Approved bool `json:"approved"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, err)
			return
		}
		session, err := mgr.ResolveApproval(sessionID, parts[2], body.Approved)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, session)
	case "questions":
		if r.Method != http.MethodPost || len(parts) < 3 {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Answer string `json:"answer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, err)
			return
		}
		session, err := mgr.AnswerQuestion(sessionID, parts[2], body.Answer)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, session)
	case "task-groups":
		if r.Method != http.MethodPost || len(parts) < 4 || parts[3] != "resume" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		session, err := mgr.ResumeTaskGroup(sessionID, parts[2])
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, session)
	default:
		writeError(w, fmt.Errorf("unknown chat session route: %s", parts[1]))
	}
}

// HTTP handler that starts a workflow job and returns the job ID.
func (s *Server) handleChatWorkflow(w http.ResponseWriter, r *http.Request, mgr *chat.Manager) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req chat.WorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, err)
		return
	}
	jobID, err := mgr.StartWorkflow(req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]string{"job_id": jobID})
}
