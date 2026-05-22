package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/command"
	"github.com/marcomoesman/prompto/internal/compact"
	"github.com/marcomoesman/prompto/internal/config"
	"github.com/marcomoesman/prompto/internal/permission"
	"github.com/marcomoesman/prompto/internal/provider"
	"github.com/marcomoesman/prompto/internal/search"
	"github.com/marcomoesman/prompto/internal/store"
	"github.com/marcomoesman/prompto/internal/tool"
	"github.com/marcomoesman/prompto/internal/tui"
	"github.com/marcomoesman/prompto/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		debug        bool
		newFlag      bool
		listSessions bool
		clearHistory bool
		assumeYes    bool
		resumeFlag   string
		modeFlag     string
		yoloFlag     bool
		agentFlag    string
		showVersion  bool
	)
	flag.BoolVar(&showVersion, "version", false, "print prompto version and exit")
	flag.BoolVar(&debug, "debug", false, "write one JSONL line per API request to .prompto/logs/")
	flag.BoolVar(&newFlag, "new", false, "start a new session, ignoring most-recent")
	flag.BoolVar(&listSessions, "sessions", false, "list sessions in this project and exit")
	flag.BoolVar(&clearHistory, "clear-history", false, "delete every session in this project (sessions, messages, file changes, todos, plans, tmp spills) and exit; prompts for confirmation unless --yes")
	flag.BoolVar(&assumeYes, "yes", false, "skip confirmation prompts on destructive operations like --clear-history")
	flag.StringVar(&resumeFlag, "resume", "", "resume a session by id prefix (empty = most recent)")
	flag.StringVar(&modeFlag, "mode", "default", "permission mode: default | acceptEdits | bypass")
	flag.BoolVar(&yoloFlag, "yolo", false, "shortcut for --mode bypass (ALL tool calls allowed without prompt)")
	flag.StringVar(&agentFlag, "agent", "", "primary agent for new sessions: build | plan (default: build)")
	flag.Parse()
	if showVersion {
		fmt.Printf("prompto v%s\n", version.Version)
		return nil
	}
	if os.Getenv("PROMPTO_DEBUG") == "1" {
		debug = true
	}

	// Detect whether --resume was provided even with no value (Go's flag
	// package treats bare --resume as unset since it takes a value).
	resumeProvided := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "resume" {
			resumeProvided = true
		}
	})

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	projectInstructions, err := agent.LoadProjectInstructions(agent.LoadInstructionsInput{
		Cwd:       cwd,
		Filenames: cfg.Rules.Files,
	})
	if err != nil {
		return fmt.Errorf("loading project instructions: %w", err)
	}

	logDir := filepath.Join(cwd, ".prompto", "logs")
	logger, err := agent.NewRequestLogger(agent.NewRequestLoggerInput{
		Debug: debug,
		Dir:   logDir,
	})
	if err != nil {
		return fmt.Errorf("creating request logger: %w", err)
	}
	defer func() { _ = logger.Close() }()

	ctx := context.Background()

	// Open the project-local SQLite DB. Failure here is fatal: phase 3
	// requires persistence to be on the happy path. Threading ctx
	// makes startup migrations cancellable so a Ctrl+C during a slow
	// schema upgrade aborts cleanly rather than hanging.
	dbPath := filepath.Join(cwd, ".prompto", "db.sqlite")
	st, err := store.Open(store.OpenInput{Path: dbPath, Ctx: ctx})
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() { _ = st.Close() }()

	// --sessions: print and exit.
	if listSessions {
		return printSessions(ctx, st)
	}

	// --clear-history: wipe every session row + on-disk artefacts and exit.
	if clearHistory {
		return clearProjectHistory(ctx, st, cwd, assumeYes)
	}

	// Permission mode + ruleset. Failures here are non-fatal: if the
	// permissions.json is corrupt we warn and fall back to an empty ruleset
	// (everything asks). --yolo wins over --mode.
	mode, err := permission.ParseMode(modeFlag)
	if err != nil {
		return err
	}
	if yoloFlag {
		mode = permission.ModeBypass
	}
	if mode == permission.ModeBypass {
		fmt.Fprintln(os.Stderr, "WARNING: prompto is running in BYPASS mode. Every tool call will execute without prompting. Abort with Ctrl-C if unintended.")
	}
	ruleset, err := permission.LoadRuleset(permission.LoadRulesetInput{
		ProjectPath: filepath.Join(cwd, ".prompto", "permissions.json"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading permissions.json: %v\n", err)
		ruleset = permission.NewRuleset()
	}
	evaluator := permission.NewEvaluator(permission.NewEvaluatorInput{
		Mode:    mode,
		Ruleset: ruleset,
	})

	// Resolve the active agent name. --agent overrides; on resume we adopt
	// the session's stored agent_name; otherwise default to "build".
	registry := agent.DefaultRegistry()
	agentName := agentFlag
	if agentName == "" {
		agentName = "build"
	}
	if _, ok := registry.Resolve(agentName); !ok {
		return fmt.Errorf("unknown agent %q (try: build, plan)", agentName)
	}

	// Resolve the session to use. On --resume we also remember the session's
	// stored model so the (provider, model) selection below can honour it.
	var (
		sessionID    string
		prior        []api.Message
		resumedModel string
	)
	switch {
	case resumeProvided:
		sess, err := resumeSession(ctx, st, resumeFlag)
		if err != nil {
			return err
		}
		sessionID = sess.ID
		resumedModel = sess.Model
		prior, err = st.LoadMessages(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("loading session messages: %w", err)
		}
		// Adopt the resumed session's agent unless --agent was given.
		if agentFlag == "" {
			if _, ok := registry.Resolve(sess.AgentName); ok {
				agentName = sess.AgentName
			} else if sess.AgentName != "" {
				fmt.Fprintf(os.Stderr, "warning: session agent %q is unknown; falling back to build\n", sess.AgentName)
			}
		} else if sess.AgentName != agentName {
			// User requested a different agent than the session was last
			// run with; persist the change.
			if err := st.SetAgentName(ctx, sessionID, agentName); err != nil {
				fmt.Fprintf(os.Stderr, "warning: updating session agent_name: %v\n", err)
			}
		}
	case newFlag:
		sess, err := st.CreateSession(ctx, store.CreateSessionInput{
			Model:     cfg.Default.Model,
			AgentName: agentName,
		})
		if err != nil {
			return fmt.Errorf("creating session: %w", err)
		}
		sessionID = sess.ID
	default:
		sess, err := st.CreateSession(ctx, store.CreateSessionInput{
			Model:     cfg.Default.Model,
			AgentName: agentName,
		})
		if err != nil {
			return fmt.Errorf("creating session: %w", err)
		}
		sessionID = sess.ID
	}

	// Resolve the active (provider, model). On --resume the session's
	// stored model wins when it's still listed in config; otherwise we
	// warn and fall back to the configured default, persisting the
	// fallback so the warning isn't repeated next resume.
	activeProviderName := cfg.Default.Provider
	activeModel := cfg.Default.Model
	if resumedModel != "" {
		if pname, _, ok := config.FindModel(cfg, resumedModel); ok {
			activeProviderName = pname
			activeModel = resumedModel
		} else {
			fmt.Fprintf(os.Stderr, "warning: session model %q is no longer in config; falling back to %q\n", resumedModel, cfg.Default.Model)
			if err := st.SetModel(ctx, sessionID, cfg.Default.Model); err != nil {
				fmt.Fprintf(os.Stderr, "warning: persisting fallback model: %v\n", err)
			}
		}
	}

	entry, ok := cfg.Providers[activeProviderName]
	if !ok {
		return fmt.Errorf("provider %q (for model %q) not found in config", activeProviderName, activeModel)
	}
	if entry.APIKey == "" && entry.Kind != "ollama" {
		return fmt.Errorf("missing API key for provider %q — set it in %s or export the env var referenced by api_key",
			activeProviderName, config.GlobalConfigPath())
	}

	prov, err := provider.New(api.ProviderConfig{
		Kind:    entry.Kind,
		BaseURL: entry.BaseURL,
		APIKey:  entry.APIKey,
		Model:   activeModel,
	})
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}
	healthCheck := func(ctx context.Context, providerName string, entry config.ProviderEntry, model string) string {
		return localProviderHealthMessage(ctx, providerName, entry, model)
	}
	var startupMessages []string
	if msg := healthCheck(ctx, activeProviderName, entry, activeModel); msg != "" {
		startupMessages = append(startupMessages, msg)
	}

	summarize := newSummarizer(prov, activeModel, config.ResolveMaxTokens(cfg, activeProviderName, activeModel))

	webfetch := tool.NewWebFetchTool(summarize, tool.WebFetchOptions{RespectRobotsTxt: cfg.Rules.RespectRobotsTxt})
	defer func() { _ = webfetch.Close() }()

	toolList := []agent.Tool{
		tool.NewReadTool(),
		tool.NewBashTool(),
		tool.NewEditTool(),
		tool.NewReplaceLinesTool(),
		tool.NewWriteTool(),
		tool.NewGrepTool(),
		tool.NewGlobTool(),
		webfetch,
		tool.NewTaskTool(),
		tool.NewTodoWriteTool(),
		tool.NewPlanExitTool(),
	}
	if cfg.Search != nil {
		searcher, err := search.New(*cfg.Search)
		if err != nil {
			return fmt.Errorf("init search provider: %w", err)
		}
		toolList = append(toolList, tool.NewWebSearchTool(searcher))
	}
	tools := tool.NewRegistry(toolList...)

	compactor := compact.New(compact.NewInput{
		Provider:           prov,
		DefaultLimit:       cfg.Context.DefaultLimit,
		MaxOverride:        cfg.Context.MaxOverride,
		ThresholdPct:       cfg.Compact.ThresholdPct,
		KeepRecentMessages: cfg.Compact.KeepRecentMessages,
		SummarizerModel:    cfg.Compact.Model,
		ModelContextLimit: func(model string) int {
			return config.ResolveModelContextLimit(cfg, model)
		},
	})

	// Provider concurrency cap. Resolved from the merged config via the
	// (provider, model) lookup; cloud providers default to UnboundedParallel
	// which is effectively no cap. Local LLMs (Ollama, llama.cpp, LM Studio)
	// should set max_parallel: 1 in their provider/model config.
	maxParallel := config.ResolveModelLimits(cfg, activeProviderName, activeModel)
	gate := agent.NewProviderGate(maxParallel)

	sampling := config.ResolveModelSampling(cfg, activeProviderName, activeModel)
	agnt := agent.New(agent.NewAgentInput{
		Provider:        prov,
		Model:           activeModel,
		Temperature:     sampling.TemperaturePtr(),
		PresencePenalty: sampling.PresencePenaltyPtr(),
		Tools:           tools,
		Logger:          logger,
		Evaluator:       evaluator,
		Compactor:       compactor,
		Registry:        registry,
		Gate:            gate,
		Notifier:        agent.NewDefaultNotifier(),
		Todos:           storeTodosAdapter{Store: st},
		LocalProvider:   agent.LooksLikeLocalProvider(entry),
		ModelGuidance: agent.ModelGuidanceOptions{
			ToolCallRecovery:   cfg.ModelGuidance.ToolCallRecovery,
			WorkspaceHints:     cfg.ModelGuidance.WorkspaceHints,
			LoopGuards:         cfg.ModelGuidance.LoopGuards,
			CompactToolSchemas: cfg.ModelGuidance.CompactToolSchemas,
		},
	})

	// FileChangeSink adapter: captures current sessionID via closure; each
	// event carries MessageID + ToolCallID directly, so the sink is
	// stateless beyond the session binding.
	fileChanges := &storeFileChangeSink{store: st, sessionID: sessionID}

	// CanUseTool bridge: captures *tea.Program by reference. p is assigned
	// after NewProgram below; the closure isn't called until p.Run starts,
	// so p is guaranteed non-nil by then.
	var p *tea.Program
	canUseTool := func(ctx context.Context, name, key string, input []byte) (agent.Decision, error) {
		isReadOnly := false
		disp := ""
		if t := tools.Get(name); t != nil {
			isReadOnly = t.IsReadOnly()
			disp = t.FormatForDisplay(input)
		}
		subagent := ""
		if scope, ok := agent.SubagentApprovalScopeFromContext(ctx); ok {
			subagent = scope.AgentName
		}
		req := &tui.PendingApproval{
			Name:       name,
			Key:        key,
			Input:      input,
			Disp:       disp,
			IsReadOnly: isReadOnly,
			Subagent:   subagent,
			Done:       make(chan agent.Decision, 1),
		}
		p.Send(tui.ToolApprovalRequestMsg{Req: req})
		select {
		case d := <-req.Done:
			return d, nil
		case <-ctx.Done():
			return agent.DecisionDeny, ctx.Err()
		}
	}

	// Build the SpawnTask closure for the primary's task tool. Children
	// inherit the same agent, gate, evaluator, and registry; the closure
	// hands them through agent.Run. spawnerStoreAdapter bridges the two
	// CreateChildSessionInput types (one in agent, one in store) so we
	// avoid an internal/store → internal/agent import.
	spawnTask := agent.NewSpawner(agent.SpawnerInput{
		Agent:       agnt,
		Store:       spawnerStoreAdapter{Store: st},
		FileChanges: fileChanges,
		CanUseTool:  canUseTool,
	})

	cmdRegistry := command.NewRegistry()
	if err := command.RegisterBuiltins(cmdRegistry); err != nil {
		return fmt.Errorf("registering commands: %w", err)
	}
	customDir := filepath.Join(cwd, ".prompto", "commands")
	if err := command.RegisterCustomCommands(cmdRegistry, customDir); err != nil {
		return fmt.Errorf("loading custom commands: %w", err)
	}

	model := tui.NewAppModel(tui.AppModelInput{
		Agent:               agnt,
		CanUseTool:          canUseTool,
		Extra:               projectInstructions,
		AgentsMDLoadRoot:    cwd,
		Store:               st,
		SessionID:           sessionID,
		FileChanges:         fileChanges,
		Prior:               prior,
		Ruleset:             ruleset,
		Evaluator:           evaluator,
		Compactor:           compactor,
		Version:             "v" + version.Version,
		AgentName:           agentName,
		SpawnTask:           spawnTask,
		Commands:            cmdRegistry,
		Config:              cfg,
		StartupMessages:     startupMessages,
		ProviderHealthCheck: healthCheck,
	})

	p = tea.NewProgram(model)
	_, err = p.Run()
	return err
}

