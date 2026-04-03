// Mnemo — Persistent memory for AI coding agents.
//
// Usage:
//
//	mnemo serve          Start HTTP + MCP server
//	mnemo mcp            Start MCP server only (stdio transport)
//	mnemo search <query> Search memories from CLI
//	mnemo save           Save a memory from CLI
//	mnemo context        Show recent context
//	mnemo stats          Show memory stats
//	mnemo cloud <cmd>    Cloud sync commands
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/MiguelAguiarDEV/mnemo/internal/cloud"
	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/auth"
	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/autosync"
	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/cloudserver"
	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/cloudstore"
	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/dashboard"
	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/jarvis"
	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/notifications"
	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/remote"
	"github.com/MiguelAguiarDEV/mnemo/internal/gateway"
	"github.com/MiguelAguiarDEV/mnemo/internal/mcp"
	"github.com/MiguelAguiarDEV/mnemo/internal/server"
	"github.com/MiguelAguiarDEV/mnemo/internal/setup"
	"github.com/MiguelAguiarDEV/mnemo/internal/store"
	mnemosync "github.com/MiguelAguiarDEV/mnemo/internal/sync"
	"github.com/MiguelAguiarDEV/mnemo/internal/tui"
	versioncheck "github.com/MiguelAguiarDEV/mnemo/internal/version"

	tea "github.com/charmbracelet/bubbletea"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// version is set via ldflags at build time by goreleaser.
// Falls back to "dev" for local builds.
var version = "dev"

var (
	storeNew      = store.New
	newHTTPServer = server.New
	startHTTP     = (*server.Server).Start

	newMCPServer          = mcp.NewServer
	newMCPServerWithTools = mcp.NewServerWithTools
	resolveMCPTools       = mcp.ResolveTools
	serveMCP              = mcpserver.ServeStdio

	newTUIModel   = func(s *store.Store) tui.Model { return tui.New(s, version) }
	newTeaProgram = tea.NewProgram
	runTeaProgram = (*tea.Program).Run

	checkForUpdates = versioncheck.CheckLatest

	setupSupportedAgents        = setup.SupportedAgents
	setupInstallAgent           = setup.Install
	setupAddClaudeCodeAllowlist = setup.AddClaudeCodeAllowlist
	scanInputLine               = fmt.Scanln

	storeSearch = func(s *store.Store, query string, opts store.SearchOptions) ([]store.SearchResult, error) {
		return s.Search(query, opts)
	}
	storeAddObservation = func(s *store.Store, p store.AddObservationParams) (int64, error) { return s.AddObservation(p) }
	storeTimeline       = func(s *store.Store, observationID int64, before, after int) (*store.TimelineResult, error) {
		return s.Timeline(observationID, before, after)
	}
	storeFormatContext = func(s *store.Store, project, scope string) (string, error) { return s.FormatContext(project, scope) }
	storeStats         = func(s *store.Store) (*store.Stats, error) { return s.Stats() }
	storeExport        = func(s *store.Store) (*store.ExportData, error) { return s.Export() }
	jsonMarshalIndent  = json.MarshalIndent

	syncStatus = func(sy *mnemosync.Syncer) (localChunks int, remoteChunks int, pendingImport int, err error) {
		return sy.Status()
	}
	syncImport = func(sy *mnemosync.Syncer) (*mnemosync.ImportResult, error) { return sy.Import() }
	syncExport = func(sy *mnemosync.Syncer, createdBy, project string) (*mnemosync.SyncResult, error) {
		return sy.Export(createdBy, project)
	}

	exitFunc = os.Exit

	// Autosync test seams
	autosyncNew       = autosync.New
	autosyncDefaultCg = autosync.DefaultConfig

	// Cloud test seams
	cloudStoreNew   = cloudstore.New
	cloudStoreClose = func(cs *cloudstore.CloudStore) error { return cs.Close() }
	cloudAuthNew    = auth.NewService
	cloudServerNew  = func(cs *cloudstore.CloudStore, svc *auth.Service, port int, opts ...cloudserver.Option) *cloudserver.CloudServer {
		return cloudserver.New(cs, svc, port, opts...)
	}
	cloudServerStart   = func(srv *cloudserver.CloudServer) error { return srv.Start() }
	remoteTransportNew = remote.NewRemoteTransport
	cloudHTTPClient    = func() *http.Client { return http.DefaultClient }
	stdinScanner       = func() *bufio.Scanner { return bufio.NewScanner(os.Stdin) }
	userHomeDir        = os.UserHomeDir
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		exitFunc(1)
	}

	// Check for updates on every invocation (2s timeout, silent on failure).
	if msg := checkForUpdates(version); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		fmt.Fprintln(os.Stderr)
	}

	cfg, cfgErr := store.DefaultConfig()
	if cfgErr != nil {
		// Fallback: try to resolve home directory from environment variables
		// that os.UserHomeDir() might have missed (e.g. MCP subprocesses on
		// Windows where %USERPROFILE% is not propagated).
		if home := resolveHomeFallback(); home != "" {
			log.Printf("[mnemo] UserHomeDir failed, using fallback: %s", home)
			cfg = store.FallbackConfig(filepath.Join(home, ".mnemo"))
		} else {
			fatal(cfgErr)
		}
	}

	// Allow overriding data dir via env
	if dir := os.Getenv("MNEMO_DATA_DIR"); dir != "" {
		cfg.DataDir = dir
	}

	// Migrate orphaned databases that ended up in wrong locations
	// (e.g. drive root on Windows due to previous bug).
	migrateOrphanedDB(cfg.DataDir)

	switch os.Args[1] {
	case "serve":
		cmdServe(cfg)
	case "mcp":
		cmdMCP(cfg)
	case "tui":
		cmdTUI(cfg)
	case "search":
		cmdSearch(cfg)
	case "save":
		cmdSave(cfg)
	case "timeline":
		cmdTimeline(cfg)
	case "context":
		cmdContext(cfg)
	case "stats":
		cmdStats(cfg)
	case "export":
		cmdExport(cfg)
	case "import":
		cmdImport(cfg)
	case "sync":
		cmdSync(cfg)
	case "cloud":
		cmdCloud(cfg)
	case "setup":
		cmdSetup()
	case "version", "--version", "-v":
		fmt.Printf("mnemo %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		exitFunc(1)
	}
}

// ─── Autosync Adapter ────────────────────────────────────────────────────────

// syncStatusAdapter bridges autosync.Manager.Status() → server.SyncStatusProvider.
type syncStatusAdapter struct {
	mgr *autosync.Manager
}

func (a *syncStatusAdapter) Status() server.SyncStatus {
	s := a.mgr.Status()
	return server.SyncStatus{
		Phase:               s.Phase,
		LastError:           s.LastError,
		ConsecutiveFailures: s.ConsecutiveFailures,
		BackoffUntil:        s.BackoffUntil,
		LastSyncAt:          s.LastSyncAt,
	}
}

