package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm"
	"github.com/Rhuan-Marques/aracne/internal/llm/providers"
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// Central chat orchestrator managing sessions, tools, scanners, topology, and LLM agents.

type Manager struct {
	mu                sync.Mutex
	store             *Store
	workspace         string
	dbPath            string
	manager           *topology.TopologyManager
	scanners          *scanner.Registry
	config            *helper.Config
	registry          *tools.Registry
	agentToolRegistry *tools.Registry
	policy            PermissionPolicy
	emit              func(Event)

	sessions           map[string]*Session
	running            map[string]bool
	runningCancels     map[string]context.CancelFunc
	stopRequested      map[string]bool
	runningToolCalls   map[string]llm.ToolCall
	interruptedTools   map[string]bool
	runningTaskGroups  map[string]bool
	taskGroupCancels   map[string]context.CancelFunc
	stoppedTaskGroups  map[string]bool
	agentDir           string
	provider           ProviderSettings
	providerConfig     ProviderConfig
	providerConfigPath string

	// Background-run lifecycle. A manager answers a request by starting work that OUTLIVES
	// it -- a run loop, a task group, a forced workflow, a title generation -- and until Close
	// existed there was no way to stop that work or to know when it had stopped. The process
	// simply exited from under it, mid-write, into the chat store and the topology database;
	// in a test the temp directory was removed while it was still being written into, which is
	// what "TempDir RemoveAll cleanup: directory not empty" was.
	//
	// bgMu guards only these three, and is deliberately NOT m.mu: goBackground is called from
	// paths that hold no lock and from inside other background goroutines, and a Close that
	// waited while holding the manager's main lock could not be finished by the goroutines it
	// is waiting for.
	bgMu     sync.Mutex
	bgClosed bool
	bg       sync.WaitGroup
	bgActive atomic.Int64
}

// closeGrace bounds Close. Everything it waits for is cancellable, so the grace is a backstop
// against a goroutine that does not honour its context rather than an expected cost -- and it
// is a backstop rather than an infinite wait because Close is what a test cleanup and a server
// shutdown call, and neither may hang forever on a hung provider request.
const closeGrace = 10 * time.Second

// goBackground runs fn in a goroutine Close will wait for, and reports whether it started one.
//
// It refuses after Close: a run that begins while the manager is shutting down would write into
// a store that is going away, and -- the mechanical reason -- a WaitGroup.Add racing a Wait is
// a data race however the writes turn out. Callers that hold state for the goroutine to clear
// must undo it when this returns false; see startRun.
func (m *Manager) goBackground(fn func()) bool {
	m.bgMu.Lock()
	if m.bgClosed {
		m.bgMu.Unlock()
		return false
	}
	m.bg.Add(1)
	m.bgActive.Add(1)
	m.bgMu.Unlock()
	go func() {
		defer func() {
			m.bgActive.Add(-1)
			m.bg.Done()
		}()
		fn()
	}()
	return true
}

// Close stops every background run this manager started and waits for them to return.
//
// After it returns, nothing this manager owns is still writing: no run loop, no task group and
// none of its workers, no forced workflow, no title generation. That is the property a caller
// needs before it removes the workspace, closes the database, or exits -- and the property a
// test needs before its temp directory is deleted.
//
// It is idempotent, and safe to call while runs are in flight: every in-flight LLM call and
// task group is cancelled first, so the goroutines unwind rather than being waited out. An
// error means the grace expired with work still running, which is a goroutine ignoring its
// context -- worth reporting, never worth hanging for.
func (m *Manager) Close() error {
	m.bgMu.Lock()
	first := !m.bgClosed
	m.bgClosed = true
	m.bgMu.Unlock()
	if first {
		m.cancelAllRuns()
	}
	done := make(chan struct{})
	// chat:untracked -- this one waits ON the group, so tracking it would deadlock.
	go func() {
		m.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(closeGrace):
		return fmt.Errorf("chat: %d background run(s) still active %s after close", m.bgActive.Load(), closeGrace)
	}
}

// cancelAllRuns is StopSession's reach, applied to every session at once: refuse further LLM
// calls, cancel the ones in flight, and cancel every task group so its workers stop between
// tasks.
func (m *Manager) cancelAllRuns() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sessionID := range m.running {
		m.stopRequested[sessionID] = true
	}
	for _, cancel := range m.runningCancels {
		if cancel != nil {
			cancel()
		}
	}
	for key, cancel := range m.taskGroupCancels {
		m.stoppedTaskGroups[key] = true
		if cancel != nil {
			cancel()
		}
	}
}

// Initializes a chat manager with topology database, config, scanner registry, tool registry, and session storage.

func NewManager(dbPath, workspace string, emit func(Event)) (*Manager, error) {
	if workspace == "" {
		workspace = "."
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err == nil {
		workspace = absWorkspace
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		return nil, err
	}
	scanners := NewScannerRegistry()
	cfg := helper.EnsureConfig(helper.ConfigPath(dbPath))
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid .aracne/config.json: %w", err)
	}
	chatDir := filepath.Join(filepath.Dir(dbPath), "chat")
	agentDir := filepath.Join(filepath.Dir(dbPath), "agents")
	if err := ensureDefaultAgentFiles(cfg, agentDir); err != nil {
		return nil, err
	}
	providerConfigPath := filepath.Join(filepath.Dir(dbPath), "providers.json")
	providerConfig, err := loadProviderConfig(providerConfigPath)
	if err != nil {
		return nil, err
	}
	provider := defaultProviderSettings()
	chatModel := providerConfig.Defaults.Main
	if sel := chatModelSelection(cfg); sel != "" {
		chatModel = sel
	}
	if configured, ok := providerSettingsForModel(providerConfig, chatModel); ok {
		provider = configured
	}
	store := NewStore(chatDir)
	sessions, err := store.LoadAll()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*Session, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = session
	}
	m := &Manager{
		store:              store,
		workspace:          workspace,
		dbPath:             dbPath,
		manager:            mgr,
		scanners:           scanners,
		config:             cfg,
		registry:           BuildToolRegistry(mgr, scanners, cfg, workspace),
		agentToolRegistry:  BuildAgentToolRegistry(mgr, scanners, cfg, workspace),
		policy:             NewPermissionPolicy(workspace),
		emit:               emit,
		sessions:           byID,
		running:            make(map[string]bool),
		runningCancels:     make(map[string]context.CancelFunc),
		stopRequested:      make(map[string]bool),
		runningToolCalls:   make(map[string]llm.ToolCall),
		interruptedTools:   make(map[string]bool),
		runningTaskGroups:  make(map[string]bool),
		taskGroupCancels:   make(map[string]context.CancelFunc),
		stoppedTaskGroups:  make(map[string]bool),
		agentDir:           agentDir,
		provider:           provider,
		providerConfig:     providerConfig,
		providerConfigPath: providerConfigPath,
	}
	return m, nil
}

