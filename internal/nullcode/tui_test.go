package nullcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nullabby/nullcode/internal/upstream"
)

func TestPaletteOpenAndFilter(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", showReasoning: true, quiet: true}
	a.handleKey(key{typ: "rune", r: '/'})
	if a.palette == nil {
		t.Fatal("/ should open the palette")
	}
	for _, r := range "hel" {
		a.handleKey(key{typ: "rune", r: r})
	}
	items := a.paletteItems()
	if len(items) != 1 || items[0].cmd != "/help" {
		t.Fatalf("items = %+v", items)
	}
	a.handleKey(key{typ: "rune", r: 'z'})
	if items = a.paletteItems(); len(items) != 0 {
		t.Fatalf("no-match filter should empty items: %+v", items)
	}
	// Backspace re-widens the filter.
	a.handleKey(key{typ: "backspace"})
	if items = a.paletteItems(); len(items) != 1 || items[0].cmd != "/help" {
		t.Fatalf("items after backspace = %+v", items)
	}
}

func TestPaletteRunsCommand(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", showReasoning: true, quiet: true}
	a.handleKey(key{typ: "rune", r: '/'})
	a.handleKey(key{typ: "rune", r: 'h'})
	a.handleKey(key{typ: "rune", r: 'e'})
	a.handleKey(key{typ: "enter"})
	if a.palette != nil {
		t.Fatal("enter should close the palette")
	}
	found := false
	for _, b := range a.blocks {
		if b.kind == bNotice && b.label == "help" {
			found = true
		}
	}
	if !found {
		t.Fatal("/help was not executed")
	}
}

func TestPaletteOpensCommandDialog(t *testing.T) {
	dir := t.TempDir()
	a := &App{width: 60, height: 20, model: "m", showReasoning: true, quiet: true, authFile: filepath.Join(dir, "config.json")}
	a.features = normalizeFeatures(defaultFeatures())
	a.cfg.Root = dir
	a.agent = NewAgent(Config{Root: dir, Model: "m", Features: defaultFeatures()}, func(Event) {})
	a.handleKey(key{typ: "rune", r: '/'})
	for _, r := range "thi" {
		a.handleKey(key{typ: "rune", r: r})
	}
	// "thi" matches only /think; enter closes the palette and opens the
	// command's own dialog.
	a.handleKey(key{typ: "enter"})
	if a.palette != nil {
		t.Fatal("enter should close the palette")
	}
	if a.choice == nil || a.choice.title != "Model thinking" {
		t.Fatalf("expected the /think dialog, got choice=%+v", a.choice)
	}
	a.handleChoiceKey(key{typ: "down"})
	a.handleChoiceKey(key{typ: "enter"})
	if a.think {
		t.Fatal("choosing off should disable thinking")
	}
}

func TestPaletteNavigation(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", showReasoning: true, quiet: true}
	a.beginPalette()
	items := a.paletteItems()
	if len(items) < 3 {
		t.Fatalf("expected several commands, got %d", len(items))
	}
	a.handlePaletteKey(key{typ: "down"})
	a.handlePaletteKey(key{typ: "down"})
	if a.palette.sel != 2 {
		t.Fatalf("sel = %d", a.palette.sel)
	}
	a.handlePaletteKey(key{typ: "up"})
	if a.palette.sel != 1 {
		t.Fatalf("sel = %d", a.palette.sel)
	}
	// Esc closes the palette; backspace on an empty filter also closes.
	a.handlePaletteKey(key{typ: "esc"})
	if a.palette != nil {
		t.Fatal("esc should close the palette")
	}
	a.beginPalette()
	a.handlePaletteKey(key{typ: "backspace"})
	if a.palette != nil {
		t.Fatal("backspace on empty filter should close the palette")
	}
}

