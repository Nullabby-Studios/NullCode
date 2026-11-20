package nullcode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nullabby/nullcode/internal/upstream"
)

func TestNormalizeFeatures(t *testing.T) {
	m := normalizeFeatures(nil)
	if !m["exec"] || !m["read"] || !m["write"] || !m["patch"] || !m["done"] {
		t.Fatalf("defaults = %+v", m)
	}
	if m["todos"] || m["mcps"] {
		t.Fatal("extras should be off by default")
	}
	m = normalizeFeatures([]string{"todos", "mcps"})
	if !m["todos"] || !m["mcps"] || !m["done"] {
		t.Fatalf("list = %+v", m)
	}
	if m["exec"] {
		t.Fatal("core should be off when a list is given")
	}
}

func TestRunActionDisabled(t *testing.T) {
	a := NewAgent(Config{Root: ".", Features: []string{"read"}}, nil)
	got := a.runAction(context.Background(), Action{Kind: "exec", Arg: "ls"})
	if got.Status != "error" || !strings.Contains(got.Body, "disabled") {
		t.Fatalf("got = %+v", got)
	}
	ok := a.runAction(context.Background(), Action{Kind: "read", Arg: "go.mod"})
	if ok.Status == "error" && strings.Contains(ok.Body, "disabled") {
		t.Fatalf("read should be enabled: %+v", ok)
	}
}

func TestTodoTool(t *testing.T) {
	dir := t.TempDir()
	a := NewAgent(Config{Root: dir, Features: []string{"exec", "read", "write", "patch", "todos"}}, nil)
	got := a.todo("add fix the login bug")
	if got.Status != "ok" {
		t.Fatalf("add = %+v", got)
	}
	got = a.todo("add write tests")
	if got.Status != "ok" {
		t.Fatalf("add2 = %+v", got)
	}
	got = a.todo("list")
	if !strings.Contains(got.Body, "fix the login bug") || !strings.Contains(got.Body, "write tests") {
		t.Fatalf("list = %+v", got)
	}
	got = a.todo("done 2")
	if got.Status != "ok" {
		t.Fatalf("done = %+v", got)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "TODO.md"))
	if !strings.Contains(string(data), "- [x] write tests") || !strings.Contains(string(data), "- [ ] fix the login bug") {
		t.Fatalf("TODO.md = %q", string(data))
	}
	got = a.todo("undo 2")
	if got.Status != "ok" {
		t.Fatalf("undo = %+v", got)
	}
	got = a.todo("done 2")
	if got.Status != "ok" {
		t.Fatalf("done again = %+v", got)
	}
	got = a.todo("clear")
	if got.Status != "ok" || strings.Contains(got.Body, "write tests") {
		t.Fatalf("clear = %+v", got)
	}
	got = a.todo("list")
	if strings.Contains(got.Body, "write tests") || !strings.Contains(got.Body, "fix the login bug") {
		t.Fatalf("list after clear = %+v", got)
	}
}

func TestSkillTool(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".nullcode", "skills")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "review.md"), []byte("check error handling"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewAgent(Config{Root: dir, Features: []string{"read", "skills"}}, nil)
	got := a.skill("")
	if !strings.Contains(got.Body, "review") {
		t.Fatalf("list = %+v", got)
	}
	got = a.skill("review")
	if got.Status != "ok" || !strings.Contains(got.Body, "check error handling") {
		t.Fatalf("load = %+v", got)
	}
	got = a.skill("missing")
	if got.Status != "error" {
		t.Fatalf("missing = %+v", got)
	}
}

func TestListSearchTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := NewAgent(Config{Root: dir, Features: []string{"read", "list", "search"}}, nil)
	got := a.list("")
	if got.Status != "ok" || !strings.Contains(got.Body, "main.go") || !strings.Contains(got.Body, "internal/") {
		t.Fatalf("list = %+v", got)
	}
	got = a.search(`"func main"`)
	if got.Status != "ok" || !strings.Contains(got.Body, "main.go") {
		t.Fatalf("search = %+v", got)
	}
	got = a.search("zzz-not-there")
	if got.Status != "ok" || !strings.Contains(got.Body, "no matches") {
		t.Fatalf("search none = %+v", got)
	}
}