func localProviderHealthMessage(ctx context.Context, providerName string, entry config.ProviderEntry, model string) string {
	if !agent.LooksLikeLocalProvider(entry) {
		return ""
	}
	err := provider.CheckLocalOpenAI(ctx, api.ProviderConfig{
		Kind:    entry.Kind,
		BaseURL: entry.BaseURL,
		APIKey:  entry.APIKey,
		Model:   model,
	})
	if err == nil {
		return ""
	}
	return fmt.Sprintf("warning: local model %q on provider %q is not ready: %v", model, providerName, err)
}

// resumeSession resolves --resume with an optional prefix. Empty prefix
// means "most recent session in this project".
func resumeSession(ctx context.Context, st *store.Store, prefix string) (store.Session, error) {
	if prefix == "" {
		all, err := st.ListSessions(ctx, 1)
		if err != nil {
			return store.Session{}, fmt.Errorf("listing sessions: %w", err)
		}
		if len(all) == 0 {
			return store.Session{}, errors.New("no sessions to resume — start one with `prompto` first")
		}
		return all[0], nil
	}
	sess, err := st.FindSessionByPrefix(ctx, prefix)
	if err != nil {
		return store.Session{}, fmt.Errorf("resolving --resume: %w", err)
	}
	return sess, nil
}