// tryStartAutosync attempts to create and start a background sync manager.
// Returns (manager, cancel) on success, or (nil, nil) if cloud is not configured.
func tryStartAutosync(s *store.Store, dataDir string) (*autosync.Manager, context.CancelFunc) {
	serverURL, token, err := resolveCloudClientConfig(dataDir, "", "", true)
	if err != nil || serverURL == "" || token == "" {
		return nil, nil
	}

	rt, err := remoteTransportNew(serverURL, token)
	if err != nil {
		log.Printf("[mnemo] autosync: failed to create transport: %v", err)
		return nil, nil
	}

	// Configure token refresh if available.
	if cc, err := loadCloudConfig(dataDir); err == nil && cc != nil && cc.ServerURL == serverURL && cc.RefreshToken != "" {
		rt.SetTokenRefresher(cc.RefreshToken, func(newToken string) error {
			cc.Token = newToken
			return saveCloudConfig(dataDir, cc)
		})
	}

	cfg := autosyncDefaultCg()
	mgr := autosyncNew(s, rt, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	go mgr.Run(ctx)

	log.Printf("[mnemo] autosync: background sync started (server: %s)", serverURL)
	return mgr, cancel
}

// ─── Commands ────────────────────────────────────────────────────────────────

func cmdServe(cfg store.Config) {
	port := 7437 // "ENGR" on phone keypad vibes
	if p := os.Getenv("MNEMO_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	// Allow: mnemo serve 8080
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil {
			port = n
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	srv := newHTTPServer(s, port)

	// Start background autosync if cloud is configured.
	if mgr, cancel := tryStartAutosync(s, cfg.DataDir); mgr != nil {
		defer cancel()
		srv.SetOnWrite(mgr.NotifyDirty)
		srv.SetSyncStatus(&syncStatusAdapter{mgr: mgr})
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[mnemo] shutting down...")
		exitFunc(0)
	}()

	if err := startHTTP(srv); err != nil {
		fatal(err)
	}
}

func cmdMCP(cfg store.Config) {
	// Parse --tools flag: mnemo mcp --tools=agent
	toolsFilter := ""
	for i := 2; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--tools=") {
			toolsFilter = strings.TrimPrefix(os.Args[i], "--tools=")
		} else if os.Args[i] == "--tools" && i+1 < len(os.Args) {
			toolsFilter = os.Args[i+1]
			i++
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	// Start background autosync if cloud is configured.
	// MCP is a long-lived stdio process — same lifecycle as serve.
	if _, cancel := tryStartAutosync(s, cfg.DataDir); cancel != nil {
		defer cancel()
	}

	var mcpSrv *mcpserver.MCPServer
	if toolsFilter != "" {
		allowlist := resolveMCPTools(toolsFilter)
		mcpSrv = newMCPServerWithTools(s, allowlist)
	} else {
		mcpSrv = newMCPServer(s)
	}

	if err := serveMCP(mcpSrv); err != nil {
		fatal(err)
	}
}

func cmdTUI(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	model := newTUIModel(s)
	p := newTeaProgram(model)
	if _, err := runTeaProgram(p); err != nil {
		fatal(err)
	}
}

func cmdSearch(cfg store.Config) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mnemo search <query> [--type TYPE] [--project PROJECT] [--scope SCOPE] [--limit N] [--remote URL] [--token TOKEN]")
		exitFunc(1)
	}

	// Collect the query (everything that's not a flag)
	var queryParts []string
	opts := store.SearchOptions{Limit: 10}
	remoteURL := ""
	token := ""

	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--type":
			if i+1 < len(os.Args) {
				opts.Type = os.Args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(os.Args) {
				opts.Project = os.Args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(os.Args) {
				if n, err := strconv.Atoi(os.Args[i+1]); err == nil {
					opts.Limit = n
				}
				i++
			}
		case "--scope":
			if i+1 < len(os.Args) {
				opts.Scope = os.Args[i+1]
				i++
			}
		case "--remote", "-r":
			if i+1 < len(os.Args) {
				remoteURL = os.Args[i+1]
				i++
			}
		case "--token", "-t":
			if i+1 < len(os.Args) {
				token = os.Args[i+1]
				i++
			}
		default:
			queryParts = append(queryParts, os.Args[i])
		}
	}

	query := strings.Join(queryParts, " ")
	if query == "" {
		fmt.Fprintln(os.Stderr, "error: search query is required")
		exitFunc(1)
	}

	resolvedRemoteURL, resolvedToken, err := resolveCloudClientConfig(cfg.DataDir, remoteURL, token, false)
	if err != nil {
		fatal(err)
		return
	}

	// Remote cloud search
	if resolvedRemoteURL != "" {
		remoteSearch(resolvedRemoteURL, resolvedToken, query, opts)
		return
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
		return
	}
	defer s.Close()

	results, err := storeSearch(s, query, opts)
	if err != nil {
		fatal(err)
		return
	}

	if len(results) == 0 {
		fmt.Printf("No memories found for: %q\n", query)
		return
	}

	fmt.Printf("Found %d memories:\n\n", len(results))
	for i, r := range results {
		project := ""
		if r.Project != nil {
			project = fmt.Sprintf(" | project: %s", *r.Project)
		}
		fmt.Printf("[%d] #%d (%s) — %s\n    %s\n    %s%s | scope: %s\n\n",
			i+1, r.ID, r.Type, r.Title,
			truncate(r.Content, 300),
			r.CreatedAt, project, r.Scope)
	}
}

func cmdSave(cfg store.Config) {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: mnemo save <title> <content> [--type TYPE] [--project PROJECT] [--scope SCOPE] [--topic TOPIC_KEY]")
		exitFunc(1)
	}

	title := os.Args[2]
	content := os.Args[3]
	typ := "manual"
	project := ""
	scope := "project"
	topicKey := ""

	for i := 4; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--type":
			if i+1 < len(os.Args) {
				typ = os.Args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(os.Args) {
				project = os.Args[i+1]
				i++
			}
		case "--scope":
			if i+1 < len(os.Args) {
				scope = os.Args[i+1]
				i++
			}
		case "--topic":
			if i+1 < len(os.Args) {
				topicKey = os.Args[i+1]
				i++
			}
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	sessionID := "manual-save"
	if project != "" {
		sessionID = "manual-save-" + project
	}
	s.CreateSession(sessionID, project, "")
	id, err := storeAddObservation(s, store.AddObservationParams{
		SessionID: sessionID,
		Type:      typ,
		Title:     title,
		Content:   content,
		Project:   project,
		Scope:     scope,
		TopicKey:  topicKey,
	})
	if err != nil {
		fatal(err)
	}

	fmt.Printf("Memory saved: #%d %q (%s)\n", id, title, typ)
}

func cmdTimeline(cfg store.Config) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mnemo timeline <observation_id> [--before N] [--after N]")
		exitFunc(1)
	}

	obsID, err := strconv.ParseInt(os.Args[2], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid observation id %q\n", os.Args[2])
		exitFunc(1)
	}

	before, after := 5, 5
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--before":
			if i+1 < len(os.Args) {
				if n, err := strconv.Atoi(os.Args[i+1]); err == nil {
					before = n
				}
				i++
			}
		case "--after":
			if i+1 < len(os.Args) {
				if n, err := strconv.Atoi(os.Args[i+1]); err == nil {
					after = n
				}
				i++
			}
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	result, err := storeTimeline(s, obsID, before, after)
	if err != nil {
		fatal(err)
	}

	// Session header
	if result.SessionInfo != nil {
		summary := ""
		if result.SessionInfo.Summary != nil {
			summary = fmt.Sprintf(" — %s", truncate(*result.SessionInfo.Summary, 100))
		}
		fmt.Printf("Session: %s (%s)%s\n", result.SessionInfo.Project, result.SessionInfo.StartedAt, summary)
		fmt.Printf("Total observations in session: %d\n\n", result.TotalInRange)
	}

	// Before
	if len(result.Before) > 0 {
		fmt.Println("─── Before ───")
		for _, e := range result.Before {
			fmt.Printf("  #%d [%s] %s — %s\n", e.ID, e.Type, e.Title, truncate(e.Content, 150))
		}
		fmt.Println()
	}

	// Focus
	fmt.Printf(">>> #%d [%s] %s <<<\n", result.Focus.ID, result.Focus.Type, result.Focus.Title)
	fmt.Printf("    %s\n", truncate(result.Focus.Content, 500))
	fmt.Printf("    %s\n\n", result.Focus.CreatedAt)

	// After
	if len(result.After) > 0 {
		fmt.Println("─── After ───")
		for _, e := range result.After {
			fmt.Printf("  #%d [%s] %s — %s\n", e.ID, e.Type, e.Title, truncate(e.Content, 150))
		}
	}
}

