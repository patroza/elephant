// Package vscodeprojects lists recent VS Code projects.package vscodeprojects

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "embed"

	_ "github.com/mattn/go-sqlite3"

	"github.com/abenz1267/elephant/v2/internal/util"
	"github.com/abenz1267/elephant/v2/pkg/common"
	"github.com/abenz1267/elephant/v2/pkg/pb/pb"
)

var (
	Name       = "vscodeprojects"
	NamePretty = "VSCode Projects"
)

//go:embed README.md
var readme string

// Config represents provider configuration
// command: VS Code executable (code, code-insiders)
// db_path: override for state.vscdb
// max_entries: cap number of entries
// min_score: minimum fuzzy score
// icon: icon name
// LaunchPrefix inherited from common.Config
// HideFromProviderlist inherited
// NamePretty inherited
// History disabled (not used here)
type Config struct {
	common.Config `koanf:",squash"`
	Command       string `koanf:"command" desc:"command to launch VS Code" default:"code"`
	DBPath        string `koanf:"db_path" desc:"override path to state.vscdb" default:""`
	MaxEntries    int    `koanf:"max_entries" desc:"maximum number of entries to show" default:"100"`
}

var config *Config

// projectEntry holds one recent project
// Path is absolute path to folder/workspace
// Label is optional label VS Code stored
// Kind is folder|workspace|file
// Raw is raw JSON object for future use
// Last is not yet populated (future)
type projectEntry struct {
	Path  string
	Label string
	Kind  string
	Raw   map[string]any
}

var (
	entries     []projectEntry
	entriesMu   sync.RWMutex
	loadedAt    time.Time
	lastModTime time.Time
	loadOnce    sync.Once
)

func Setup() {
	start := time.Now()
	config = &Config{
		Config: common.Config{
			Icon:     "visual-studio-code",
			MinScore: 20,
		},
		Command:    "code",
		MaxEntries: 100,
	}
	common.LoadConfig(Name, config)
	if config.NamePretty != "" {
		NamePretty = config.NamePretty
	}
	refreshIfNeeded(true)
	slog.Info(Name, "loaded", time.Since(start))
}

func Available() bool {
	path := dbPath()
	return common.FileExists(path)
}

func PrintDoc() {
	fmt.Println(readme)
	fmt.Println()
	util.PrintConfig(Config{}, Name)
}

func State(provider string) *pb.ProviderStateResponse {
	return &pb.ProviderStateResponse{}
}

func HideFromProviderlist() bool {
	return config.HideFromProviderlist
}

func Icon() string { return config.Icon }

const (
	ActionOpen   = "open"
	ActionReveal = "reveal"
)

func Activate(single bool, identifier, action, query, args string, format uint8, conn net.Conn) {
	entriesMu.RLock()
	idx := parseIndex(identifier)
	if idx < 0 || idx >= len(entries) {
		entriesMu.RUnlock()
		slog.Error(Name, "activate", "invalid identifier", "id", identifier)
		return
	}
	pe := entries[idx]
	entriesMu.RUnlock()

	if action == "" {
		action = ActionOpen
	}
	slog.Info(Name, "activate", "opening entry", "id", identifier, "action", action)

	var cmdStr string
	switch action {
	case ActionOpen:
		// open in VS Code
		cmdStr = strings.TrimSpace(fmt.Sprintf("%s %s '%s'", common.LaunchPrefix(""), config.Command, pe.Path))
	case ActionReveal:
		cmdStr = strings.TrimSpace(fmt.Sprintf("%s xdg-open '%s'", common.LaunchPrefix(""), filepath.Dir(pe.Path)))
	default:
		slog.Error(Name, "activate", fmt.Sprintf("unknown action: %s", action))
		return
	}

	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	slog.Info(Name, "activate", "executing command", "cmd", cmdStr)
	err := cmd.Start()
	if err != nil {
		slog.Error(Name, "activate", err)
		return
	}
	go func() { _ = cmd.Wait() }()
}

func parseIndex(id string) int {
	var i int
	_, err := fmt.Sscanf(id, "%d", &i)
	if err != nil {
		return -1
	}
	return i
}