// clearProjectHistory wipes every session row from the project's
// store, then removes the on-disk artefacts that referenced those
// sessions: read-tool spill files under .prompto/tmp/ and plan-mode
// outputs under .prompto/plans/.
//
// Prompts for confirmation on stdin unless assumeYes is set. The
// confirmation requires the literal token "yes" — anything else
// aborts. Empty stores are reported as such and exit cleanly without
// prompting, since there's nothing to lose.
func clearProjectHistory(ctx context.Context, st *store.Store, cwd string, assumeYes bool) error {
	all, err := st.ListSessions(ctx, 1)
	if err != nil {
		return fmt.Errorf("checking session count: %w", err)
	}
	if len(all) == 0 {
		// Belt-and-braces clean of the artefact dirs even when the
		// DB is already empty — the user invoked --clear-history,
		// so they want a clean slate either way.
		_ = removeArtefactDir(filepath.Join(cwd, ".prompto", "tmp"))
		_ = removeArtefactDir(filepath.Join(cwd, ".prompto", "plans"))
		fmt.Println("No sessions to clear.")
		return nil
	}
	if !assumeYes {
		fmt.Fprintf(os.Stderr,
			"This will permanently delete every session in %s\n"+
				"(messages, file changes, todos, tmp spills, plan files).\n"+
				"This action cannot be undone.\n\n"+
				"Type 'yes' to confirm: ", filepath.Join(cwd, ".prompto"))
		var reply string
		if _, err := fmt.Fscanln(os.Stdin, &reply); err != nil || strings.TrimSpace(reply) != "yes" {
			fmt.Fprintln(os.Stderr, "aborted.")
			return nil
		}
	}
	n, err := st.DeleteAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("clearing history: %w", err)
	}
	if err := removeArtefactDir(filepath.Join(cwd, ".prompto", "tmp")); err != nil {
		fmt.Fprintf(os.Stderr, "warning: removing .prompto/tmp: %v\n", err)
	}
	if err := removeArtefactDir(filepath.Join(cwd, ".prompto", "plans")); err != nil {
		fmt.Fprintf(os.Stderr, "warning: removing .prompto/plans: %v\n", err)
	}
	fmt.Printf("Cleared %d session(s) from %s\n", n, filepath.Join(cwd, ".prompto"))
	return nil
}