func TestConnectFlow(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		width: 60, height: 20, model: "", showReasoning: true, quiet: true,
		authFile: filepath.Join(dir, "config.json"),
	}
	a.cfg.BaseURL = "http://127.0.0.1:9/v1"
	a.events = make(chan Event, 16)
	a.quit = make(chan struct{})
	a.beginConnect()
	if a.connect == nil || a.connect.step != 0 {
		t.Fatalf("connect = %+v", a.connect)
	}
	a.handleConnectKey(key{typ: "rune", r: 's'})
	a.handleConnectKey(key{typ: "paste", text: "k-token"})
	a.handleConnectKey(key{typ: "enter"})
	if a.connect.step != 1 {
		t.Fatalf("step = %d", a.connect.step)
	}
	// Leave URL empty (default applies); jump to the model list to avoid
	// a network fetch, then pick with arrows + enter. Models are grouped
	// by provider: rows = [header "Other", m1, m2].
	a.connect.step = 2
	a.connect.models = []string{"m1", "m2"}
	a.handleConnectKey(key{typ: "down"})
	if a.connect.sel != 1 {
		t.Fatalf("sel = %d", a.connect.sel)
	}
	a.handleConnectKey(key{typ: "down"})
	if a.connect.sel != 2 {
		t.Fatalf("sel = %d", a.connect.sel)
	}
	a.handleConnectKey(key{typ: "enter"})
	if a.connect != nil {
		t.Fatal("connect should be finished")
	}
	c, ok := loadAuthFrom(a.authFile)
	if !ok || c.APIKey != "sk-token" || c.Model != "m2" || c.BaseURL != "http://127.0.0.1:9/v1" {
		t.Fatalf("saved = %+v ok=%v", c, ok)
	}
	if a.agent == nil {
		t.Fatal("agent not started after connect")
	}
	if a.model != "m2" {
		t.Fatalf("model = %q", a.model)
	}
}

func TestConnectCancel(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		width: 60, height: 20, model: "", showReasoning: true, quiet: true,
		authFile: filepath.Join(dir, "config.json"),
	}
	a.cfg.BaseURL = "http://127.0.0.1:9/v1"
	a.events = make(chan Event, 16)
	a.quit = make(chan struct{})
	a.beginConnect()
	a.handleConnectKey(key{typ: "rune", r: 'x'})
	a.handleConnectKey(key{typ: "esc"})
	if a.connect != nil {
		t.Fatal("connect should be closed")
	}
	if _, ok := loadAuthFrom(a.authFile); ok {
		t.Fatal("cancel should not save")
	}
	if a.agent == nil {
		t.Fatal("agent not started after cancel")
	}
}

func TestConnectModelNav(t *testing.T) {
	a := &App{width: 60, height: 20, model: "", showReasoning: true, quiet: true}
	a.beginConnect()
	a.connect.step = 2
	a.connect.models = []string{"m1", "m2", "m3"}
	// Grouped rows: [header, m1, m2, m3] — up from the header stays put,
	// down skips to the first model, and the last model clamps.
	a.handleConnectKey(key{typ: "up"})
	if a.connect.sel != 0 {
		t.Fatalf("sel = %d", a.connect.sel)
	}
	a.handleConnectKey(key{typ: "down"})
	a.handleConnectKey(key{typ: "down"})
	if a.connect.sel != 2 {
		t.Fatalf("sel = %d", a.connect.sel)
	}
	a.handleConnectKey(key{typ: "down"})
	if a.connect.sel != 3 {
		t.Fatalf("sel = %d", a.connect.sel)
	}
	a.handleConnectKey(key{typ: "down"})
	if a.connect.sel != 3 {
		t.Fatalf("sel should clamp, got %d", a.connect.sel)
	}
}

func TestConnectFetchFailure(t *testing.T) {
	a := &App{width: 60, height: 20, model: "", showReasoning: true, quiet: true, authFile: filepath.Join(t.TempDir(), "config.json")}
	a.cfg.BaseURL = "http://127.0.0.1:9/v1"
	a.beginConnect()
	a.handleConnectKey(key{typ: "rune", r: 'k'})
	a.handleConnectKey(key{typ: "enter"})
	a.handleConnectKey(key{typ: "enter"})
	if a.connect == nil || a.connect.step != 3 {
		t.Fatalf("expected manual model step, got %+v", a.connect)
	}
	// A failed fetch routes the dialog to manual model entry with a status.
	a.handleEvent(Event{Kind: "connect_models", Text: "dial refused"})
	if a.connect == nil || a.connect.step != 3 || a.connect.status == "" {
		t.Fatalf("expected failure status, got %+v", a.connect)
	}
	a.handleConnectKey(key{typ: "rune", r: 'm'})
	a.handleConnectKey(key{typ: "enter"})
	if a.connect != nil {
		t.Fatal("connect should be finished")
	}
	if a.model != "m" {
		t.Fatalf("model = %q", a.model)
	}
}

func TestFramePalette(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", think: true, showReasoning: true}
	a.beginPalette()
	lines := frameLines(t, a)
	if len(lines) != 20 {
		t.Fatalf("lines = %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Commands") || !strings.Contains(joined, "/help") {
		t.Fatal("palette modal missing commands")
	}
	a.palette.filter = []rune("he")
	lines = frameLines(t, a)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "/help") || strings.Contains(joined, "/clear") {
		t.Fatal("filter should narrow the palette")
	}
	for i, line := range lines {
		clean := ansiRE.ReplaceAllString(line, "")
		if got := strWidth(clean); got != 60 {
			t.Fatalf("line %d width = %d, want 60: %q", i, got, clean)
		}
	}
}

