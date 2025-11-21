package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/abenz1267/elephant/v2/pkg/common"
	"github.com/abenz1267/elephant/v2/pkg/common/history"
	"github.com/abenz1267/elephant/v2/pkg/pb/pb"
	_ "github.com/mattn/go-sqlite3"
)

var (
	Name       = "vscode"
	NamePretty = "VSCode Projects"
	config     *Config
	h          = history.Load(Name)
	entries    []Entry
)

//go:embed README.md
var readme string

type Config struct {
	common.Config    `koanf:",squash"`
	DBPath           string `koanf:"db_path" desc:"path to VSCode state database" default:"~/.config/Code/User/globalStorage/state.vscdb"`
	CodeCommand      string `koanf:"code_command" desc:"command to launch VSCode" default:"code"`
	MaxEntries       int    `koanf:"max_entries" desc:"maximum number of recent entries to show" default:"50"`
	History          bool   `koanf:"history" desc:"make use of history for sorting" default:"true"`
	HistoryWhenEmpty bool   `koanf:"history_when_empty" desc:"consider history when query is empty" default:"false"`
}

type Entry struct {
	Type      string // "folder" or "workspace"
	Path      string
	Label     string
	IsRemote  bool
	Authority string
}

type RecentlyOpened struct {
	Entries []struct {
		FolderURI       string `json:"folderUri"`
		Label           string `json:"label"`
		RemoteAuthority string `json:"remoteAuthority"`
		Workspace       *struct {
			ID         string `json:"id"`
			ConfigPath string `json:"configPath"`
		} `json:"workspace"`
	} `json:"entries"`
}

const (
	ActionOpen          = "open"
	ActionDeleteHistory = "delete_history"
)

func Setup() {
	start := time.Now()

	config = &Config{
		Config: common.Config{
			Icon:     "vscode",
			MinScore: 20,
		},
		DBPath:           "~/.config/Code/User/globalStorage/state.vscdb",
		CodeCommand:      "code",
		MaxEntries:       50,
		History:          true,
		HistoryWhenEmpty: false,
	}

	common.LoadConfig(Name, config)

	if config.NamePretty != "" {
		NamePretty = config.NamePretty
	}

	// Expand home directory
	if strings.HasPrefix(config.DBPath, "~/") {
		home, _ := os.UserHomeDir()
		config.DBPath = filepath.Join(home, config.DBPath[2:])
	}

	// Load entries
	if err := loadEntries(); err != nil {
		slog.Error(Name, "setup", err)
		return
	}

	slog.Info(Name, "loaded", len(entries), "duration", time.Since(start))
}

func loadEntries() error {
	if !common.FileExists(config.DBPath) {
		return fmt.Errorf("VSCode state database not found at %s", config.DBPath)
	}

	db, err := sql.Open("sqlite3", config.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	var jsonData string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'history.recentlyOpenedPathsList'").Scan(&jsonData)
	if err != nil {
		return fmt.Errorf("failed to query recent paths: %w", err)
	}

	var recent RecentlyOpened
	if err := json.Unmarshal([]byte(jsonData), &recent); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	entries = make([]Entry, 0, len(recent.Entries))
	seen := make(map[string]bool)

	for _, e := range recent.Entries {
		if len(entries) >= config.MaxEntries {
			break
		}

		var entry Entry

		// Handle workspace
		if e.Workspace != nil {
			uri := e.Workspace.ConfigPath
			path, label, isRemote, authority := parseURI(uri)
			if path == "" {
				continue
			}

			// Skip duplicates
			identifier := fmt.Sprintf("workspace:%s", uri)
			if seen[identifier] {
				continue
			}
			seen[identifier] = true

			entry = Entry{
				Type:      "workspace",
				Path:      path,
				Label:     label,
				IsRemote:  isRemote,
				Authority: authority,
			}
		} else if e.FolderURI != "" {
			// Handle folder
			path, label, isRemote, authority := parseURI(e.FolderURI)
			if path == "" {
				continue
			}

			// Use custom label if provided
			if e.Label != "" {
				label = e.Label
			}

			// Skip duplicates
			identifier := fmt.Sprintf("folder:%s", e.FolderURI)
			if seen[identifier] {
				continue
			}
			seen[identifier] = true

			entry = Entry{
				Type:      "folder",
				Path:      path,
				Label:     label,
				IsRemote:  isRemote,
				Authority: authority,
			}

			// Check if remote authority is provided
			if e.RemoteAuthority != "" {
				entry.IsRemote = true
				entry.Authority = e.RemoteAuthority
			}
		} else {
			continue
		}

		entries = append(entries, entry)
	}

	return nil
}

func parseURI(uri string) (path, label string, isRemote bool, authority string) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", false, ""
	}

	switch u.Scheme {
	case "file":
		path = u.Path
		label = filepath.Base(path)
		return path, label, false, ""
	case "vscode-remote":
		// Format: vscode-remote://tunnel%2Bpatricks-oma/home/patroza/.config
		authority = u.Host
		path = u.Path
		label = filepath.Base(path)
		return path, label, true, authority
	default:
		return "", "", false, ""
	}
}