// Returns all sessions as summaries, sorted by pinned status then recency

func (m *Manager) ListSessions() []SessionSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]SessionSummary, 0, len(m.sessions))
	for _, session := range m.sessions {
		result = append(result, summarizeSession(session))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Pinned != result[j].Pinned {
			return result[i].Pinned
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result
}

// Creates a new chat session with specified agent and title

func (m *Manager) CreateSession(agent, title string) (*Session, error) {
	agent = normalizeAgent(agent)
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New chat"
	}
	now := time.Now().UTC()
	session := &Session{
		ID:           newID("chat"),
		Title:        title,
		CreatedAt:    now,
		UpdatedAt:    now,
		Mode:         ModeBuild,
		ApprovalMode: ApprovalManual,
		Provider:     m.provider.Provider,
		Model:        m.provider.Model,
		Agent:        agent,
		LLMMessages:  []llm.Message{{Role: "system", Content: m.systemPrompt(ModeBuild, agent)}},
	}
	m.mu.Lock()
	m.sessions[session.ID] = session
	err := m.saveLocked(session)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	m.recordEvent(session.ID, "session_created", map[string]any{"session_id": session.ID})
	return session, nil
}

// Retrieves a session by ID with thread-safe locking and formats it for response

func (m *Manager) GetSession(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionLocked(id)
	if err != nil {
		return nil, err
	}
	return m.sessionForResponseLocked(session), nil
}

// Removes a session from storage and active sessions

// ContextFilter returns the resolved read context-filter from the active
// config. It is used by the viz layer to decide, per neighbor resource,
// whether a chat read surfaced its code (green) or only its description
// (yellow), so the context graph honors the same read configuration the
// chat's own read tools use.
func (m *Manager) ContextFilter() domain.ContextFilter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.config == nil {
		return domain.DefaultContextFilter()
	}
	return m.config.EffectiveContextFilter()
}

func (m *Manager) DeleteSession(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[id] {
		return fmt.Errorf("cannot delete a running session")
	}
	delete(m.sessions, id)
	return m.store.Delete(id)
}

// Updates a session's title and/or pinned status, then persists the change.
func (m *Manager) UpdateSession(id string, title *string, pinned *bool) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionLocked(id)
	if err != nil {
		return nil, err
	}
	if title != nil {
		if trimmed := strings.TrimSpace(*title); trimmed != "" {
			session.Title = trimmed
			// RewindAndResend truncates the conversation back to (and including) the given
			// user message, then re-sends it with optionally edited content. Used for
			// edit-and-resend and regenerate. User messages map 1:1 between the display
			// transcript and the LLM transcript, so the Nth user turn is a safe cut point.
		}
	}
	if pinned != nil {
		session.Pinned = *pinned
	}
	if err := m.saveLocked(session); err != nil {
		return nil, err
	}
	return m.sessionForResponseLocked(session), nil
}

// RewindAndResend truncates the conversation back to (and including) the given
// user message, then re-sends it with optionally edited content. Used for
// edit-and-resend and regenerate. User messages map 1:1 between the display
// transcript and the LLM transcript, so the Nth user turn is a safe cut point.
func (m *Manager) RewindAndResend(sessionID, messageID, newContent string) (*Session, error) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if m.running[sessionID] {
		m.mu.Unlock()
		return nil, fmt.Errorf("chat session is currently running")
	}
	msgIdx, userOrdinal := -1, 0
	for i, msg := range session.Messages {
		if msg.Role == "user" {
			userOrdinal++
			if msg.ID == messageID {
				msgIdx = i
				break
			}
		}
	}
	if msgIdx < 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("user message not found: %s", messageID)
	}
	content := strings.TrimSpace(newContent)
	if content == "" {
		content = session.Messages[msgIdx].Content
	}
	llmCut, seen := len(session.LLMMessages), 0
	for i, lm := range session.LLMMessages {
		if lm.Role == "user" {
			seen++
			if seen == userOrdinal {
				llmCut = i
				break
			}
			// Truncates conversation back to and including the last user message, then resends it
		}
	}
	session.Messages = append([]SessionMessage(nil), session.Messages[:msgIdx]...)
	session.LLMMessages = append([]llm.Message(nil), session.LLMMessages[:llmCut]...)
	session.PendingApprovals = nil
	session.PendingQuestions = nil
	session.UpdatedAt = time.Now().UTC()
	if err := m.saveLocked(session); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	return m.Send(sessionID, SendRequest{Content: content})
}

func (m *Manager) RegenerateLast(sessionID string) (*Session, error) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	lastUserID := ""
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if session.Messages[i].Role == "user" {
			lastUserID = session.Messages[i].ID
			break
		}
	}
	m.mu.Unlock()
	if lastUserID == "" {
		return nil, fmt.Errorf("no user message to regenerate")
	}
	return m.RewindAndResend(sessionID, lastUserID, "")
}

// Updates the manager's configuration and rebuilds the tool and agent registries.
func (m *Manager) SetConfig(cfg *helper.Config) {
	if cfg == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = cfg
	m.registry = BuildToolRegistry(m.manager, m.scanners, cfg, m.workspace)
	m.agentToolRegistry = BuildAgentToolRegistry(m.manager, m.scanners, cfg, m.workspace)
}