func TestFrameSetup(t *testing.T) {
	a := &App{width: 70, height: 20, model: "", think: true, showReasoning: true}
	a.cfg.BaseURL = defaultBaseURL
	a.beginConnect()
	lines := frameLines(t, a)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "API token") || !strings.Contains(joined, "Connect provider") {
		t.Fatal("connect modal missing token step")
	}
	a.connect.token = []rune("secret")
	lines = frameLines(t, a)
	joined = strings.Join(lines, "\n")
	if strings.Contains(joined, "secret") {
		t.Fatal("connect modal should mask the token")
	}
	for i, line := range lines {
		clean := ansiRE.ReplaceAllString(line, "")
		if got := strWidth(clean); got != 70 {
			t.Fatalf("line %d width = %d, want 70: %q", i, got, clean)
		}
	}
	// Model list step renders ids with a selection marker.
	a.connect.step = 2
	a.connect.models = []string{"m1", "m2"}
	lines = frameLines(t, a)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "m2") {
		t.Fatal("connect modal missing model list")
	}
}

func TestProviderLabel(t *testing.T) {
	cases := map[string]string{
		"https://api.steelai.lol/v1":  "steelai.lol",
		"https://nullaby.org/v1":      "nullaby.org",
		"http://127.0.0.1:8080/v1":    "127.0.0.1",
		"https://api.deepseek.com/v1": "deepseek.com",
		"not a url":                   "not a url",
	}
	for base, want := range cases {
		if got := providerLabel(base); got != want {
			t.Fatalf("providerLabel(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestPickerRowsGroupAndFilter(t *testing.T) {
	a := &App{width: 60, height: 20, model: "", showReasoning: true, quiet: true}
	a.providers = []providerAuth{
		{BaseURL: "https://api.steelai.lol/v1", APIKey: "k"},
		{BaseURL: "https://nullaby.org/v1", APIKey: "k"},
	}
	a.providerModels = map[string][]string{
		"https://api.steelai.lol/v1": {"glm-5-3-flash", "deepseek-v4.1-flash"},
		"https://nullaby.org/v1":     {"qwen3.5-122b"},
	}
	// Providers sorted by label: nullaby.org, steelai.lol — each group headed
	// by a header row, models sorted within the group.
	rows := a.pickerRows("")
	want := []pickerRow{
		{group: "nullaby.org"},
		{provider: "https://nullaby.org/v1", model: "qwen3.5-122b"},
		{group: "steelai.lol"},
		{provider: "https://api.steelai.lol/v1", model: "deepseek-v4.1-flash"},
		{provider: "https://api.steelai.lol/v1", model: "glm-5-3-flash"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for i, r := range want {
		if rows[i] != r {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], r)
		}
	}
	// The filter narrows across all providers.
	rows = a.pickerRows("glm")
	if len(rows) != 2 || rows[0].group != "steelai.lol" || rows[1].model != "glm-5-3-flash" {
		t.Fatalf("filtered rows = %+v", rows)
	}
}

func TestPickerKeys(t *testing.T) {
	a := &App{width: 60, height: 20, model: "", showReasoning: true, quiet: true, authFile: filepath.Join(t.TempDir(), "config.json")}
	a.cfg.BaseURL = "https://steelai.test/v1"
	a.providers = []providerAuth{{BaseURL: "https://steelai.test/v1", APIKey: "k"}}
	a.providerModels = map[string][]string{
		"https://steelai.test/v1": {"glm-5", "deepseek-v4"},
	}
	a.beginPicker()
	if a.picker == nil {
		t.Fatal("beginPicker should open the picker")
	}
	// Rows: [steelai.test header, deepseek-v4, glm-5]. Down from the header
	// selects the first model; the last model clamps.
	a.handlePickerKey(key{typ: "down"})
	if a.picker.sel != 1 {
		t.Fatalf("sel = %d", a.picker.sel)
	}
	a.handlePickerKey(key{typ: "down"})
	a.handlePickerKey(key{typ: "down"})
	if a.picker.sel != 2 {
		t.Fatalf("sel = %d", a.picker.sel)
	}
	a.handlePickerKey(key{typ: "up"})
	a.handlePickerKey(key{typ: "up"})
	if a.picker.sel != 1 {
		t.Fatalf("sel = %d", a.picker.sel)
	}
	// Filter narrows the rows.
	for _, r := range "glm" {
		a.handlePickerKey(key{typ: "rune", r: r})
	}
	if rows := a.pickerItems(); len(rows) != 2 || rows[0].model != "" || rows[1].model != "glm-5" {
		t.Fatalf("filtered rows = %+v", rows)
	}
	// Enter picks the selected model and saves the config.
	a.handlePickerKey(key{typ: "enter"})
	if a.picker != nil {
		t.Fatal("enter should close the picker")
	}
	if a.model != "glm-5" {
		t.Fatalf("model = %q", a.model)
	}
	if c, ok := loadAuthFrom(a.authFile); !ok || c.Model != "glm-5" || len(c.Providers) != 1 {
		t.Fatalf("saved = %+v ok=%v", c, ok)
	}
}

func TestPickerSwitchesProvider(t *testing.T) {
	a := &App{width: 60, height: 20, model: "", showReasoning: true, quiet: true, authFile: filepath.Join(t.TempDir(), "config.json")}
	a.cfg.BaseURL = "https://nullaby.test/v1"
	a.cfg.APIKey = "k1"
	a.agent = NewAgent(a.cfg, func(Event) {})
	a.providers = []providerAuth{
		{BaseURL: "https://nullaby.test/v1", APIKey: "k1"},
		{BaseURL: "https://other.test/v1", APIKey: "k2"},
	}
	a.providerModels = map[string][]string{
		"https://nullaby.test/v1": {"qwen-1"},
		"https://other.test/v1":   {"glm-9"},
	}
	a.beginPicker()
	rows := a.pickerItems()
	// Rows: [nullaby.test hdr, qwen-1, other.test hdr, glm-9] — select glm-9.
	if len(rows) != 4 {
		t.Fatalf("rows = %+v", rows)
	}
	a.picker.sel = 3
	a.handlePickerKey(key{typ: "enter"})
	if a.picker != nil {
		t.Fatal("enter should close the picker")
	}
	if a.model != "glm-9" || a.cfg.BaseURL != "https://other.test/v1" || a.cfg.APIKey != "k2" {
		t.Fatalf("provider switch: model=%q base=%q key=%q", a.model, a.cfg.BaseURL, a.cfg.APIKey)
	}
	if a.agent.cfg.BaseURL != "https://other.test/v1" {
		t.Fatalf("agent endpoint = %q", a.agent.cfg.BaseURL)
	}
	if c, ok := loadAuthFrom(a.authFile); !ok || c.BaseURL != "https://other.test/v1" {
		t.Fatalf("saved = %+v ok=%v", c, ok)
	}
}

func TestPickerCustomModel(t *testing.T) {
	a := &App{width: 60, height: 20, model: "", showReasoning: true, quiet: true, authFile: filepath.Join(t.TempDir(), "config.json")}
	a.beginPicker()
	for _, r := range "my-model" {
		a.handlePickerKey(key{typ: "rune", r: r})
	}
	a.handlePickerKey(key{typ: "enter"})
	if a.model != "my-model" {
		t.Fatalf("custom model = %q", a.model)
	}
	if a.picker != nil {
		t.Fatal("enter should close the picker")
	}
}

func TestPickerBackspaceCloses(t *testing.T) {
	a := &App{width: 60, height: 20, model: "", showReasoning: true, quiet: true}
	a.beginPicker()
	a.handlePickerKey(key{typ: "backspace"})
	if a.picker != nil {
		t.Fatal("backspace on an empty filter should close the picker")
	}
}

func TestFrameModelPicker(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", think: true, showReasoning: true}
	a.providers = []providerAuth{
		{BaseURL: "https://api.steelai.test/v1", APIKey: "k"},
		{BaseURL: "https://nullaby.test/v1", APIKey: "k"},
	}
	a.providerModels = map[string][]string{
		"https://api.steelai.test/v1": {"deepseek-v4.1-flash", "glm-5-3-flash"},
		"https://nullaby.test/v1":     {"qwen3.5-122b"},
	}
	a.beginPicker()
	lines := frameLines(t, a)
	if len(lines) != 20 {
		t.Fatalf("lines = %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Models", "search models", "nullaby.test", "steelai.test", "deepseek-v4.1-flash"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("model modal missing %q", want)
		}
	}
	for i, line := range lines {
		clean := ansiRE.ReplaceAllString(line, "")
		if got := strWidth(clean); got != 60 {
			t.Fatalf("line %d width = %d, want 60: %q", i, got, clean)
		}
	}
	// Filter narrows the visible list.
	a.picker.filter = []rune("deep")
	lines = frameLines(t, a)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "deepseek-v4.1-flash") || strings.Contains(joined, "nullaby.test") {
		t.Fatal("filter should narrow the model list")
	}
}

func TestFrameChoiceModal(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", think: true, showReasoning: true}
	a.beginToolsPicker()
	lines := frameLines(t, a)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Tool calling", "auto", "protocol", "native"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("choice modal missing %q", want)
		}
	}
	a.handleChoiceKey(key{typ: "down"})
	a.handleChoiceKey(key{typ: "enter"})
	if a.choice != nil {
		t.Fatal("enter should close the choice modal")
	}
	if a.cfg.ToolMode != "protocol" {
		t.Fatalf("tool mode = %q", a.cfg.ToolMode)
	}
}