func Activate(single bool, identifier, action string, query string, args string, format uint8, conn net.Conn) {
	switch action {
	case history.ActionDelete:
		h.Remove(identifier)
		return
	case ActionOpen:
		// Parse identifier
		parts := strings.SplitN(identifier, ":", 2)
		if len(parts) != 2 {
			slog.Error(Name, "invalid identifier", identifier)
			return
		}

		entryType := parts[0]
		entryURI := parts[1]

		// Find the entry
		var entry *Entry
		for i := range entries {
			expectedID := fmt.Sprintf("%s:%s", entries[i].Type, getURIForEntry(&entries[i]))
			if expectedID == identifier {
				entry = &entries[i]
				break
			}
		}

		if entry == nil {
			slog.Error(Name, "entry not found", identifier)
			return
		}

		var cmd *exec.Cmd
		if entry.IsRemote {
			// For remote entries, use the original URI format
			cmd = exec.Command(config.CodeCommand, "--remote", entryURI)
		} else if entryType == "workspace" {
			cmd = exec.Command(config.CodeCommand, entry.Path)
		} else {
			cmd = exec.Command(config.CodeCommand, entry.Path)
		}

		cmd.Env = os.Environ()
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true,
		}

		if err := cmd.Start(); err != nil {
			slog.Error(Name, "failed to start", err)
			return
		}

		go func() {
			cmd.Wait()
		}()

		if config.History {
			h.Save(query, identifier)
		}

		slog.Info(Name, "identifier", identifier)
	}
}

func Query(conn net.Conn, query string, _ bool, exact bool, _ uint8) []*pb.QueryResponse_Item {
	startTime := time.Now()
	items := make([]*pb.QueryResponse_Item, 0, len(entries))

	for i := range entries {
		entry := &entries[i]

		identifier := fmt.Sprintf("%s:%s", entry.Type, getURIForEntry(entry))

		text := entry.Label
		subtext := entry.Path

		// Add remote indicator
		if entry.IsRemote {
			subtext = fmt.Sprintf("[%s] %s", entry.Authority, entry.Path)
		}

		// Add workspace indicator
		if entry.Type == "workspace" {
			text = fmt.Sprintf("%s (workspace)", text)
		}

		var score int32
		var positions []int32
		var fuzzyStart int32

		if query != "" {
			// Search in both label and path
			searchText := fmt.Sprintf("%s %s", entry.Label, entry.Path)
			score, positions, fuzzyStart = common.FuzzyScore(query, searchText, exact)

			if score < config.MinScore {
				continue
			}
		}

		var usageScore int32
		if config.History && score > config.MinScore || (query == "" && config.HistoryWhenEmpty) {
			usageScore = h.CalcUsageScore(query, identifier)
			score = score + usageScore
		}

		icon := config.Icon
		if entry.Type == "workspace" {
			icon = "folder-workspace"
		}

		items = append(items, &pb.QueryResponse_Item{
			Identifier: identifier,
			Text:       text,
			Subtext:    subtext,
			Icon:       icon,
			Type:       pb.QueryResponse_REGULAR,
			Score:      score,
			Provider:   Name,
			Actions:    []string{ActionOpen},
			Fuzzyinfo: &pb.QueryResponse_Item_FuzzyInfo{
				Positions: positions,
				Start:     fuzzyStart,
			},
		})
	}

	slog.Info(Name, "query", query, "results", len(items), "duration", time.Since(startTime))

	return items
}

func getURIForEntry(entry *Entry) string {
	if entry.IsRemote {
		// Reconstruct the vscode-remote URI
		// Note: We need to URL encode the authority
		return fmt.Sprintf("vscode-remote://%s%s", url.PathEscape(entry.Authority), entry.Path)
	}
	return fmt.Sprintf("file://%s", entry.Path)
}

func PrintDoc() {
	fmt.Println(readme)
}

// Icon returns the configured icon for the provider.
func Icon() string {
	if config != nil {
		return config.Icon
	}
	return "vscode"
}

// HideFromProviderlist indicates if the provider should be hidden from the providerlist.
func HideFromProviderlist() bool {
	if config != nil {
		return config.HideFromProviderlist
	}
	return false
}

// State returns the provider state and available provider-level actions.
// VSCode recent projects provider currently has no special states or actions.
func State(provider string) *pb.ProviderStateResponse {
	return &pb.ProviderStateResponse{
		States:  []string{},
		Actions: []string{},
	}
}

// Available checks if the provider can operate (state DB exists and code command is present).
func Available() bool {
	// Ensure the DB path is expanded similarly to Setup (in case Available is called before Setup).
	dbPath := "~/.config/Code/User/globalStorage/state.vscdb"
	if config != nil && config.DBPath != "" {
		dbPath = config.DBPath
	}
	if strings.HasPrefix(dbPath, "~/") {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, dbPath[2:])
	}

	// Check code command
	if cmdPath, err := exec.LookPath("code"); cmdPath == "" || err != nil {
		slog.Info(Name, "available", "code command not found. disabling.")
		return false
	}

	// Check DB file exists
	if !common.FileExists(dbPath) {
		slog.Info(Name, "available", "VSCode state DB not found", "path", dbPath)
		return false
	}

	return true
}