func cmdContext(cfg store.Config) {
	project := ""
	remoteURL := ""
	token := ""
	scope := ""

	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--remote", "-r":
			if i+1 < len(os.Args) {
				remoteURL = os.Args[i+1]
				i++
			}
		case "--token", "-t":
			if i+1 < len(os.Args) {
				token = os.Args[i+1]
				i++
			}
		case "--scope":
			if i+1 < len(os.Args) {
				scope = os.Args[i+1]
				i++
			}
		default:
			if project == "" {
				project = os.Args[i]
			}
		}
	}

	resolvedRemoteURL, resolvedToken, err := resolveCloudClientConfig(cfg.DataDir, remoteURL, token, false)
	if err != nil {
		fatal(err)
		return
	}

	// Remote cloud context
	if resolvedRemoteURL != "" {
		remoteContext(resolvedRemoteURL, resolvedToken, project, scope)
		return
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	ctx, err := storeFormatContext(s, project, "")
	if err != nil {
		fatal(err)
	}

	if ctx == "" {
		fmt.Println("No previous session memories found.")
		return
	}

	fmt.Print(ctx)
}

func cmdStats(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	stats, err := storeStats(s)
	if err != nil {
		fatal(err)
	}

	projects := "none yet"
	if len(stats.Projects) > 0 {
		projects = strings.Join(stats.Projects, ", ")
	}

	fmt.Printf("Mnemo Memory Stats\n")
	fmt.Printf("  Sessions:     %d\n", stats.TotalSessions)
	fmt.Printf("  Observations: %d\n", stats.TotalObservations)
	fmt.Printf("  Prompts:      %d\n", stats.TotalPrompts)
	fmt.Printf("  Projects:     %s\n", projects)
	fmt.Printf("  Database:     %s/mnemo.db\n", cfg.DataDir)
}

func cmdExport(cfg store.Config) {
	outFile := "mnemo-export.json"
	if len(os.Args) > 2 {
		outFile = os.Args[2]
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	data, err := storeExport(s)
	if err != nil {
		fatal(err)
	}

	out, err := jsonMarshalIndent(data, "", "  ")
	if err != nil {
		fatal(err)
	}

	if err := os.WriteFile(outFile, out, 0644); err != nil {
		fatal(err)
	}

	fmt.Printf("Exported to %s\n", outFile)
	fmt.Printf("  Sessions:     %d\n", len(data.Sessions))
	fmt.Printf("  Observations: %d\n", len(data.Observations))
	fmt.Printf("  Prompts:      %d\n", len(data.Prompts))
}

func cmdImport(cfg store.Config) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mnemo import <file.json>")
		exitFunc(1)
	}

	inFile := os.Args[2]
	raw, err := os.ReadFile(inFile)
	if err != nil {
		fatal(fmt.Errorf("read %s: %w", inFile, err))
	}

	var data store.ExportData
	if err := json.Unmarshal(raw, &data); err != nil {
		fatal(fmt.Errorf("parse %s: %w", inFile, err))
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	result, err := s.Import(&data)
	if err != nil {
		fatal(err)
	}

	fmt.Printf("Imported from %s\n", inFile)
	fmt.Printf("  Sessions:     %d\n", result.SessionsImported)
	fmt.Printf("  Observations: %d\n", result.ObservationsImported)
	fmt.Printf("  Prompts:      %d\n", result.PromptsImported)
}

func cmdSync(cfg store.Config) {
	// Parse flags
	doImport := false
	doStatus := false
	doAll := false
	project := ""
	remoteURL := ""
	token := ""
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--import":
			doImport = true
		case "--status":
			doStatus = true
		case "--all":
			doAll = true
		case "--project":
			if i+1 < len(os.Args) {
				project = os.Args[i+1]
				i++
			}
		case "--remote", "-r":
			if i+1 < len(os.Args) {
				remoteURL = os.Args[i+1]
				i++
			}
		case "--token", "-t":
			if i+1 < len(os.Args) {
				token = os.Args[i+1]
				i++
			}
		}
	}

	// Default project to current directory name (so sync only exports
	// memories for THIS project, not everything in the global DB).
	// --all skips project filtering entirely — exports everything.
	if !doAll && project == "" {
		if cwd, err := os.Getwd(); err == nil {
			project = filepath.Base(cwd)
		}
	}

	syncDir := ".mnemo"

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	resolvedRemoteURL, resolvedToken, err := resolveCloudClientConfig(cfg.DataDir, remoteURL, token, false)
	if err != nil {
		fatal(err)
	}
	if resolvedRemoteURL != "" {
		rt, err := remoteTransportNew(resolvedRemoteURL, resolvedToken)
		if err != nil {
			fatal(err)
		}
		handleRemoteSync(s, rt, doStatus, doImport, doAll, project)
		return
	}

	sy := mnemosync.NewLocal(s, syncDir)

	if doStatus {
		local, remote, pending, err := syncStatus(sy)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Sync status:\n")
		fmt.Printf("  Local chunks:    %d\n", local)
		fmt.Printf("  Remote chunks:   %d\n", remote)
		fmt.Printf("  Pending import:  %d\n", pending)
		return
	}

	if doImport {
		result, err := syncImport(sy)
		if err != nil {
			fatal(err)
		}

		if result.ChunksImported == 0 {
			fmt.Println("Already up to date — no new chunks to import.")
			if result.ChunksSkipped > 0 {
				fmt.Printf("  (%d chunks already imported)\n", result.ChunksSkipped)
			}
			return
		}

		fmt.Printf("Imported %d new chunk(s) from .mnemo/\n", result.ChunksImported)
		fmt.Printf("  Sessions:     %d\n", result.SessionsImported)
		fmt.Printf("  Observations: %d\n", result.ObservationsImported)
		fmt.Printf("  Prompts:      %d\n", result.PromptsImported)
		if result.ChunksSkipped > 0 {
			fmt.Printf("  Skipped:      %d (already imported)\n", result.ChunksSkipped)
		}
		return
	}

	// Export: DB → new chunk
	username := mnemosync.GetUsername()
	if doAll {
		fmt.Println("Exporting ALL memories (all projects)...")
	} else {
		fmt.Printf("Exporting memories for project %q...\n", project)
	}
	result, err := syncExport(sy, username, project)
	if err != nil {
		fatal(err)
	}

	if result.IsEmpty {
		if doAll {
			fmt.Println("Nothing new to sync — all memories already exported.")
		} else {
			fmt.Printf("Nothing new to sync for project %q — all memories already exported.\n", project)
		}
		return
	}

	fmt.Printf("Created chunk %s\n", result.ChunkID)
	fmt.Printf("  Sessions:     %d\n", result.SessionsExported)
	fmt.Printf("  Observations: %d\n", result.ObservationsExported)
	fmt.Printf("  Prompts:      %d\n", result.PromptsExported)
	fmt.Println()
	fmt.Println("Add to git:")
	fmt.Printf("  git add .mnemo/ && git commit -m \"sync mnemo memories\"\n")
}