func TestSessionTopic(t *testing.T) {
	msgs := []upstream.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "Fix the narrow width frame bug in the renderer\nand also check tests"},
	}
	topic := sessionTopic(msgs)
	if topic != "Fix the narrow width frame bug in the renderer" {
		t.Fatalf("topic = %q", topic)
	}
	long := []upstream.Message{{Role: "user", Content: strings.Repeat("x", 100)}}
	if got := sessionTopic(long); len([]rune(got)) != 61 {
		t.Fatalf("long topic = %d runes", len([]rune(got)))
	}
	if got := sessionTopic(nil); got != "" {
		t.Fatalf("empty topic = %q", got)
	}
}

func TestFrameSessionsModal(t *testing.T) {
	dir := t.TempDir()
	sessionsRoot = dir
	s := storedSession{ID: "sess-1", Topic: "Fix the renderer bug", Started: time.Now(), Updated: time.Now(), Model: "m"}
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(filepath.Join(dir, "sess-1.json"), data, 0o600)
	a := &App{width: 60, height: 20, model: "m", think: true, showReasoning: true}
	a.beginSessions()
	if a.sessionsM == nil || len(a.sessionsM.items) != 1 {
		t.Fatalf("sessions = %+v", a.sessionsM)
	}
	lines := frameLines(t, a)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Sessions", "Fix the renderer bug", "0 msgs"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sessions modal missing %q", want)
		}
	}
	a.handleSessionsKey(key{typ: "esc"})
	if a.sessionsM != nil {
		t.Fatal("esc should close the sessions modal")
	}
}