// Returns the current LLM provider configuration state.
func (m *Manager) ProviderSettings() ProviderConfigState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return providerConfigState(m.providerConfig)
}

// Merges provider configuration with the existing secrets, persists it, and returns the resulting state.
func (m *Manager) SetProviderConfig(config ProviderConfig) (ProviderConfigState, error) {
	m.mu.Lock()
	merged := mergeProviderSecrets(config, m.providerConfig)
	m.mu.Unlock()
	merged = normalizeProviderConfig(merged)
	if err := saveProviderConfig(m.providerConfigPath, merged); err != nil {
		return ProviderConfigState{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerConfig = merged
	if configured, ok := providerSettingsForModel(merged, merged.Defaults.Main); ok {
		m.provider = configured
	} else {
		m.provider = defaultProviderSettings()
	}
	return providerConfigState(m.providerConfig), nil
}

// Sets the active LLM provider and model, updating the manager's current provider.
func (m *Manager) SetProvider(settings ProviderSettings) ProviderSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	if configured, ok := providerSettingsForName(m.providerConfig, settings.Provider, settings.Model); ok {
		if settings.APIKey != "" {
			configured.APIKey = settings.APIKey
		}
		m.provider = configured
	} else {
		if settings.Provider == "" {
			settings.Provider = m.provider.Provider
		}
		if settings.Model == "" {
			settings.Model = defaultModel(settings.Provider)
		}
		m.provider = settings
	}
	public := m.provider
	public.APIKey = ""
	return public
}

// Sends a user message to a chat session and queues the LLM run when one is not already going.
func (m *Manager) Send(sessionID string, req SendRequest) (*Session, error) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if strings.TrimSpace(req.Content) == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("message content is required")
	}
	if m.running[sessionID] {
		m.mu.Unlock()
		return nil, fmt.Errorf("chat session is currently running; queue the message client-side")
	}
	if req.Mode == "" {
		req.Mode = session.Mode
	}
	if req.ApprovalMode == "" {
		req.ApprovalMode = session.ApprovalMode
	}
	if req.Provider.Provider != "" {
		session.Provider = req.Provider.Provider
		if req.Provider.Model != "" {
			session.Model = req.Provider.Model
		}
	}
	session.Mode = normalizeMode(req.Mode)
	session.ApprovalMode = normalizeApprovalMode(req.ApprovalMode)
	firstPrompt := session.Title == "New chat" && len(session.Messages) == 0
	msg := SessionMessage{ID: newID("msg"), Role: "user", Content: req.Content, CreatedAt: time.Now().UTC(), Status: "completed"}
	session.Messages = append(session.Messages, msg)
	session.LLMMessages = replaceSystemPrompt(session.LLMMessages, m.systemPrompt(session.Mode, session.Agent))
	session.LLMMessages = append(session.LLMMessages, llm.Message{Role: "user", Content: req.Content})
	// Processes approval or rejection for a pending approval in a session
	session.UpdatedAt = time.Now().UTC()
	if err := m.saveLocked(session); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()

	m.recordEvent(sessionID, "message", map[string]any{"message": msg})
	if firstPrompt {
		m.goBackground(func() { m.generateTitle(sessionID, req.Content) })
	}
	m.startRun(sessionID)
	return m.GetSession(sessionID)
}