func handleRemoteSync(s *store.Store, transport mnemosync.Transport, doStatus, doImport, doAll bool, project string) {
	sy := mnemosync.NewWithTransport(s, transport)

	if doStatus {
		local, remoteCount, pending, err := syncStatus(sy)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Sync status:\n")
		fmt.Printf("  Local chunks:    %d\n", local)
		fmt.Printf("  Remote chunks:   %d\n", remoteCount)
		fmt.Printf("  Pending import:  %d\n", pending)
		return
	}

	if doImport {
		result, err := syncImport(sy)
		if err != nil {
			fatal(fmt.Errorf("pull: %w", err))
		}
		if result.ChunksImported == 0 {
			fmt.Println("Nothing new to pull.")
			return
		}
		fmt.Printf("Pulled %d chunk(s) (%d sessions, %d observations, %d prompts)\n",
			result.ChunksImported, result.SessionsImported, result.ObservationsImported, result.PromptsImported)
		return
	}

	username := mnemosync.GetUsername()
	if doAll {
		fmt.Println("Pushing ALL memories to remote...")
	} else if project != "" {
		fmt.Printf("Pushing memories for project %q to remote...\n", project)
	}

	exportResult, err := syncExport(sy, username, project)
	if err != nil {
		fatal(fmt.Errorf("push: %w", err))
	}
	if exportResult.IsEmpty {
		fmt.Println("Nothing new to push.")
	} else {
		fmt.Printf("Pushed chunk %s (%d sessions, %d observations, %d prompts)\n",
			exportResult.ChunkID,
			exportResult.SessionsExported,
			exportResult.ObservationsExported,
			exportResult.PromptsExported)
	}

	importResult, err := syncImport(sy)
	if err != nil {
		fatal(fmt.Errorf("pull: %w", err))
	}
	if importResult.ChunksImported == 0 {
		fmt.Println("Nothing new to pull.")
		return
	}
	fmt.Printf("Pulled %d chunk(s) (%d sessions, %d observations, %d prompts)\n",
		importResult.ChunksImported,
		importResult.SessionsImported,
		importResult.ObservationsImported,
		importResult.PromptsImported)
}

func cmdSetup() {
	agents := setupSupportedAgents()

	// If agent name given directly: mnemo setup opencode
	if len(os.Args) > 2 && !strings.HasPrefix(os.Args[2], "-") {
		result, err := setupInstallAgent(os.Args[2])
		if err != nil {
			fatal(err)
		}
		fmt.Printf("✓ Installed %s plugin (%d files)\n", result.Agent, result.Files)
		fmt.Printf("  → %s\n", result.Destination)
		printPostInstall(result.Agent)
		return
	}

	// Interactive selection
	fmt.Println("mnemo setup — Install agent plugin")
	fmt.Println()
	fmt.Println("Which agent do you want to set up?")
	fmt.Println()

	for i, a := range agents {
		fmt.Printf("  [%d] %s\n", i+1, a.Description)
		fmt.Printf("      Install to: %s\n\n", a.InstallDir)
	}

	fmt.Print("Enter choice (1-", len(agents), "): ")
	var input string
	scanInputLine(&input)

	choice, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || choice < 1 || choice > len(agents) {
		fmt.Fprintln(os.Stderr, "Invalid choice.")
		exitFunc(1)
	}

	selected := agents[choice-1]
	fmt.Printf("\nInstalling %s plugin...\n", selected.Name)

	result, err := setupInstallAgent(selected.Name)
	if err != nil {
		fatal(err)
	}

	fmt.Printf("✓ Installed %s plugin (%d files)\n", result.Agent, result.Files)
	fmt.Printf("  → %s\n", result.Destination)
	printPostInstall(result.Agent)
}

func printPostInstall(agent string) {
	switch agent {
	case "opencode":
		fmt.Println("\nNext steps:")
		fmt.Println("  1. Restart OpenCode — plugin + MCP server are ready")
		fmt.Println("  2. Run 'mnemo serve &' for session tracking (HTTP API)")
	case "claude-code":
		// Offer to add mnemo tools to the permissions allowlist
		fmt.Print("\nAdd mnemo tools to ~/.claude/settings.json allowlist?\n")
		fmt.Print("This prevents Claude Code from asking permission on every tool call.\n")
		fmt.Print("Add to allowlist? (y/N): ")
		var answer string
		scanInputLine(&answer)
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer == "y" || answer == "yes" {
			if err := setupAddClaudeCodeAllowlist(); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not update allowlist: %v\n", err)
				fmt.Fprintln(os.Stderr, "  You can add them manually to permissions.allow in ~/.claude/settings.json")
			} else {
				fmt.Println("  ✓ Mnemo tools added to allowlist")
			}
		} else {
			fmt.Println("  Skipped. You can add them later to permissions.allow in ~/.claude/settings.json")
		}

		fmt.Println("\nNext steps:")
		fmt.Println("  1. Restart Claude Code — the plugin is active immediately")
		fmt.Println("  2. Verify with: claude plugin list")
	case "gemini-cli":
		fmt.Println("\nNext steps:")
		fmt.Println("  1. Restart Gemini CLI so MCP config is reloaded")
		fmt.Println("  2. Verify ~/.gemini/settings.json includes mcpServers.mnemo")
		fmt.Println("  3. Verify ~/.gemini/system.md + ~/.gemini/.env exist for compaction recovery")
	case "codex":
		fmt.Println("\nNext steps:")
		fmt.Println("  1. Restart Codex so MCP config is reloaded")
		fmt.Println("  2. Verify ~/.codex/config.toml has [mcp_servers.mnemo]")
		fmt.Println("  3. Verify model_instructions_file + experimental_compact_prompt_file are set")
	}
}

// ─── Cloud Commands ──────────────────────────────────────────────────────────

// CloudConfig holds saved cloud credentials at ~/.mnemo/cloud.json.
type CloudConfig struct {
	ServerURL    string `json:"server_url"`
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
}

func cloudConfigPath(dataDir string) string {
	if dataDir != "" {
		return filepath.Join(dataDir, "cloud.json")
	}
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mnemo", "cloud.json")
}