func TestEffortCommand(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", showReasoning: true, quiet: true, authFile: filepath.Join(t.TempDir(), "config.json")}
	a.command("/effort medium")
	if a.cfg.ReasoningEffort != "medium" {
		t.Fatalf("effort = %q", a.cfg.ReasoningEffort)
	}
	if c, ok := loadAuthFrom(a.authFile); !ok || c.ReasoningEffort != "medium" {
		t.Fatalf("saved = %+v", c)
	}
	a.command("/effort default")
	if a.cfg.ReasoningEffort != "" {
		t.Fatalf("effort after default = %q", a.cfg.ReasoningEffort)
	}
	a.command("/effort bogus")
	if a.cfg.ReasoningEffort != "" {
		t.Fatalf("bogus effort should be rejected, got %q", a.cfg.ReasoningEffort)
	}
}

func TestFrameToggleModal(t *testing.T) {
	dir := t.TempDir()
	a := &App{width: 60, height: 20, model: "m", think: true, showReasoning: true, quiet: true, authFile: filepath.Join(dir, "config.json")}
	a.agent = NewAgent(Config{Root: dir, Model: "m"}, func(Event) {})
	a.command("/yolo")
	if a.choice == nil || a.choice.title != "Auto approvals" {
		t.Fatalf("choice = %+v", a.choice)
	}
	// a.yolo is false, so approvals are currently on — cursor on "on".
	if a.choice.sel != 0 {
		t.Fatalf("cursor should be on on, got %d", a.choice.sel)
	}
	a.handleChoiceKey(key{typ: "down"})
	a.handleChoiceKey(key{typ: "enter"})
	if !a.yolo {
		t.Fatal("enter on off should enable yolo")
	}
}

func TestContextCommand(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", showReasoning: true, quiet: true, authFile: filepath.Join(t.TempDir(), "config.json")}
	a.command("/context 4096")
	if a.cfg.ContextTokens != 4096 {
		t.Fatalf("ctx = %d", a.cfg.ContextTokens)
	}
	if c, ok := loadAuthFrom(a.authFile); !ok || c.ContextTokens != 4096 {
		t.Fatalf("saved = %+v", c)
	}
	a.command("/context auto")
	if a.cfg.ContextTokens != 0 {
		t.Fatalf("auto ctx = %d", a.cfg.ContextTokens)
	}
}