func (m *Manager) ResolveApproval(sessionID, approvalID string, approved bool) (*Session, error) {
	var pending PendingApproval
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	idx := -1
	for i, approval := range session.PendingApprovals {
		if approval.ID == approvalID {
			pending = approval
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("approval not found: %s", approvalID)
	}
	session.PendingApprovals = append(session.PendingApprovals[:idx], session.PendingApprovals[idx+1:]...)
	_ = m.saveLocked(session)
	m.mu.Unlock()

	if approved {
		m.recordEvent(sessionID, "approval_resolved", map[string]any{"approval_id": approvalID, "approved": true})
		if err := m.executeToolCall(sessionID, pending.ToolCall); err != nil {
			m.appendToolResult(sessionID, pending.ToolCall, "Error: "+err.Error(), "error")
		}
	} else {
		m.recordEvent(sessionID, "approval_resolved", map[string]any{"approval_id": approvalID, "approved": false})
		m.appendToolResult(sessionID, pending.ToolCall, "User declined this tool call.", "declined")
	}
	m.processToolCalls(sessionID, m.takePendingToolCalls(sessionID))
	m.startRun(sessionID)
	return m.GetSession(sessionID)
}

// Records an answer to a pending question in a session.
func (m *Manager) AnswerQuestion(sessionID, questionID, answer string) (*Session, error) {
	var pending PendingQuestion
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	idx := -1
	for i, question := range session.PendingQuestions {
		if question.ID == questionID {
			pending = question
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("question not found: %s", questionID)
	}
	session.PendingQuestions = append(session.PendingQuestions[:idx], session.PendingQuestions[idx+1:]...)
	_ = m.saveLocked(session)
	m.mu.Unlock()

	m.recordEvent(sessionID, "question_answered", map[string]any{"question_id": questionID})
	m.appendToolResult(sessionID, pending.ToolCall, answer, "completed")
	m.processToolCalls(sessionID, m.takePendingToolCalls(sessionID))
	m.startRun(sessionID)
	return m.GetSession(sessionID)
}

// Starts an asynchronous run for a session, handling state management and tool dispatch.
func (m *Manager) startRun(sessionID string) {
	m.mu.Lock()
	if m.running[sessionID] {
		m.mu.Unlock()
		return
	}
	delete(m.stopRequested, sessionID)
	m.running[sessionID] = true
	m.mu.Unlock()
	m.recordEvent(sessionID, "run_started", map[string]any{"active": true})
	started := m.goBackground(func() {
		defer func() {
			m.mu.Lock()
			if cancel := m.runningCancels[sessionID]; cancel != nil {
				cancel()
			}
			delete(m.running, sessionID)
			delete(m.runningCancels, sessionID)
			delete(m.stopRequested, sessionID)
			delete(m.runningToolCalls, sessionID)
			m.mu.Unlock()
			m.recordEvent(sessionID, "run_completed", map[string]any{"active": false})
		}()
		m.runLoop(sessionID)
	})
	if !started {
		// The manager is closing, so the goroutine that would have cleared this never ran.
		// Leaving the session marked running would make it unstartable for the rest of the
		// process and report an active run that does not exist.
		m.mu.Lock()
		delete(m.running, sessionID)
		m.mu.Unlock()
		m.recordEvent(sessionID, "run_completed", map[string]any{"active": false})
	}
}

// Marks a session for stop, cancels its LLM context and running tasks, interrupts a non-CreateTasks tool call, and records the stop event.
func (m *Manager) StopSession(sessionID string) (*Session, error) {
	var interrupted *llm.ToolCall
	m.mu.Lock()
	if _, err := m.getSessionLocked(sessionID); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.stopRequested[sessionID] = true
	if cancel := m.runningCancels[sessionID]; cancel != nil {
		cancel()
	}
	if tc, ok := m.runningToolCalls[sessionID]; ok && tc.Function.Name != "CreateTasks" && !m.interruptedTools[tc.ID] {
		m.interruptedTools[tc.ID] = true
		copy := tc
		interrupted = &copy
	}
	// Initiates an LLM API call and manages task group lifecycle for the session.
	prefix := sessionID + ":"
	for key, cancel := range m.taskGroupCancels {
		if strings.HasPrefix(key, prefix) {
			m.stoppedTaskGroups[key] = true
			cancel()
		}
	}
	m.mu.Unlock()
	if interrupted != nil {
		m.appendToolResult(sessionID, *interrupted, "Tool interrupted.", "interrupted")
	}
	m.recordEvent(sessionID, "stop_requested", map[string]any{"active": false})
	return m.GetSession(sessionID)
}

// Reports whether a stop has been requested and, when it has not, returns a cancellable context for the LLM call.
func (m *Manager) beginLLMCall(sessionID string) (context.Context, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopRequested[sessionID] {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runningCancels[sessionID] = cancel
	return ctx, true
}

// Drops a session's cancel function once its LLM call has finished.
func (m *Manager) finishLLMCall(sessionID string) {
	m.mu.Lock()
	delete(m.runningCancels, sessionID)
	m.mu.Unlock()
}

// Reports whether a stop has been requested for a session.
func (m *Manager) isStopRequested(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopRequested[sessionID]
}

// Marks a tool call as running for a session, refusing when a stop is already pending.
func (m *Manager) markRunningTool(sessionID string, tc llm.ToolCall) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopRequested[sessionID] {
		return false
	}
	m.runningToolCalls[sessionID] = tc
	return true
}

// Clears a running tool call from the session's tracking map, reporting whether the call was interrupted or stopped.
func (m *Manager) clearRunningTool(sessionID, toolCallID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tc, ok := m.runningToolCalls[sessionID]; ok && tc.ID == toolCallID {
		delete(m.runningToolCalls, sessionID)
	}
	interrupted := m.interruptedTools[toolCallID]
	if interrupted {
		delete(m.interruptedTools, toolCallID)
	}
	return interrupted || m.stopRequested[sessionID]
}

// Runs one streaming chat request, preferring the provider's context-aware form when it has one.
func streamChat(ctx context.Context, provider llm.Provider, messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	if contextual, ok := provider.(llm.ContextProvider); ok {
		return contextual.StreamChatContext(ctx, messages, tools, emit)
	}
	return provider.StreamChat(messages, tools, emit)
}

// Runs the main conversation loop, streaming LLM responses and handling tool calls until completion or interruption.
func (m *Manager) runLoop(sessionID string) {
	for i := 0; i < 40; i++ {
		if m.isStopRequested(sessionID) {
			return
		}
		m.mu.Lock()
		session, err := m.getSessionLocked(sessionID)
		if err != nil || len(session.PendingApprovals) > 0 || len(session.PendingQuestions) > 0 {
			m.mu.Unlock()
			return
		}
		if ensureToolResultsComplete(session) {
			_ = m.saveLocked(session)
		}
		messages := append([]llm.Message(nil), session.LLMMessages...)
		providerSettings := m.providerForSession(session)
		m.mu.Unlock()

		provider, err := newProvider(providerSettings)
		if err != nil {
			m.appendAssistantError(sessionID, err)
			return
		}
		ctx, ok := m.beginLLMCall(sessionID)
		if !ok {
			return
		}
		m.recordEvent(sessionID, "thinking", map[string]any{"active": true})
		resp, err := streamChat(ctx, provider, messages, toToolDefinitions(m.registry.List()), func(event llm.StreamEvent) {
			payload := map[string]any{}
			if event.Content != "" {
				payload["content"] = event.Content
			}
			if event.Reasoning != "" {
				payload["reasoning"] = event.Reasoning
			}
			if len(payload) > 0 {
				m.recordEvent(sessionID, "delta", payload)
			}
		})
		m.finishLLMCall(sessionID)
		m.recordEvent(sessionID, "thinking", map[string]any{"active": false})
		if err != nil {
			if errors.Is(err, context.Canceled) || m.isStopRequested(sessionID) {
				return
			}
			m.appendAssistantError(sessionID, err)
			return
		}
		if m.isStopRequested(sessionID) {
			return
		}

		assistantMsg := llm.Message{Role: "assistant", Content: resp.Content,
			ToolCalls: resp.ToolCalls, Thinking: resp.Thinking}
		m.mu.Lock()
		session, err = m.getSessionLocked(sessionID)
		if err != nil {
			m.mu.Unlock()
			return
		}
		session.LLMMessages = append(session.LLMMessages, assistantMsg)
		if strings.TrimSpace(resp.Content) != "" {
			msg := SessionMessage{ID: newID("msg"), Role: "assistant", Content: resp.Content, Reasoning: resp.Reasoning, CreatedAt: time.Now().UTC(), Status: "completed"}
			session.Messages = append(session.Messages, msg)
			session.UpdatedAt = time.Now().UTC()
			_ = m.saveLocked(session)
			m.mu.Unlock()
			m.recordEvent(sessionID, "message", map[string]any{"message": msg})
		} else {
			_ = m.saveLocked(session)
			m.mu.Unlock()
		}

		if len(resp.ToolCalls) == 0 {
			return
		}
		if m.processToolCalls(sessionID, resp.ToolCalls) {
			return
		}
	}
	m.appendAssistantError(sessionID, fmt.Errorf("exceeded max chat iterations"))
}

// processToolCalls runs the given tool calls in order. When one pauses (needs
// approval, asks a question, or the run is stopped mid-batch) the remaining calls
// are persisted to session.PendingToolCalls so a later resume can finish them, and
// it returns true. It returns false once the whole batch has been drained.
func (m *Manager) processToolCalls(sessionID string, tcs []llm.ToolCall) bool {
	for i, tc := range tcs {
		if paused := m.prepareOrExecuteTool(sessionID, tc); paused {
			m.setPendingToolCalls(sessionID, tcs[i+1:])
			return true
		}
		if m.isStopRequested(sessionID) {
			m.setPendingToolCalls(sessionID, tcs[i+1:])
			return true
		}
	}
	m.setPendingToolCalls(sessionID, nil)
	return false
}

// setPendingToolCalls stores the not-yet-processed tool calls of the current
// assistant turn so an approval/question resume can drain them later.
func (m *Manager) setPendingToolCalls(sessionID string, tcs []llm.ToolCall) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err == nil {
		if len(tcs) == 0 {
			session.PendingToolCalls = nil
		} else {
			session.PendingToolCalls = append([]llm.ToolCall(nil), tcs...)
		}
		_ = m.saveLocked(session)
	}
	m.mu.Unlock()
}

// takePendingToolCalls returns and clears any queued sibling tool calls from a
// paused assistant turn.
func (m *Manager) takePendingToolCalls(sessionID string) []llm.ToolCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		return nil
	}
	tcs := session.PendingToolCalls
	if len(tcs) > 0 {
		session.PendingToolCalls = nil
		_ = m.saveLocked(session)
	}
	return tcs
}