func loadCloudConfig(dataDir string) (*CloudConfig, error) {
	path := cloudConfigPath(dataDir)
	if path == "" {
		return nil, fmt.Errorf("could not determine home directory")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cc CloudConfig
	if err := json.Unmarshal(data, &cc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cc, nil
}

func resolveCloudClientConfig(dataDir, cliServerURL, cliToken string, useConfigServer bool) (string, string, error) {
	serverURL := cliServerURL
	token := cliToken

	if serverURL == "" {
		serverURL = os.Getenv("MNEMO_REMOTE_URL")
	}
	if token == "" {
		token = os.Getenv("MNEMO_TOKEN")
	}

	var cc *CloudConfig
	if loaded, err := loadCloudConfig(dataDir); err == nil {
		cc = loaded
	}
	if useConfigServer && serverURL == "" && cc != nil {
		serverURL = cc.ServerURL
	}
	if token == "" && cc != nil {
		token = cc.Token
	}

	if serverURL == "" {
		return "", "", nil
	}
	if token == "" {
		return "", "", fmt.Errorf("cloud config missing token (provide --token, MNEMO_TOKEN, or login first)")
	}
	return serverURL, token, nil
}

func saveCloudConfig(dataDir string, cc *CloudConfig) error {
	path := cloudConfigPath(dataDir)
	if path == "" {
		return fmt.Errorf("could not determine home directory")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func cmdCloud(cfg store.Config) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mnemo cloud <serve|register|login|sync|sync-status|status|api-key|enroll|unenroll|projects>")
		exitFunc(1)
		return
	}

	switch os.Args[2] {
	case "serve":
		cmdCloudServe()
	case "register":
		cmdCloudRegister(cfg.DataDir)
	case "login":
		cmdCloudLogin(cfg.DataDir)
	case "sync":
		cmdCloudSync(cfg)
	case "sync-status":
		cmdCloudSyncStatus(cfg)
	case "status":
		cmdCloudStatus(cfg)
	case "api-key":
		cmdCloudAPIKey(cfg.DataDir)
	case "enroll":
		cmdCloudEnroll(cfg)
	case "unenroll":
		cmdCloudUnenroll(cfg)
	case "projects":
		cmdCloudProjects(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown cloud command: %s\n", os.Args[2])
		fmt.Fprintln(os.Stderr, "usage: mnemo cloud <serve|register|login|sync|sync-status|status|api-key|enroll|unenroll|projects>")
		exitFunc(1)
		return
	}
}

func cmdCloudServe() {
	cloudCfg := cloud.ConfigFromEnv()
	cloudCfg.DSN = cloud.DatabaseURLFromEnv()
	cloudCfg.JWTSecret = cloud.JWTSecretFromEnv()

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--port":
			if i+1 < len(os.Args) {
				if n, err := strconv.Atoi(os.Args[i+1]); err == nil {
					cloudCfg.Port = n
				}
				i++
			}
		case "--database-url":
			if i+1 < len(os.Args) {
				cloudCfg.DSN = os.Args[i+1]
				i++
			}
		}
	}

	if cloudCfg.DSN == "" {
		fmt.Fprintln(os.Stderr, "error: --database-url or MNEMO_DATABASE_URL is required")
		exitFunc(1)
		return
	}

	if cloudCfg.JWTSecret == "" {
		fmt.Fprintln(os.Stderr, "error: MNEMO_JWT_SECRET environment variable is required (>= 32 chars)")
		exitFunc(1)
		return
	}

	cs, err := cloudStoreNew(cloudCfg)
	if err != nil {
		fatal(err)
		return
	}
	defer cloudStoreClose(cs)

	authSvc, err := cloudAuthNew(cs, cloudCfg.JWTSecret)
	if err != nil {
		fatal(err)
		return
	}

	dashCfg := dashboard.DashboardConfig{
		AdminEmail: cloudCfg.AdminEmail,
	}
	// Discord DM notifications (optional — skipped if env vars are missing).
	var notifier notifications.Notifier
	if botToken := os.Getenv("DISCORD_BOT_TOKEN"); botToken != "" {
		if userID := os.Getenv("DISCORD_USER_ID"); userID != "" {
			log.Printf("[mnemo-cloud] Discord DM notifications enabled for user %s", userID)
			notifier = notifications.NewDiscord(botToken, userID)
		}
	}

	// JARVIS orchestrator
	adapter := jarvis.NewStoreAdapter(cs)
	orch := jarvis.New(jarvis.OrchestratorConfig{
		Store:    adapter,
		Notifier: notifier,
	})

	// Background context — cancelled on SIGINT/SIGTERM for graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start background JARVIS subsystems.
	log.Println("[mnemo-cloud] starting JARVIS dream (memory consolidation)")
	orch.StartDream(ctx)

	log.Println("[mnemo-cloud] starting JARVIS ticker (proactive health checks)")
	orch.StartTicker(ctx)

	// ── Gateway setup ──────────────────────────────────────────────────────
	// Create Gateway with JARVIS orchestrator as handler.
	gwHandler := func(gwCtx context.Context, msg gateway.IncomingMessage) (gateway.OutgoingMessage, error) {
		convIDStr := msg.Metadata["conversation_id"]
		convID, _ := strconv.ParseInt(convIDStr, 10, 64)
		resp, chatErr := orch.Chat(msg.SenderID, convID, msg.Text, func(string) {})
		if chatErr != nil {
			return gateway.OutgoingMessage{}, chatErr
		}
		return gateway.OutgoingMessage{
			ChannelName: msg.ChannelName,
			RecipientID: msg.SenderID,
			Text:        resp,
			Format:      gateway.FormatMarkdown,
			ReplyTo:     msg.ReplyTo,
		}, nil
	}
	gw := gateway.New(gwHandler)

	// Web channel (always enabled).
	webCh := gateway.NewWebChannel(gw)
	if err := gw.Register(webCh); err != nil {
		log.Printf("[mnemo-cloud] WARN: failed to register web channel: %v", err)
	}

	// Discord channel (opt-in via JARVIS_DISCORD_ENABLED).
	discordEnabled := os.Getenv("JARVIS_DISCORD_ENABLED") == "true"
	discordToken := os.Getenv("DISCORD_BOT_TOKEN")
	discordUserIDs := os.Getenv("DISCORD_USER_ID")

	var discordCh *gateway.DiscordChannel
	if discordEnabled && discordToken != "" {
		allowedUsers := strings.Split(discordUserIDs, ",")
		discordCh = gateway.NewDiscordChannel(discordToken, allowedUsers,
			gateway.WithDiscordChannelGateway(gw),
		)
		if err := gw.Register(discordCh); err != nil {
			log.Printf("[mnemo-cloud] WARN: failed to register discord channel: %v", err)
		} else {
			log.Println("[mnemo-cloud] Discord channel registered (JARVIS_DISCORD_ENABLED=true)")
		}
	} else if discordEnabled && discordToken == "" {
		log.Println("[mnemo-cloud] WARN: JARVIS_DISCORD_ENABLED=true but DISCORD_BOT_TOKEN is empty — Discord channel not started")
	}

	// Start Gateway (starts all registered channels).
	if err := gw.Start(ctx); err != nil {
		log.Printf("[mnemo-cloud] WARN: gateway start failed: %v", err)
	}

	opts := []cloudserver.Option{
		cloudserver.WithDashboard(dashCfg),
		cloudserver.WithDSN(cloudCfg.DSN),
		cloudserver.WithJARVIS(orch),
		cloudserver.WithJobs(jarvis.NewJobServiceAdapter(orch)),
		cloudserver.WithGateway(gw),
		cloudserver.WithWebChannel(webCh),
	}

	if notifier != nil {
		opts = append(opts, cloudserver.WithNotifier(notifier))
	}

	srv := cloudServerNew(cs, authSvc, cloudCfg.Port, opts...)
	if err := cloudServerStart(srv); err != nil {
		fatal(err)
		return
	}
}

func cmdCloudRegister(dataDir string) {
	serverURL := ""
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--server":
			if i+1 < len(os.Args) {
				serverURL = os.Args[i+1]
				i++
			}
		}
	}
	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "error: --server is required")
		exitFunc(1)
		return
	}

	scanner := stdinScanner()

	fmt.Print("Username: ")
	scanner.Scan()
	username := strings.TrimSpace(scanner.Text())

	fmt.Print("Email: ")
	scanner.Scan()
	email := strings.TrimSpace(scanner.Text())

	fmt.Print("Password: ")
	scanner.Scan()
	password := strings.TrimSpace(scanner.Text())

	if username == "" || email == "" || password == "" {
		fmt.Fprintln(os.Stderr, "error: username, email, and password are required")
		exitFunc(1)
		return
	}

	// Call POST /auth/register
	reqBody, _ := json.Marshal(map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	})

	resp, err := cloudHTTPClient().Post(
		strings.TrimRight(serverURL, "/")+"/auth/register",
		"application/json",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		fatal(fmt.Errorf("register: %w", err))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			fatal(fmt.Errorf("register failed: %s", errResp.Error))
		}
		fatal(fmt.Errorf("register failed: %s", resp.Status))
	}

	var result auth.AuthResult
	if err := json.Unmarshal(body, &result); err != nil {
		fatal(fmt.Errorf("parse register response: %w", err))
	}

	// Save credentials to config file
	cc := &CloudConfig{
		ServerURL:    serverURL,
		Token:        result.AccessToken,
		RefreshToken: result.RefreshToken,
		UserID:       result.UserID,
		Username:     result.Username,
	}
	if err := saveCloudConfig(dataDir, cc); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save config: %v\n", err)
	}

	fmt.Printf("Registered as %s (user_id: %s)\n", result.Username, result.UserID)
	fmt.Printf("Credentials saved to %s\n", cloudConfigPath(dataDir))
}