// Query returns recent projects filtered by fuzzy query on label or path.
func Query(conn net.Conn, query string, single bool, exact bool, format uint8) []*pb.QueryResponse_Item {
	refreshIfNeeded(false)

	entriesMu.RLock()
	defer entriesMu.RUnlock()

	res := []*pb.QueryResponse_Item{}
	for i, e := range entries {
		text := e.Label
		if text == "" {
			text = filepath.Base(e.Path)
		}
		sub := e.Path

		item := &pb.QueryResponse_Item{
			Identifier: fmt.Sprintf("%d", i),
			Text:       text,
			Subtext:    sub,
			Provider:   Name,
			Icon:       config.Icon,
			Actions:    []string{ActionOpen, ActionReveal},
			Score:      int32(1000000000 - i), // Higher score for more recent items
		}

		if query != "" {
			best, score, pos, start, ok := fuzzyBest(query, []string{text, sub}, exact)
			if ok {
				field := "text"
				if best != text {
					field = "subtext"
				}
				item.Score = score
				item.Fuzzyinfo = &pb.QueryResponse_Item_FuzzyInfo{Start: start, Field: field, Positions: pos}
			}
		}

		if query == "" || item.Score > config.MinScore {
			res = append(res, item)
			if len(res) >= config.MaxEntries {
				break
			}
		}
	}
	return res
}

func fuzzyBest(q string, vals []string, exact bool) (string, int32, []int32, int32, bool) {
	var best string
	var scoreRes int32
	var posRes []int32
	var startRes int32
	for _, v := range vals {
		score, pos, start := common.FuzzyScore(q, v, exact)
		if score > scoreRes {
			best = v
			scoreRes = score
			posRes = pos
			startRes = start
		}
	}
	if scoreRes == 0 {
		return "", 0, nil, 0, false
	}
	return best, max(scoreRes-startRes, 10), posRes, startRes, true
}

func dbPath() string {
	if config != nil && config.DBPath != "" {
		return config.DBPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "Code", "User", "globalStorage", "state.vscdb")
}

// refreshIfNeeded loads or reloads entries based on file mod time.
func refreshIfNeeded(force bool) {
	loadOnce.Do(func() { force = true }) // first call ensures load
	path := dbPath()
	if path == "" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	mt := fi.ModTime()
	if !force && !mt.After(lastModTime) && time.Since(loadedAt) < 30*time.Second {
		return // reuse cached within 30s unless file changed
	}
	loadDB(path)
}

func loadDB(path string) {
	entriesMu.Lock()
	defer entriesMu.Unlock()

	lastModTime = time.Now()
	loadedAt = time.Now()
	entries = []projectEntry{}

	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", path))
	if err != nil {
		slog.Error(Name, "opendb", err)
		return
	}
	defer db.Close()

	rows, err := db.Query("SELECT key, value FROM ItemTable WHERE key = 'history.recentlyOpenedPathsList'")
	if err != nil {
		slog.Error(Name, "query", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var valBytes []byte
		if err := rows.Scan(&key, &valBytes); err != nil {
			continue
		}
		if !strings.Contains(strings.ToLower(key), "recent") && key != "history.recentlyOpenedPathsList" {
			continue
		}

		// Attempt to parse JSON
		var obj map[string]any
		if err := json.Unmarshal(valBytes, &obj); err != nil {
			continue
		}
		// Expect array under entries or recentlyOpened list variants
		for _, arrKey := range []string{"entries", "recentlyOpened", "workspaces", "files"} {
			if rawArr, ok := obj[arrKey]; ok {
				switch vv := rawArr.(type) {
				case []any:
					for _, item := range vv {
						parseEntry(item)
					}
				}
			}
		}
	}
}

func parseEntry(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	label := str(m["label"])
	folder := str(m["folderUri"])
	file := str(m["fileUri"])
	
	// Handle workspace which can be a map with configPath
	var workspace string
	if wsVal := m["workspace"]; wsVal != nil {
		if wsMap, ok := wsVal.(map[string]any); ok {
			workspace = str(wsMap["configPath"])
		} else {
			workspace = str(wsVal)
		}
	}
	
	path := ""
	kind := ""

	for _, cand := range []string{folder, file, workspace} {
		if cand != "" {
			path = uriToPath(cand)
			break
		}
	}
	if path == "" {
		return
	}
	if folder != "" {
		kind = "folder"
	} else if file != "" {
		kind = "file"
	} else if workspace != "" {
		kind = "workspace"
	}

	slog.Info(Name, "loadDB", "found entry", "path", path, "label", label, "kind", kind)

	entries = append(entries, projectEntry{Path: path, Label: label, Kind: kind, Raw: m})
}

func str(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
}

func uriToPath(u string) string {
	if strings.HasPrefix(u, "file://") {
		return strings.TrimPrefix(u, "file://")
	}
	return u
}

// max helper (Go 1.21 has slices.Max etc.)
func max(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