// ensureToolResultsComplete appends a synthetic tool result for any tool_call in
// the most recent assistant turn that lacks a matching role:"tool" message, so the
// provider invariant "every tool_call must be answered before the next request"
// always holds. This backstops interrupted/cancelled/crashed tool runs that would
// otherwise orphan a tool_call. Returns true if it modified the session.
func ensureToolResultsComplete(session *Session) bool {
	lastAssistant := -1
	for i := len(session.LLMMessages) - 1; i >= 0; i-- {
		msg := session.LLMMessages[i]
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			lastAssistant = i
			break
		}
	}
	if lastAssistant < 0 {
		return false
	}
	answered := map[string]bool{}
	for _, msg := range session.LLMMessages[lastAssistant+1:] {
		switch msg.Role {
		case "tool":
			if msg.ToolCallID != "" {
				answered[msg.ToolCallID] = true
			}
		case "assistant", "user":
			// The conversation already moved past this turn; nothing to backfill.
			return false
		}
	}
	changed := false
	for _, tc := range session.LLMMessages[lastAssistant].ToolCalls {
		if !answered[tc.ID] {
			session.LLMMessages = append(session.LLMMessages, llm.Message{Role: "tool", Content: "Tool call did not complete.", ToolCallID: tc.ID})
			changed = true
		}
	}
	return changed
}

// Prepares a tool call for execution, enforcing permissions and handling ask_user_question and auto-approval.
func (m *Manager) prepareOrExecuteTool(sessionID string, tc llm.ToolCall) bool {
	m.addToolMessage(sessionID, tc)
	if tc.Function.Name == "ask_user_question" {
		if err := m.createQuestion(sessionID, tc); err != nil {
			m.appendToolResult(sessionID, tc, "Error: "+err.Error(), "error")
			return false
		}
		return true
	}
	decision := m.policy.Decide(tc.Function.Name, tc.Function.Arguments, m.sessionMode(sessionID), m.sessionApprovalMode(sessionID))
	if decision.Action == permissionDeny {
		m.appendToolResult(sessionID, tc, "Error: "+decision.Reason, "denied")
		return false
	}
	if decision.Action == permissionAsk && m.sessionApprovalMode(sessionID) == ApprovalAuto {
		if m.autoApprove(sessionID, tc, decision.Reason) {
			decision = permissionDecision{Action: permissionAllow, Reason: "auto-approved"}
		}
	}
	if decision.Action == permissionAsk {
		approval := PendingApproval{ID: newID("approval"), CreatedAt: time.Now().UTC(), Reason: decision.Reason, ToolCall: tc}
		m.mu.Lock()
		session, err := m.getSessionLocked(sessionID)
		if err == nil {
			session.PendingApprovals = append(session.PendingApprovals, approval)
			session.UpdatedAt = time.Now().UTC()
			_ = m.saveLocked(session)
		}
		m.mu.Unlock()
		m.recordEvent(sessionID, "approval_required", map[string]any{"approval": approval})
		return true
	}
	if err := m.executeToolCall(sessionID, tc); err != nil {
		m.appendToolResult(sessionID, tc, "Error: "+err.Error(), "error")
	}
	return false
}

// Dispatches a tool call to the handler for ask_user_question, CreateTasks, or an ordinary tool.
func (m *Manager) executeToolCall(sessionID string, tc llm.ToolCall) error {
	if m.isStopRequested(sessionID) {
		return nil
	}
	if tc.Function.Name == "ask_user_question" {
		return m.createQuestion(sessionID, tc)
	}
	if tc.Function.Name == "CreateTasks" {
		return m.executeCreateTasksTool(sessionID, tc)
	}
	tool, ok := m.registry.Get(tc.Function.Name)
	if !ok {
		return fmt.Errorf("unknown tool %q", tc.Function.Name)
	}
	if !m.markRunningTool(sessionID, tc) {
		return nil
	}
	defer m.clearRunningTool(sessionID, tc.ID)
	m.recordEvent(sessionID, "tool_running", map[string]any{"tool_call_id": tc.ID})
	result, err := tool.Run(json.RawMessage(tc.Function.Arguments))
	if m.clearRunningTool(sessionID, tc.ID) {
		return nil
	}
	if err != nil {
		if strings.TrimSpace(result) != "" {
			result = result + "\n" + err.Error()
		} else {
			result = err.Error()
		}
		m.appendToolResult(sessionID, tc, "Error: "+result, "error")
		return nil
	}
	m.appendToolResult(sessionID, tc, result, "completed")
	return nil
}