func cmdCloudLogin(dataDir string) {
	serverURL := ""
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--server":
			if i+1 < len(os.Args) {
				serverURL = os.Args[i+1]
				i++
			}
		}
	}
	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "error: --server is required")
		exitFunc(1)
		return
	}

	scanner := stdinScanner()

	fmt.Print("Username or email: ")
	scanner.Scan()
	identifier := strings.TrimSpace(scanner.Text())

	fmt.Print("Password: ")
	scanner.Scan()
	password := strings.TrimSpace(scanner.Text())

	if identifier == "" || password == "" {
		fmt.Fprintln(os.Stderr, "error: username or email and password are required")
		exitFunc(1)
		return
	}

	// Call POST /auth/login
	reqBody, _ := json.Marshal(map[string]string{
		"identifier": identifier,
		"password":   password,
	})

	resp, err := cloudHTTPClient().Post(
		strings.TrimRight(serverURL, "/")+"/auth/login",
		"application/json",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		fatal(fmt.Errorf("login: %w", err))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			fatal(fmt.Errorf("login failed: %s", errResp.Error))
		}
		fatal(fmt.Errorf("login failed: %s", resp.Status))
	}

	var result auth.AuthResult
	if err := json.Unmarshal(body, &result); err != nil {
		fatal(fmt.Errorf("parse login response: %w", err))
	}

	cc := &CloudConfig{
		ServerURL:    serverURL,
		Token:        result.AccessToken,
		RefreshToken: result.RefreshToken,
		UserID:       result.UserID,
		Username:     result.Username,
	}
	if err := saveCloudConfig(dataDir, cc); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save config: %v\n", err)
	}

	fmt.Printf("Logged in as %s\n", result.Username)
	fmt.Printf("Credentials saved to %s\n", cloudConfigPath(dataDir))
}

func cmdCloudSync(cfg store.Config) {
	serverURL := ""
	token := ""
	useLegacy := false
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--server", "--remote", "-r":
			if i+1 < len(os.Args) {
				serverURL = os.Args[i+1]
				i++
			}
		case "--token", "-t":
			if i+1 < len(os.Args) {
				token = os.Args[i+1]
				i++
			}
		case "--legacy":
			useLegacy = true
		}
	}

	serverURL, token, err := resolveCloudClientConfig(cfg.DataDir, serverURL, token, true)
	if err != nil {
		fatal(err)
	}
	if serverURL == "" || token == "" {
		fatal(fmt.Errorf("cloud config missing server_url or token (run 'mnemo cloud login' first)"))
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	rt, err := remoteTransportNew(serverURL, token)
	if err != nil {
		fatal(err)
	}
	if cc, err := loadCloudConfig(cfg.DataDir); err == nil && cc != nil && cc.ServerURL == serverURL && cc.RefreshToken != "" {
		rt.SetTokenRefresher(cc.RefreshToken, func(newToken string) error {
			cc.Token = newToken
			return saveCloudConfig(cfg.DataDir, cc)
		})
	}

	// Legacy chunk-based sync (deprecated — preserved for backward compatibility).
	if useLegacy {
		handleRemoteSync(s, rt, false, false, true, "")
		return
	}

	// New mutation-safe foreground sync using the autosync engine.
	syncCfg := autosyncDefaultCg()
	syncCfg.PollInterval = 0 // no periodic polling in foreground mode
	mgr := autosyncNew(s, rt, syncCfg)

	fmt.Printf("Syncing with %s...\n", serverURL)

	// Run a single sync cycle in foreground.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Trigger an immediate cycle and wait.
	done := make(chan struct{})
	go func() {
		mgr.Run(ctx)
		close(done)
	}()

	mgr.NotifyDirty()

	// Wait for the first cycle to complete (healthy or failed).
	for {
		status := mgr.Status()
		switch status.Phase {
		case autosync.PhaseHealthy:
			cancel()
			<-done
			fmt.Println("Sync complete.")
			return
		case autosync.PhasePushFailed, autosync.PhasePullFailed, autosync.PhaseBackoff:
			cancel()
			<-done
			if status.LastError != "" {
				fatal(fmt.Errorf("sync failed: %s", status.LastError))
			}
			fatal(fmt.Errorf("sync failed (phase: %s)", status.Phase))
			return
		}
		// Brief sleep to avoid busy-wait.
		select {
		case <-done:
			return
		default:
		}
	}
}