func TestAgentAskFlow(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		parts := []string{"Before.\n", glyphOpen + "ask" + glyphClose + " which database?\n", glyphOpen + "done" + glyphClose + "\n"}
		for _, p := range parts {
			b, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]string{"content": p}}},
			})
			fmt.Fprintf(w, "data: %s\n\n", b)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var asked string
	a := NewAgent(Config{
		BaseURL: srv.URL, Model: "test", MaxSteps: 4, AutoApprove: false,
		Root: dir, MaxTokens: 128, Think: true, Features: []string{"exec", "read", "write", "patch", "ask"},
	}, func(e Event) {
		if e.Kind == "ask" && e.Ask != nil {
			asked = e.Ask.Question
			e.Ask.Resp <- "postgres"
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.Run(ctx, "pick a db")

	if asked != "which database?" {
		t.Fatalf("asked = %q", asked)
	}
	var sawAnswer bool
	for _, m := range a.Snapshot() {
		if strings.Contains(m.Content, "the user answered: postgres") {
			sawAnswer = true
		}
	}
	if !sawAnswer {
		t.Fatal("answer missing from history")
	}
}

func TestSessionsRoundtrip(t *testing.T) {
	old := sessionsRoot
	sessionsRoot = filepath.Join(t.TempDir(), "sessions")
	defer func() { sessionsRoot = old }()

	dir := t.TempDir()
	a := &App{
		width: 60, height: 20, model: "m1", showReasoning: true, quiet: true,
		features:  normalizeFeatures(defaultFeatures()),
		sessionID: newSessionID(),
		sessStart: time.Now(),
	}
	a.cfg.Root = dir
	a.agent = NewAgent(Config{Root: dir, Model: "m1", Features: []string{"read"}}, func(Event) {})
	a.agent.Restore([]upstream.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	})
	a.blocks = []*Block{
		{kind: bUser, label: "you", text: "hello"},
		{kind: bResult, label: "result", text: "ok", meta: "exit=0"},
	}
	if err := a.saveSession(); err != nil {
		t.Fatalf("save: %v", err)
	}
	sessions, err := listSessions()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list = %+v err=%v", sessions, err)
	}
	if sessions[0].Preview != "hello" || sessions[0].Model != "m1" {
		t.Fatalf("info = %+v", sessions[0])
	}
	st, err := loadSessionFile(sessions[0].Path)
	if err != nil || len(st.Messages) != 2 || len(st.Blocks) != 2 {
		t.Fatalf("stored = %+v err=%v", st, err)
	}
	a.restoreSession(st)
	if len(a.blocks) != 2 || a.model != "m1" {
		t.Fatalf("restored = %d blocks, model %q", len(a.blocks), a.model)
	}
}

func TestFunctionsMenu(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		width: 60, height: 20, model: "m", showReasoning: true, quiet: true,
		features:  normalizeFeatures(defaultFeatures()),
		sessionID: newSessionID(),
		sessStart: time.Now(),
	}
	a.cfg.Root = dir
	a.agent = NewAgent(Config{Root: dir, Features: defaultFeatures()}, func(Event) {})
	a.beginFunctions()
	if a.functions == nil || len(a.functions.items) == 0 {
		t.Fatal("menu not opened")
	}
	// Toggle "todos" on (index 6 in functionPacks).
	idx := -1
	for i, it := range a.functions.items {
		if it.Name == "todos" {
			idx = i
		}
	}
	a.functions.sel = idx
	a.handleFunctionsKey(key{typ: "enter"})
	if !a.features["todos"] {
		t.Fatal("todos should be enabled")
	}
	if !a.agent.features["todos"] {
		t.Fatal("agent features not updated")
	}
	a.handleFunctionsKey(key{typ: "enter"})
	if a.features["todos"] {
		t.Fatal("todos should be disabled again")
	}
	a.handleFunctionsKey(key{typ: "esc"})
	if a.functions != nil {
		t.Fatal("esc should close the menu")
	}
}