// removeArtefactDir wipes a session-scoped directory under .prompto/.
// Missing dirs are not an error; that's the steady state on fresh
// projects. Other errors propagate so the caller can warn.
func removeArtefactDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return nil
}

// printSessions emits a human-readable list of sessions in this project.
// Children (sessions with non-empty parent_id) are indented under their
// parents. Listing only walks primaries to avoid double-printing children
// returned by ListSessions.
func printSessions(ctx context.Context, st *store.Store) error {
	sessions, err := st.ListSessions(ctx, 50)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions yet.")
		return nil
	}
	for _, sess := range sessions {
		if sess.ParentID != "" {
			continue // children are rendered under their parent below
		}
		printSessionRow(ctx, st, sess, "")

		children, err := st.ListChildren(ctx, sess.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: listing children of %s: %v\n", sess.ID[:8], err)
			continue
		}
		for _, child := range children {
			printSessionRow(ctx, st, child, "  ")
		}
	}
	return nil
}

// printSessionRow formats a single session line with the given indent.
func printSessionRow(ctx context.Context, st *store.Store, sess store.Session, indent string) {
	count, _ := st.CountMessages(ctx, sess.ID)
	title := sess.Title
	if title == "" {
		title = "(untitled)"
	}
	agentName := sess.AgentName
	if agentName == "" {
		agentName = "build"
	}
	fmt.Printf("%s%s  %-8s  %s  %s  %d msgs  %s\n",
		indent,
		sess.ID[:8],
		agentName,
		sess.UpdatedAt.Format(time.RFC3339),
		sess.Status,
		count,
		title,
	)
}