// Parses a question tool call from the LLM and records its options and multi-select flag.
func (m *Manager) createQuestion(sessionID string, tc llm.ToolCall) error {
	var params struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Multiple bool     `json:"multiple"`
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &params); err != nil {
		return fmt.Errorf("invalid question arguments: %w", err)
	}
	if strings.TrimSpace(params.Question) == "" {
		return fmt.Errorf("question is required")
	}
	question := PendingQuestion{ID: newID("question"), CreatedAt: time.Now().UTC(), Question: params.Question, Options: params.Options, Multiple: params.Multiple, ToolCall: tc}
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err == nil {
		session.PendingQuestions = append(session.PendingQuestions, question)
		session.UpdatedAt = time.Now().UTC()
		_ = m.saveLocked(session)
	}
	m.mu.Unlock()
	m.recordEvent(sessionID, "question_required", map[string]any{"question": question})
	return nil
}

// Appends tool execution result and status to session message history.
func (m *Manager) addToolMessage(sessionID string, tc llm.ToolCall) {
	input := jsonRaw{}
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
	msg := SessionMessage{ID: newID("tool"), Role: "tool", CreatedAt: time.Now().UTC(), Status: "pending", ToolCallID: tc.ID, ToolName: tc.Function.Name, ToolInput: input}
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err == nil {
		session.Messages = append(session.Messages, msg)
		session.UpdatedAt = time.Now().UTC()
		_ = m.saveLocked(session)
	}
	m.mu.Unlock()
	m.recordEvent(sessionID, "tool_call", map[string]any{"message": msg})
}

// Appends a tool result to the session's conversation and records the corresponding event.
func (m *Manager) appendToolResult(sessionID string, tc llm.ToolCall, result, status string) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err == nil {
		// Appends an error message from the assistant to the session and marks the conversation as failed.
		session.LLMMessages = append(session.LLMMessages, llm.Message{Role: "tool", Content: result, ToolCallID: tc.ID})
		for i := len(session.Messages) - 1; i >= 0; i-- {
			if session.Messages[i].ToolCallID == tc.ID {
				session.Messages[i].ToolOutput = result
				session.Messages[i].Status = status
				break
			}
		}
		session.UpdatedAt = time.Now().UTC()
		_ = m.saveLocked(session)
	}
	m.mu.Unlock()
	m.recordEvent(sessionID, "tool_result", map[string]any{"tool_call_id": tc.ID, "status": status, "output": result})
}

// Appends an assistant-visible error message to the session.
func (m *Manager) appendAssistantError(sessionID string, err error) {
	status := "error"
	var contractErr *ProviderContractError
	if errors.As(err, &contractErr) {
		status = "warning"
	}
	msg := SessionMessage{ID: newID("msg"), Role: "assistant", Content: "Error: " + err.Error(), CreatedAt: time.Now().UTC(), Status: status}
	m.mu.Lock()
	session, getErr := m.getSessionLocked(sessionID)
	if getErr == nil {
		session.Messages = append(session.Messages, msg)
		session.UpdatedAt = time.Now().UTC()
		_ = m.saveLocked(session)
	}
	m.mu.Unlock()
	m.recordEvent(sessionID, "message", map[string]any{"message": msg})
}

// Generates a session title with an LLM provider, falling back to one derived from the first prompt.
func (m *Manager) generateTitle(sessionID, firstPrompt string) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		m.mu.Unlock()
		return
	}
	settings := m.providerForSession(session)
	m.mu.Unlock()

	provider, err := newProvider(settings)
	if err != nil {
		m.updateTitle(sessionID, titleFromContent(firstPrompt))
		return
	}
	resp, err := provider.Chat([]llm.Message{
		{Role: "system", Content: "Generate a concise chat title from the user's first message. Return only the title, no quotes, no punctuation unless necessary. Max 6 words."},
		{Role: "user", Content: firstPrompt},
	}, nil)
	if err != nil {
		m.updateTitle(sessionID, titleFromContent(firstPrompt))
		return
	}
	title := cleanGeneratedTitle(resp.Content)
	if title == "" {
		title = titleFromContent(firstPrompt)
	}
	m.updateTitle(sessionID, title)
}

// Determines if a tool call should be auto-approved based on session provider settings and reason.

// Sets the title of a chat session, whether generated by the LLM or derived from its first message.
func (m *Manager) updateTitle(sessionID, title string) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err == nil && session.Title == "New chat" {
		session.Title = title
		session.UpdatedAt = time.Now().UTC()
		_ = m.saveLocked(session)
	}
	m.mu.Unlock()
	if err == nil {
		m.recordEvent(sessionID, "session_title_updated", map[string]any{"session_id": sessionID, "title": title})
	}
}

// Asks the judge model whether a tool call may run unattended, and records the verdict as an event.
func (m *Manager) autoApprove(sessionID string, tc llm.ToolCall, reason string) bool {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		m.mu.Unlock()
		return false
	}
	settings := m.providerForSession(session)
	m.mu.Unlock()
	provider, err := newProvider(settings)
	if err != nil {
		return false
	}
	prompt := "Decide if this local coding-agent tool call should run. Return exactly ALLOW or ASK. Allow safe in-workspace reads and ordinary build/test commands. Ask for destructive commands, secrets, network credential use, or unclear outside-workspace access."
	input := fmt.Sprintf("Reason: %s\nTool: %s\nArguments: %s", reason, tc.Function.Name, tc.Function.Arguments)
	resp, err := provider.Chat([]llm.Message{{Role: "system", Content: prompt}, {Role: "user", Content: input}}, nil)
	if err != nil {
		return false
	}
	decision := strings.ToUpper(strings.TrimSpace(resp.Content))
	allowed := strings.HasPrefix(decision, "ALLOW")
	m.recordEvent(sessionID, "auto_approval", map[string]any{"tool_call_id": tc.ID, "allowed": allowed, "decision": resp.Content})
	return allowed
}

