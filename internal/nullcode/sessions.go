package nullcode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nullabby/nullcode/internal/upstream"
)

// sessionsRoot, when set, overrides the sessions directory (tests).
var sessionsRoot string

func sessionsDir() (string, error) {
	if sessionsRoot != "" {
		return sessionsRoot, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nullcode", "sessions"), nil
}

func newSessionID() string {
	return fmt.Sprintf("%s-%06x", time.Now().Format("20060102-150405"), time.Now().UnixNano()%0xffffff)
}

type storedBlock struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	Text  string `json:"text,omitempty"`
	Meta  string `json:"meta,omitempty"`
}

type storedSession struct {
	ID       string             `json:"id"`
	Topic    string             `json:"topic,omitempty"`
	Started  time.Time          `json:"started"`
	Updated  time.Time          `json:"updated"`
	Model    string             `json:"model,omitempty"`
	Messages []upstream.Message `json:"messages,omitempty"`
	Blocks   []storedBlock      `json:"blocks,omitempty"`
}

// sessionTopic derives a short overview line from the first user message.
func sessionTopic(msgs []upstream.Message) string {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(m.Content), "\n", 2)[0])
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > 60 {
			return string(r[:60]) + "…"
		}
		return line
	}
	return ""
}

func kindName(k blockKind) string {
	switch k {
	case bUser:
		return "user"
	case bAssistant:
		return "assistant"
	case bReasoning:
		return "reasoning"
	case bTool:
		return "tool"
	case bResult:
		return "result"
	case bError:
		return "error"
	default:
		return "notice"
	}
}

func kindFromName(s string) blockKind {
	switch s {
	case "user":
		return bUser
	case "assistant":
		return bAssistant
	case "reasoning":
		return bReasoning
	case "tool":
		return bTool
	case "result":
		return bResult
	case "error":
		return bError
	default:
		return bNotice
	}
}

func (a *App) saveSession() error {
	if a.agent == nil {
		return nil
	}
	dir, err := sessionsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	msgs := a.agent.Snapshot()
	s := storedSession{
		ID:      a.sessionID,
		Topic:   sessionTopic(msgs),
		Started: a.sessStart,
		Updated: time.Now(),
		Model:   a.model,
	}
	for _, m := range msgs {
		s.Messages = append(s.Messages, m)
	}
	for _, b := range a.blocks {
		s.Blocks = append(s.Blocks, storedBlock{Kind: kindName(b.kind), Label: b.label, Text: b.text, Meta: b.meta})
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, s.ID+".json"), data, 0o600)
}

func loadSessionFile(path string) (*storedSession, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s storedSession
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

type sessionInfo struct {
	Path    string
	ID      string
	Topic   string
	Started time.Time
	Model   string
	Msgs    int
	Preview string
}

// listSessions returns saved sessions, most recent first (max 10).
func listSessions() ([]sessionInfo, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type fm struct {
		info sessionInfo
		mod  time.Time
	}
	var all []fm
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		s, err := loadSessionFile(path)
		if err != nil {
			continue
		}
		info := sessionInfo{Path: path, ID: s.ID, Topic: s.Topic, Started: s.Started, Model: s.Model, Msgs: len(s.Messages)}
		for _, b := range s.Blocks {
			if b.Kind == "user" && strings.TrimSpace(b.Text) != "" {
				info.Preview = strings.TrimSpace(b.Text)
				break
			}
		}
		if info.Topic == "" {
			info.Topic = sessionTopic(s.Messages)
		}
		mod := s.Updated
		if mod.IsZero() {
			if fi, err := e.Info(); err == nil {
				mod = fi.ModTime()
			}
		}
		all = append(all, fm{info: info, mod: mod})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	if len(all) > 10 {
		all = all[:10]
	}
	out := make([]sessionInfo, 0, len(all))
	for _, f := range all {
		out = append(out, f.info)
	}
	return out, nil
}