// cmdCloudSyncStatus shows the local sync state from SQLite (mutation journal, degraded state).
func cmdCloudSyncStatus(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	state, err := s.GetSyncState(store.DefaultSyncTargetKey)
	if err != nil {
		fmt.Println("Cloud sync status: not initialized")
		fmt.Println("  Run 'mnemo cloud login' and then 'mnemo cloud sync' to start syncing.")
		return
	}

	fmt.Println("Cloud sync status (local):")
	fmt.Printf("  Lifecycle:           %s\n", state.Lifecycle)
	fmt.Printf("  Last enqueued seq:   %d\n", state.LastEnqueuedSeq)
	fmt.Printf("  Last acked seq:      %d\n", state.LastAckedSeq)
	fmt.Printf("  Last pulled seq:     %d\n", state.LastPulledSeq)

	pending := state.LastEnqueuedSeq - state.LastAckedSeq
	if pending < 0 {
		pending = 0
	}
	fmt.Printf("  Pending mutations:   %d\n", pending)

	if state.ConsecutiveFailures > 0 {
		fmt.Printf("  Consecutive failures: %d\n", state.ConsecutiveFailures)
	}
	if state.LastError != nil && *state.LastError != "" {
		fmt.Printf("  Last error:          %s\n", *state.LastError)
	}
	if state.BackoffUntil != nil && *state.BackoffUntil != "" {
		fmt.Printf("  Backoff until:       %s\n", *state.BackoffUntil)
	}
	if state.LeaseOwner != nil && *state.LeaseOwner != "" {
		fmt.Printf("  Lease owner:         %s\n", *state.LeaseOwner)
	}
}

func cmdCloudStatus(cfg store.Config) {
	serverURL := ""
	token := ""
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--server", "--remote", "-r":
			if i+1 < len(os.Args) {
				serverURL = os.Args[i+1]
				i++
			}
		case "--token", "-t":
			if i+1 < len(os.Args) {
				token = os.Args[i+1]
				i++
			}
		}
	}

	serverURL, token, err := resolveCloudClientConfig(cfg.DataDir, serverURL, token, true)
	if err != nil {
		fatal(err)
	}
	if serverURL == "" || token == "" {
		fatal(fmt.Errorf("cloud config missing server_url or token (run 'mnemo cloud login' first)"))
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	rt, err := remoteTransportNew(serverURL, token)
	if err != nil {
		fatal(err)
	}
	sy := mnemosync.NewWithTransport(s, rt)

	local, remoteCount, pending, err := syncStatus(sy)
	if err != nil {
		fatal(err)
	}

	fmt.Printf("Cloud sync status:\n")
	fmt.Printf("  Server:          %s\n", serverURL)
	fmt.Printf("  Local chunks:    %d\n", local)
	fmt.Printf("  Remote chunks:   %d\n", remoteCount)
	fmt.Printf("  Pending import:  %d\n", pending)
}

func cmdCloudAPIKey(dataDir string) {
	cc, err := loadCloudConfig(dataDir)
	if err != nil {
		fatal(fmt.Errorf("load cloud config: %w (run 'mnemo cloud login' first)", err))
	}
	if cc.ServerURL == "" || cc.Token == "" {
		fatal(fmt.Errorf("cloud config missing server_url or token (run 'mnemo cloud login' first)"))
	}

	client := cloudHTTPClient()
	req, err := http.NewRequest("POST", strings.TrimRight(cc.ServerURL, "/")+"/auth/api-key", nil)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+cc.Token)

	resp, err := client.Do(req)
	if err != nil {
		fatal(fmt.Errorf("api-key: %w", err))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			fatal(fmt.Errorf("api-key failed: %s", errResp.Error))
		}
		fatal(fmt.Errorf("api-key failed: %s", resp.Status))
	}

	var result struct {
		APIKey  string `json:"api_key"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fatal(fmt.Errorf("parse api-key response: %w", err))
	}

	fmt.Printf("API Key: %s\n", result.APIKey)
	fmt.Println("WARNING: Store this key securely. It will not be shown again.")
}

// ─── Enrollment Commands ─────────────────────────────────────────────────────

// cmdCloudEnroll enrolls a project for cloud sync.
func cmdCloudEnroll(cfg store.Config) {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: mnemo cloud enroll <project>")
		exitFunc(1)
		return
	}

	project := os.Args[3]

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	if err := s.EnrollProject(project); err != nil {
		fatal(err)
	}

	fmt.Printf("Project %q enrolled for cloud sync.\n", project)
}

// cmdCloudUnenroll removes a project from cloud sync enrollment.
func cmdCloudUnenroll(cfg store.Config) {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: mnemo cloud unenroll <project>")
		exitFunc(1)
		return
	}

	project := os.Args[3]

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	if err := s.UnenrollProject(project); err != nil {
		fatal(err)
	}

	fmt.Printf("Project %q unenrolled from cloud sync.\n", project)
}

// cmdCloudProjects lists all projects currently enrolled for cloud sync.
func cmdCloudProjects(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	projects, err := s.ListEnrolledProjects()
	if err != nil {
		fatal(err)
	}

	if len(projects) == 0 {
		fmt.Println("No projects enrolled for cloud sync.")
		fmt.Println("  Use 'mnemo cloud enroll <project>' to enroll a project.")
		return
	}

	fmt.Printf("Enrolled projects (%d):\n", len(projects))
	for _, p := range projects {
		fmt.Printf("  %s  (enrolled: %s)\n", p.Project, p.EnrolledAt)
	}
}

// ─── Remote Search/Context Helpers ───────────────────────────────────────────

func remoteSearch(serverURL, token, query string, opts store.SearchOptions) {
	u := strings.TrimRight(serverURL, "/") + "/sync/search?q=" + query
	if opts.Type != "" {
		u += "&type=" + opts.Type
	}
	if opts.Project != "" {
		u += "&project=" + opts.Project
	}
	if opts.Scope != "" {
		u += "&scope=" + opts.Scope
	}
	if opts.Limit > 0 {
		u += "&limit=" + strconv.Itoa(opts.Limit)
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := cloudHTTPClient().Do(req)
	if err != nil {
		fatal(fmt.Errorf("remote search: %w", err))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			fatal(fmt.Errorf("remote search: %s", errResp.Error))
		}
		fatal(fmt.Errorf("remote search: %s", resp.Status))
	}

	var searchResp struct {
		Results []struct {
			ID        int64   `json:"id"`
			Type      string  `json:"type"`
			Title     string  `json:"title"`
			Content   string  `json:"content"`
			Project   *string `json:"project"`
			Scope     string  `json:"scope"`
			Rank      float64 `json:"rank"`
			CreatedAt string  `json:"created_at"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &searchResp); err != nil {
		fatal(fmt.Errorf("parse remote search: %w", err))
	}

	if len(searchResp.Results) == 0 {
		fmt.Printf("No memories found for: %q\n", query)
		return
	}

	fmt.Printf("Found %d memories (cloud):\n\n", len(searchResp.Results))
	for i, r := range searchResp.Results {
		project := ""
		if r.Project != nil {
			project = fmt.Sprintf(" | project: %s", *r.Project)
		}
		fmt.Printf("[%d] #%d (%s) — %s\n    %s\n    %s%s | scope: %s\n\n",
			i+1, r.ID, r.Type, r.Title,
			truncate(r.Content, 300),
			r.CreatedAt, project, r.Scope)
	}
}