// Records a session event with metadata for audit and tracing purposes.
func (m *Manager) recordEvent(sessionID, typ string, payload map[string]any) {
	event := Event{ID: newID("evt"), SessionID: sessionID, Type: typ, CreatedAt: time.Now().UTC(), Payload: payload}
	m.mu.Lock()
	if session, err := m.getSessionLocked(sessionID); err == nil {
		session.Events = append(session.Events, event)
		if len(session.Events) > 500 {
			session.Events = session.Events[len(session.Events)-500:]
		}
		_ = m.saveLocked(session)
	}
	m.mu.Unlock()
	if m.emit != nil {
		m.emit(event)
	}
}

// Retrieves a session by ID (must be called with the mutex held).
func (m *Manager) getSessionLocked(id string) (*Session, error) {
	if session, ok := m.sessions[id]; ok {
		return session, nil
	}
	session, err := m.store.Load(id)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	m.sessions[id] = session
	return session, nil
}

// Persists a session to storage (must be called with the mutex held).
func (m *Manager) saveLocked(session *Session) error {
	return m.store.Save(session)
}

// Prepares a session for an API response by resolving its running and task-group state.
func (m *Manager) sessionForResponseLocked(session *Session) *Session {
	copy := *session
	copy.Running = m.running[session.ID]
	if len(session.TaskGroups) > 0 {
		copy.TaskGroups = append([]TaskGroup(nil), session.TaskGroups...)
		for i := range copy.TaskGroups {
			copy.TaskGroups[i].Processing = m.runningTaskGroups[taskGroupRunKey(session.ID, copy.TaskGroups[i].ID)]
		}
	}
	return &copy
}

// Returns the provider settings configured for a session.
func (m *Manager) providerForSession(session *Session) ProviderSettings {
	settings := m.provider
	if configured, ok := providerSettingsForName(m.providerConfig, session.Provider, session.Model); ok {
		settings = configured
	}
	if session.Model != "" {
		settings.Model = session.Model
	}
	return settings
}

// Retrieves the interaction mode from a session, for permission-policy decisions.
func (m *Manager) sessionMode(sessionID string) Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, err := m.getSessionLocked(sessionID); err == nil {
		return session.Mode
	}
	return ModeBuild
}

// Retrieves the approval mode from a session, for permission-policy decisions.
func (m *Manager) sessionApprovalMode(sessionID string) ApprovalMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, err := m.getSessionLocked(sessionID); err == nil {
		return session.ApprovalMode
	}
	return ApprovalManual
}

// Returns the system prompt for the LLM, chosen by session mode and agent.
func (m *Manager) systemPrompt(mode Mode, sessionAgent string) string {
	base := "You are a codebase LLM chat inside aracne. Use tools to inspect and modify the workspace. Native read tools return topology-aware context and native edit/write tools update topology. Prefer specific resource read functions (read_struct, read_function, etc.) instead of the base read_file function. In Plan Mode, do not mutate files or topology; produce plans and ask clarifying questions. In Build Mode, implement requested changes with minimal edits. Report confirmed unrelated bugs with bug_report instead of fixing them. Current mode: " + string(mode) + agentKindPrompt(m.agentDir)
	if prompt := agentPrompt(sessionAgent); prompt != "" {
		return base + "\n\nYou are running as the " + normalizeAgent(sessionAgent) + " agent. Follow this agent instruction:\n\n" + prompt
	}
	return base
}

// Creates an LLM provider instance from settings, applying defaults and configuring reasoning/thinking budgets.
func toToolDefinitions(toolList []tools.Tool) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(toolList))
	for _, tool := range toolList {
		params := llm.Parameters{Type: "object", Properties: make(map[string]llm.Property)}
		for _, param := range tool.Parameters() {
			prop := llm.Property{Type: param.Type, Description: param.Description}
			if param.Items != "" {
				prop.Items = &llm.ItemSpec{Type: param.Items}
			}
			params.Properties[param.Name] = prop
			if param.Required {
				params.Required = append(params.Required, param.Name)
			}
		}
		defs = append(defs, llm.ToolDefinition{Name: tool.Name(), Description: tool.Description(), Parameters: params})
	}
	return defs
}

// Creates an LLM provider (Anthropic/OpenAI/DeepSeek) from settings, applying the thinking budget.
func newProvider(settings ProviderSettings) (llm.Provider, error) {
	if settings.Provider == "" {
		settings = defaultProviderSettings()
	}
	if settings.Model == "" {
		settings.Model = defaultModel(settings.Provider)
	}
	if settings.APIKey == "" && settings.KeyEnv != "" {
		settings.APIKey = os.Getenv(settings.KeyEnv)
	}
	contract := settings.Provider
	if settings.Contract != "" {
		contract = settings.Contract
	}
	var provider llm.Provider
	switch contract {
	case ProviderOpenAI:
		op := providers.NewOpenAI(settings.APIKey, settings.Model, settings.BaseURL)
		if settings.ThinkingBudget > 0 && isOpenAIReasoningModel(settings.Model) {
			op.SetReasoningEffort(openAIReasoningEffort(settings.ThinkingBudget))
		}
		provider = op
	case ProviderAnthropic:
		ap := providers.NewAnthropic(settings.APIKey, settings.Model, settings.BaseURL)
		if settings.ThinkingBudget > 0 {
			ap.SetThinkingBudget(settings.ThinkingBudget)
		}
		provider = ap
	case ProviderDeepSeek:
		apiKey := settings.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("DEEPSEEK_API_KEY")
		}
		// isOpenAIReasoningModel reports whether an OpenAI model accepts the
		// reasoning_effort parameter. Sending it to a non-reasoning model is an API
		// error, so reasoning is only applied when the model is reasoning-capable.
		// DeepSeek selects reasoning via the model (deepseek-reasoner), not a
		// request parameter, so ThinkingBudget is handled by model choice.
		provider = providers.NewDeepSeekWithConfig(apiKey, settings.Model, settings.BaseURL)
	default:
		return nil, fmt.Errorf("unsupported provider contract: %s", contract)
	}
	if settings.Custom {
		provider = contractGuardProvider{provider: provider}
	}
	return provider, nil
}