// spawnerStoreAdapter wraps *store.Store so it satisfies agent.SpawnerStore.
// The agent package and the store package each declare their own
// CreateChildSessionInput so neither has to import the other; this adapter
// translates one shape into the other. All other methods on SpawnerStore
// (AppendMessage, SetSessionStatus, LoadMessages) are forwarded by
// embedding.
type spawnerStoreAdapter struct {
	*store.Store
}

func (s spawnerStoreAdapter) CreateChildSession(ctx context.Context, in agent.CreateChildSessionInput) (string, error) {
	return s.Store.CreateChildSession(ctx, store.CreateChildSessionInput{
		ParentID:  in.ParentID,
		AgentName: in.AgentName,
		Model:     in.Model,
		Title:     in.Title,
	})
}

// storeTodosAdapter satisfies agent.TodoStore on top of *store.Store.
// The two packages each declare their own Todo struct so neither has to
// import the other; this adapter translates at the seam.
type storeTodosAdapter struct {
	*store.Store
}

func (s storeTodosAdapter) LoadTodos(ctx context.Context, sessionID string) ([]agent.Todo, error) {
	rows, err := s.Store.LoadTodos(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Todo, len(rows))
	for i, r := range rows {
		out[i] = agent.Todo{
			ID:         r.ID,
			Content:    r.Content,
			Status:     r.Status,
			ActiveForm: r.ActiveForm,
		}
	}
	return out, nil
}

func (s storeTodosAdapter) SaveTodos(ctx context.Context, sessionID string, todos []agent.Todo) error {
	rows := make([]store.Todo, len(todos))
	for i, t := range todos {
		rows[i] = store.Todo{
			ID:         t.ID,
			Content:    t.Content,
			Status:     t.Status,
			ActiveForm: t.ActiveForm,
		}
	}
	return s.Store.SaveTodos(ctx, store.SaveTodosInput{
		SessionID: sessionID,
		Todos:     rows,
	})
}

// storeFileChangeSink is the bridge from agent.FileChangeSink into
// internal/store.Store.RecordFileChange. It carries sessionID as state;
// MessageID and ToolCallID come from each event.
type storeFileChangeSink struct {
	store     *store.Store
	mu        sync.RWMutex
	sessionID string
}

func (s *storeFileChangeSink) Record(ctx context.Context, ev agent.FileChangeEvent) error {
	s.mu.RLock()
	sessionID := s.sessionID
	s.mu.RUnlock()
	return s.store.RecordFileChange(ctx, store.RecordFileChangeInput{
		SessionID:     sessionID,
		MessageID:     ev.MessageID,
		ToolCallID:    ev.ToolCallID,
		Path:          ev.Path,
		Op:            ev.Op,
		ContentBefore: ev.ContentBefore,
		ContentAfter:  ev.ContentAfter,
		Mode:          ev.Mode,
	})
}

// SetSessionID retargets the sink at a different session. Called by the
// TUI after /clear or /new rotates the active session under the same sink
// instance. Satisfies agent.SessionScopedSink.
func (s *storeFileChangeSink) SetSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}