func remoteContext(serverURL, token, project, scope string) {
	u := strings.TrimRight(serverURL, "/") + "/sync/context"
	params := []string{}
	if project != "" {
		params = append(params, "project="+project)
	}
	if scope != "" {
		params = append(params, "scope="+scope)
	}
	if len(params) > 0 {
		u += "?" + strings.Join(params, "&")
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := cloudHTTPClient().Do(req)
	if err != nil {
		fatal(fmt.Errorf("remote context: %w", err))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			fatal(fmt.Errorf("remote context: %s", errResp.Error))
		}
		fatal(fmt.Errorf("remote context: %s", resp.Status))
	}

	var ctxResp struct {
		Context string `json:"context"`
	}
	if err := json.Unmarshal(body, &ctxResp); err != nil {
		fatal(fmt.Errorf("parse remote context: %w", err))
	}

	if ctxResp.Context == "" {
		fmt.Println("No previous session memories found.")
		return
	}

	fmt.Print(ctxResp.Context)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func printUsage() {
	fmt.Printf(`mnemo v%s — Persistent memory for AI coding agents

Usage:
  mnemo <command> [arguments]

Commands:
  serve [port]       Start HTTP API server (default: 7437)
  mcp [--tools=PROFILE] Start MCP server (stdio transport, for any AI agent)
                       Profiles: agent (11 tools), admin (3 tools), all (default, 14)
                       Combine: --tools=agent,admin or pick individual tools
                       Example: mnemo mcp --tools=agent
  tui                Launch interactive terminal UI
  search <query>     Search memories [--type TYPE] [--project PROJECT] [--scope SCOPE] [--limit N]
                       [--remote URL] [--token TOKEN]  Query cloud server instead of local DB
  save <title> <msg> Save a memory  [--type TYPE] [--project PROJECT] [--scope SCOPE]
  timeline <obs_id>  Show chronological context around an observation [--before N] [--after N]
  context [project]  Show recent context from previous sessions
                       [--remote URL] [--token TOKEN]  Query cloud server instead of local DB
  stats              Show memory system statistics
  export [file]      Export all memories to JSON (default: mnemo-export.json)
  import <file>      Import memories from a JSON export file
  setup [agent]      Install/setup agent integration (opencode, claude-code, gemini-cli, codex)
  sync               Export new memories as compressed chunk to .mnemo/
                       --import   Import new chunks from .mnemo/ into local DB
                       --status   Show sync status (local vs remote chunks)
                       --project  Filter export to a specific project
                       --all      Export ALL projects (ignore directory-based filter)

  cloud serve        Start cloud server (Postgres backend)
                       --port PORT          HTTP port (default: 8080)
                       --database-url URL   Postgres DSN (or MNEMO_DATABASE_URL env)
  cloud register     Register a new cloud account
                       --server URL         Cloud server URL (required)
  cloud login        Login to an existing cloud account
                       --server URL         Cloud server URL (required)
  cloud sync         Sync local mutations to cloud (push + pull)
                       --legacy   Use legacy chunk-based sync (deprecated)
  cloud sync-status  Show local sync journal state (pending mutations, degraded state)
  cloud status       Show cloud sync status (local vs remote chunks, legacy)
  cloud api-key      Generate a new API key for the cloud server
  cloud enroll <p>   Enroll a project for cloud sync (only enrolled projects are pushed)
  cloud unenroll <p> Unenroll a project from cloud sync
  cloud projects     List projects currently enrolled for cloud sync

  version            Print version
  help               Show this help

Environment:
  MNEMO_DATA_DIR    Override data directory (default: ~/.mnemo)
  MNEMO_PORT        Override HTTP server port (default: 7437)
  MNEMO_REMOTE_URL  Cloud server URL (used by --remote flag)
  MNEMO_TOKEN       Cloud auth token (used by --token flag)
  MNEMO_DATABASE_URL  Postgres DSN for cloud serve
  MNEMO_JWT_SECRET    JWT signing secret for cloud serve (>= 32 chars)

MCP Configuration (add to your agent's config):
  {
    "mcp": {
      "mnemo": {
        "type": "stdio",
        "command": "mnemo",
        "args": ["mcp", "--tools=agent"]
      }
    }
  }
`, version)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "mnemo: %s\n", err)
	exitFunc(1)
}

// resolveHomeFallback tries platform-specific environment variables to find
// a home directory when os.UserHomeDir() fails. This commonly happens on
// Windows when mnemo is launched as an MCP subprocess without full env
// propagation.
func resolveHomeFallback() string {
	// Windows: try common env vars that might be set even when
	// %USERPROFILE% is missing.
	for _, env := range []string{"USERPROFILE", "HOME", "LOCALAPPDATA"} {
		if v := os.Getenv(env); v != "" {
			if env == "LOCALAPPDATA" {
				// LOCALAPPDATA is C:\Users\<user>\AppData\Local — go up two levels.
				parent := filepath.Dir(filepath.Dir(v))
				if parent != "." && parent != v {
					return parent
				}
			}
			return v
		}
	}

	// Unix: $HOME should always work, but try passwd-style fallback.
	if v := os.Getenv("HOME"); v != "" {
		return v
	}

	return ""
}

// migrateOrphanedDB checks for mnemo databases that ended up in wrong
// locations (e.g. drive root on Windows when UserHomeDir failed silently)
// and moves them to the correct location if the correct location has no DB.
func migrateOrphanedDB(correctDir string) {
	correctDB := filepath.Join(correctDir, "mnemo.db")

	// If the correct DB already exists, nothing to migrate.
	if _, err := os.Stat(correctDB); err == nil {
		return
	}

	// Known wrong locations: relative ".mnemo" resolved from common roots.
	// On Windows this typically ends up at C:\.mnemo or D:\.mnemo.
	candidates := []string{
		filepath.Join(string(filepath.Separator), ".mnemo", "mnemo.db"),
	}

	// On Windows, check all drive letter roots.
	if filepath.Separator == '\\' {
		for _, drive := range "CDEFGHIJ" {
			candidates = append(candidates,
				filepath.Join(string(drive)+":\\", ".mnemo", "mnemo.db"),
			)
		}
	}

	for _, candidate := range candidates {
		if candidate == correctDB {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}

		// Found an orphaned DB — migrate it.
		log.Printf("[mnemo] found orphaned database at %s, migrating to %s", candidate, correctDB)

		if err := os.MkdirAll(correctDir, 0755); err != nil {
			log.Printf("[mnemo] migration failed (create dir): %v", err)
			return
		}

		// Move DB and WAL/SHM files if they exist.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			src := candidate + suffix
			dst := correctDB + suffix
			if _, statErr := os.Stat(src); statErr != nil {
				continue
			}
			if renameErr := os.Rename(src, dst); renameErr != nil {
				log.Printf("[mnemo] migration failed (move %s): %v", filepath.Base(src), renameErr)
				return
			}
		}

		// Clean up empty orphaned directory.
		orphanDir := filepath.Dir(candidate)
		entries, _ := os.ReadDir(orphanDir)
		if len(entries) == 0 {
			os.Remove(orphanDir)
		}

		log.Printf("[mnemo] migration complete — memories recovered")
		return
	}
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