// isOpenAIReasoningModel reports whether an OpenAI model accepts the
// reasoning_effort parameter. Sending it to a non-reasoning model is an API
// error, so reasoning is only applied when the model is reasoning-capable.
func isOpenAIReasoningModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		// openAIReasoningEffort maps a thinking-token budget to an OpenAI reasoning
		// effort tier.
		return false
	}
	if strings.Contains(m, "reason") {
		return true
	}
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5"} {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
}

// Wraps an LLM provider to catch and convert contract violations into ProviderContractError.
// openAIReasoningEffort maps a thinking-token budget to an OpenAI reasoning
// effort tier.
func openAIReasoningEffort(budget int) string {
	switch {
	case budget <= 0:
		return ""
	case budget < 2048:
		return "low"
	case budget < 6000:
		return "medium"
	default:
		return "high"
		// Streams a chat request to the wrapped provider and wraps any contract errors.
	}
}

// Wraps an llm.Provider so a contract violation surfaces as a provider error the run loop can report.
type contractGuardProvider struct {
	provider llm.Provider
}

// Sends a chat request to the wrapped provider and wraps any contract error.
func (p contractGuardProvider) Chat(messages []llm.Message, tools []llm.ToolDefinition) (*llm.ChatResponse, error) {
	resp, err := p.provider.Chat(messages, tools)
	if err != nil && isContractError(err) {
		return nil, &ProviderContractError{Err: err}
	}
	return resp, err
}

// Wraps the provider's StreamChat to catch and convert contract errors.
func (p contractGuardProvider) StreamChat(messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	resp, err := p.provider.StreamChat(messages, tools, emit)
	if err != nil && isContractError(err) {
		return nil, &ProviderContractError{Err: err}
	}
	return resp, err
}

// Wraps the provider's StreamChatContext to catch and convert contract errors.
func (p contractGuardProvider) StreamChatContext(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	if contextual, ok := p.provider.(llm.ContextProvider); ok {
		resp, err := contextual.StreamChatContext(ctx, messages, tools, emit)
		// chatModelSelection returns the model id from viz.chat.agents.model, stripping
		// any "provider/" prefix. Empty means "use the providers.json default".
		if err != nil && isContractError(err) {
			return nil, &ProviderContractError{Err: err}
		}
		return resp, err
	}
	return p.StreamChat(messages, tools, emit)
}

// Reports whether an error indicates a contract violation (an incomplete read, write, or tool result).
func isContractError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unmarshal") || strings.Contains(message, "decode") || strings.Contains(message, "read stream")
}

// Returns default provider settings, prioritizing OpenAI, then Anthropic, then DeepSeek based on API key availability.
// any "provider/" prefix. Empty means "use the providers.json default".
func chatModelSelection(cfg *helper.Config) string {
	if cfg == nil {
		return ""
	}
	model := strings.TrimSpace(cfg.Viz.Chat.Agents.Model)
	if model == "" {
		return ""
	}
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		return strings.TrimSpace(model[idx+1:])
	}
	return model
}

// Selects the default LLM provider from whichever API key is present in the environment.
func defaultProviderSettings() ProviderSettings {
	if os.Getenv("OPENAI_API_KEY") != "" {
		return ProviderSettings{Provider: ProviderOpenAI, Model: defaultModel(ProviderOpenAI)}
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return ProviderSettings{Provider: ProviderAnthropic, Model: defaultModel(ProviderAnthropic)}
	}
	return ProviderSettings{Provider: ProviderDeepSeek, Model: defaultModel(ProviderDeepSeek)}
}

// Returns the default model for a given LLM provider.

// Returns the default model for a given LLM provider.
func defaultModel(provider ProviderName) string {
	if model := firstSupportedProviderModel(provider); model != "" {
		return model
	}
	switch provider {
	case ProviderAnthropic:
		return "fable-5"
	case ProviderDeepSeek:
		return "deepseek-v4-pro"
	default:
		return "gpt-5.5"
	}
}

// Normalizes a Mode value to a supported state, defaulting to build.
func normalizeMode(mode Mode) Mode {
	if mode == ModePlan {
		return ModePlan
	}
	return ModeBuild
}

// Normalizes an ApprovalMode value to a valid state.
func normalizeApprovalMode(mode ApprovalMode) ApprovalMode {
	switch mode {
	// Sanitizes generated title text by trimming whitespace and removing quotes and special characters.
	case ApprovalAuto, ApprovalAlways:
		return mode
	default:
		return ApprovalManual
	}
}

// Replaces the system prompt in an LLM request with a custom prompt.
func replaceSystemPrompt(messages []llm.Message, prompt string) []llm.Message {
	if len(messages) > 0 && messages[0].Role == "system" {
		messages[0].Content = prompt
		return messages
	}
	return append([]llm.Message{{Role: "system", Content: prompt}}, messages...)
}

// Generates a chat title from content, truncating to 48 chars and defaulting to "New chat" if empty.
func cleanGeneratedTitle(content string) string {
	content = strings.TrimSpace(content)
	content = strings.Trim(content, "\"'` ")
	content = strings.ReplaceAll(content, "\n", " ")
	fields := strings.Fields(content)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	content = strings.Join(fields, " ")
	if len(content) > 64 {
		content = content[:64]
	}
	return content
}

// Derives a session title from a message's content.
func titleFromContent(content string) string {
	content = strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
	if len(content) > 48 {
		return content[:48] + "..."
	}
	if content == "" {
		return "New chat"
	}
	return content
}

// Generates a unique ID with the given prefix.
func newID(prefix string) string {
	return fmt.Sprintf("%s_%d_%04x", prefix, time.Now().UnixNano(), rand.Intn(65536))
}
