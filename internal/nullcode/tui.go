package nullcode

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/nullabby/nullcode/internal/upstream"
)

type blockKind int

const (
	bUser blockKind = iota
	bAssistant
	bReasoning
	bTool
	bResult
	bDiff
	bError
	bNotice
)

type Block struct {
	kind      blockKind
	label     string
	text      string
	meta      string
	streaming bool

	cache      []string
	cacheW     int
	cacheExp   bool
	cacheLen   int
	cacheMeta  string
	cacheState bool
}

// App is the NullCode terminal UI.
type App struct {
	cfg   Config
	agent *Agent

	width, height int
	blocks        []*Block
	input         []rune
	cursor        int
	cursorCol     int
	hist          []string
	histPos       int
	draft         string
	scroll        int
	maxScroll     int
	expanded      bool
	showReasoning bool

	busy     bool
	status   string
	spinner  int
	tick     int
	approval *Approval
	cancel   context.CancelFunc

	model  string
	think  bool
	yolo   bool
	tokens int64
	turn   int

	connect   *connectState
	functions *functionsState
	palette   *paletteState
	picker    *modelPicker
	choice    *choicePicker
	sessionsM *sessionsState
	ask       *AskPrompt
	needsAuth bool
	authFile  string
	features  map[string]bool
	sessionID string
	sessStart time.Time
	quiet     bool

	providers      []providerAuth
	providerModels map[string][]string
	providerCtx    map[string]int
	models         []string

	events chan Event
	keys   chan key
	quit   chan struct{}
	once   sync.Once
	dirty  bool
}

// functionsState is the /functions menu: toggle tool packs on/off.
type functionsState struct {
	sel   int
	items []funcItem
	on    map[string]bool
}

// paletteState is the command palette: typing / opens a modal listing all
// slash commands. The filter row narrows the list; Enter runs the command,
// which opens its own dialog when it needs input.
type paletteState struct {
	sel    int
	filter []rune
}

// modelPicker is the /model dialog: search/filter models advertised by the
// provider, or type a custom model id. Rows are grouped by provider.
type modelPicker struct {
	sel    int
	filter []rune
}

// pickerRow is one line of a model list: a provider group header
// (model == "") or a selectable model with its source provider.
type pickerRow struct {
	group    string
	provider string
	model    string
}

// choicePicker is a generic choice dialog (/tools tool mode).
type choicePicker struct {
	title   string
	choices []choiceItem
	sel     int
	apply   func(string)
}

// sessionsState is the /sessions dialog: saved sessions with their topics.
type sessionsState struct {
	items []sessionInfo
	sel   int
}

type choiceItem struct {
	Value string
	Desc  string
}

// connectState is the first-launch / /connect dialog: it walks through
// API token, optional URL, then model selection, rendered as a centered
// modal window. Empty fields keep the current/default values.
type connectState struct {
	step     int // 0 token, 1 url, 2 models, 3 manual model id
	token    []rune
	url      []rune
	custom   []rune
	base     string // resolved backend URL used for the model fetch
	models   []string
	sel      int
	fetching bool
	status   string
	curBase  string
	curKey   string
	curModel string
}

// Run starts the NullCode TUI.
func Run(cfg Config) error {
	if cfg.Root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg.Root = wd
	}
	authFile, _ := authPath()
	saved, hasSaved := loadAuthFrom(authFile)
	setTheme(saved.Theme != "light")
	if cfg.BaseURL == "" {
		cfg.BaseURL = saved.BaseURL
	}
	if cfg.APIKey == "" {
		cfg.APIKey = saved.APIKey
	}
	if cfg.Model == "" {
		cfg.Model = saved.Model
	}
	if cfg.ToolMode == "" {
		cfg.ToolMode = saved.ToolMode
	}
	if cfg.ReasoningEffort == "" {
		cfg.ReasoningEffort = saved.ReasoningEffort
	}
	if cfg.ContextTokens == 0 {
		cfg.ContextTokens = saved.ContextTokens
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Features == nil {
		if len(saved.Features) > 0 {
			cfg.Features = saved.Features
		} else {
			cfg.Features = defaultFeatures()
		}
	}
	if !hasSaved && cfg.APIKey != "" {
		_ = saveAuthTo(authFile, authConfig{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model, Theme: themeName(), Features: cfg.Features, ToolMode: cfg.ToolMode,
			ReasoningEffort: cfg.ReasoningEffort, ContextTokens: cfg.ContextTokens, Providers: []providerAuth{{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey}}})
		hasSaved = true
	}
	provs := saved.Providers
	if len(provs) == 0 && cfg.BaseURL != "" && cfg.APIKey != "" {
		provs = []providerAuth{{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey}}
	}
	a := &App{
		cfg:            cfg,
		model:          cfg.Model,
		think:          cfg.Think,
		yolo:           cfg.AutoApprove,
		showReasoning:  true,
		authFile:       authFile,
		features:       normalizeFeatures(cfg.Features),
		sessionID:      newSessionID(),
		sessStart:      time.Now(),
		needsAuth:      !hasSaved && cfg.APIKey == "",
		providers:      provs,
		providerModels: map[string][]string{},
	}
	a.welcome()
	return a.run()
}

func (a *App) welcome() {
	root := shortPath(a.cfg.Root)
	mdl := a.model
	if mdl == "" {
		mdl = "auto"
	}
	a.blocks = append(a.blocks, &Block{
		kind:  bNotice,
		label: "nullcode",
		text: "A minimal coding agent for the nullabby model family.\n\n" +
			"root   " + root + "\n" +
			"model  " + mdl + "\n\n" +
			"Type a task, or /help for commands. exec, write, and patch ask for approval\n" +
			"unless you enable /yolo. Ctrl+D quits.",
	})
}

func (a *App) run() error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("nullcode is a TUI; run it in an interactive terminal")
	}
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		a.width, a.height = w, h
	} else {
		a.width, a.height = 100, 30
	}

	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	enableVT()
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[?2004h\x1b[?1000h\x1b[?1002h\x1b[?1006h\x1b[2J\x1b[H")
	defer func() {
		fmt.Print("\x1b[?1006l\x1b[?1002l\x1b[?1000l\x1b[?2004l\x1b[?7h\x1b[?25h\x1b[0m\x1b[?1049l")
		_ = term.Restore(fd, old)
	}()

	a.events = make(chan Event, 4096)
	a.keys = make(chan key, 64)
	a.quit = make(chan struct{})
	if a.needsAuth {
		a.beginConnect()
	} else {
		a.startAgent()
	}
	go readKeys(os.Stdin, a.keys)

	ticker := time.NewTicker(70 * time.Millisecond)
	defer ticker.Stop()

	a.dirty = true
	for {
		select {
		case k := <-a.keys:
			a.handleKey(k)
		case e := <-a.events:
			a.handleEvent(e)
		case <-ticker.C:
			a.tick++
			if a.tick%4 == 0 {
				a.checkSize()
			}
			if a.dirty || a.busy || a.withSpinner() {
				if a.busy {
					a.spinner++
				}
				a.render()
				a.dirty = false
			}
		case <-a.quit:
			if a.cancel != nil {
				a.cancel()
			}
			return nil
		}
	}
}

func (a *App) withSpinner() bool { return a.busy || a.approval != nil }

func (a *App) post(e Event) {
	select {
	case a.events <- e:
	case <-a.quit:
	}
}

func (a *App) startAgent() {
	a.agent = NewAgent(a.cfg, a.post)
	a.syncCtxLimit()
	// Only probe a backend the user actually configured (a non-default saved
	// URL or a flag/env override). The shipped default nullaby.org is a
	// placeholder that most users never reach, and auto-probing it just
	// spams a "backend unreachable" notice on first launch.
	if a.cfg.BaseURL != "" && a.cfg.BaseURL != defaultBaseURL {
		go a.probe(a.model)
	}
}

func (a *App) beginConnect() {
	a.connect = &connectState{curBase: a.cfg.BaseURL, curKey: a.cfg.APIKey, curModel: a.model}
}

func (a *App) handleConnectKey(k key) {
	c := a.connect
	base := c.base
	if base == "" {
		base = a.cfg.BaseURL
	}
	rows := modelRows(base, c.models, "")
	switch k.typ {
	case "up":
		if c.step == 2 && !c.fetching {
			c.sel = connectSelect(rows, c.sel, -1)
		}
	case "down":
		if c.step == 2 && !c.fetching {
			c.sel = connectSelect(rows, c.sel, 1)
		}
	case "rune":
		if k.r < 32 {
			return
		}
		switch c.step {
		case 0:
			c.token = append(c.token, k.r)
		case 1:
			c.url = append(c.url, k.r)
		case 3:
			c.custom = append(c.custom, k.r)
		}
	case "paste":
		txt := strings.ReplaceAll(k.text, "\r\n", " ")
		txt = strings.ReplaceAll(txt, "\n", " ")
		txt = strings.ReplaceAll(txt, "\r", " ")
		switch c.step {
		case 0:
			c.token = append(c.token, []rune(sanitizeText(txt))...)
		case 1:
			c.url = append(c.url, []rune(sanitizeText(txt))...)
		case 3:
			c.custom = append(c.custom, []rune(sanitizeText(txt))...)
		}
	case "backspace":
		switch c.step {
		case 0:
			if len(c.token) > 0 {
				c.token = c.token[:len(c.token)-1]
			}
		case 1:
			if len(c.url) > 0 {
				c.url = c.url[:len(c.url)-1]
			}
		case 3:
			if len(c.custom) > 0 {
				c.custom = c.custom[:len(c.custom)-1]
			}
		}
	case "enter":
		switch c.step {
		case 0:
			c.step = 1
		case 1:
			c.step = 2
			a.fetchModels()
		case 2:
			if !c.fetching && len(c.models) > 0 {
				sel := c.sel
				if sel < 0 || sel >= len(rows) || rows[sel].model == "" {
					sel = -1
					for i, r := range rows {
						if r.model != "" {
							sel = i
							break
						}
					}
				}
				if sel >= 0 {
					a.finishConnect(rows[sel].model)
				}
			}
		case 3:
			if id := strings.TrimSpace(string(c.custom)); id != "" {
				a.finishConnect(id)
			}
		}
	case "esc", "ctrl-c":
		a.finishConnect("")
	case "ctrl-d":
		a.shutdown()
	}
}

// fetchModels queries the provider being connected for its model list.
func (a *App) fetchModels() {
	c := a.connect
	if a.events == nil {
		c.step = 3
		return
	}
	base := strings.TrimSpace(string(c.url))
	if base == "" {
		base = a.cfg.BaseURL
	}
	key := strings.TrimSpace(string(c.token))
	if key == "" {
		key = a.cfg.APIKey
	}
	c.base = base
	c.fetching = true
	c.status = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		models, _, err := upstream.New(base, key).Models(ctx)
		e := Event{Kind: "connect_models", Models: models}
		if err != nil {
			e.Text = cleanNetErr(err)
		}
		a.post(e)
	}()
}

// saveConfig persists the active provider, model, theme and feature packs.
func (a *App) saveConfig() error {
	feats := make([]string, 0, len(a.features))
	for k, on := range a.features {
		if on && k != "done" {
			feats = append(feats, k)
		}
	}
	sort.Strings(feats)
	return saveAuthTo(a.authFile, authConfig{
		BaseURL:         a.cfg.BaseURL,
		APIKey:          a.cfg.APIKey,
		Model:           a.model,
		Theme:           themeName(),
		Features:        feats,
		ToolMode:        a.cfg.ToolMode,
		ReasoningEffort: a.cfg.ReasoningEffort,
		ContextTokens:   a.cfg.ContextTokens,
		Providers:       a.providers,
	})
}

func (a *App) finishConnect(model string) {
	c := a.connect
	a.connect = nil
	if model != "" {
		if v := strings.TrimSpace(string(c.token)); v != "" {
			a.cfg.APIKey = v
		}
		if v := strings.TrimSpace(string(c.url)); v != "" {
			a.cfg.BaseURL = v
		}
		a.model = model
		a.cfg.Model = model
		a.upsertProvider(a.cfg.BaseURL, a.cfg.APIKey)
		if c.models != nil {
			if a.providerModels == nil {
				a.providerModels = map[string][]string{}
			}
			a.providerModels[a.cfg.BaseURL] = c.models
			a.models = c.models
		}
		if err := a.saveConfig(); err != nil {
			a.appendBlock(bError, "error", "could not save connection: "+err.Error(), "", false)
		} else {
			a.appendBlock(bNotice, "login", "connected — provider and model saved ("+model+")", "", false)
		}
		a.startAgent()
	} else {
		a.appendBlock(bNotice, "notice", "connect canceled — using current settings (not saved)", "", false)
		if a.agent == nil {
			a.startAgent()
		}
	}
}

func (a *App) probe(model string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, hint, err := a.agent.Probe(ctx)
	if err != nil {
		a.post(Event{Kind: "notice", Text: "backend unreachable at " + a.cfg.BaseURL + ": " + cleanNetErr(err)})
		return
	}
	a.post(Event{Kind: "models", Provider: a.cfg.BaseURL, CtxHint: hint, Models: models})
	shown := a.model
	if shown == "" {
		shown = "auto"
	}
	for _, m := range models {
		if m == a.model {
			return
		}
	}
	if len(models) > 0 {
		a.post(Event{Kind: "notice", Text: "backend advertises: " + strings.Join(models, ", ") + " (selected model: " + shown + ")"})
	}
}

func (a *App) checkSize() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return
	}
	if w != a.width || h != a.height {
		a.width, a.height = w, h
		a.dirty = true
	}
}

func (a *App) handleEvent(e Event) {
	a.dirty = true
	switch e.Kind {
	case "status":
		a.status = e.Text
	case "reasoning":
		if a.showReasoning {
			a.appendStream(bReasoning, "thinking", e.Text)
		}
	case "content":
		a.appendStream(bAssistant, "nullcode", e.Text)
	case "action":
		a.stopStreaming()
		a.appendBlock(bTool, e.Action.Kind, e.Action.Arg, "…", false)
	case "body":
		if b := a.lastBlock(); b != nil && b.kind == bTool {
			b.meta = e.Meta
		}
	case "output":
		a.appendStream(bResult, "result", e.Text)
	case "diff":
		a.stopStreaming()
		a.appendBlock(bDiff, "diff", e.Text, e.Meta, false)
	case "action_done":
		if b := a.lastBlock(); b != nil && b.kind == bResult {
			b.meta = e.Meta
			b.streaming = false
			if b.text == "" {
				b.text = e.Text
			}
		} else {
			a.appendBlock(bResult, "result", e.Text, e.Meta, false)
		}
	case "notice":
		a.stopStreaming()
		a.appendBlock(bNotice, "notice", e.Text, "", false)
	case "error":
		a.stopStreaming()
		a.appendBlock(bError, "error", e.Text, "", false)
	case "usage":
		a.tokens = e.Usage
	case "models":
		if e.Provider != "" {
			if a.providerModels == nil {
				a.providerModels = map[string][]string{}
			}
			a.providerModels[e.Provider] = e.Models
			if e.CtxHint > 0 {
				if a.providerCtx == nil {
					a.providerCtx = map[string]int{}
				}
				a.providerCtx[e.Provider] = e.CtxHint
				// Provider-supplied limit applies when the user hasn't set one.
				if a.cfg.ContextTokens <= 0 && e.Provider == a.cfg.BaseURL && a.agent != nil {
					a.agent.SetContextLimit(e.CtxHint)
				}
			}
		}
		if e.Provider == "" || e.Provider == a.cfg.BaseURL {
			a.models = e.Models
		}
	case "ask":
		a.stopStreaming()
		if e.Ask != nil {
			a.ask = e.Ask
			a.appendBlock(bNotice, "ask", e.Ask.Question, "", false)
		}
	case "connect_models":
		if c := a.connect; c != nil {
			c.fetching = false
			if e.Text != "" || len(e.Models) == 0 {
				c.status = "could not list models"
				if e.Text != "" {
					c.status += ": " + e.Text
				}
				c.step = 3
			} else {
				c.models = e.Models
				c.sel = 0
			}
		}
	case "approval":
		a.stopStreaming()
		if a.yolo {
			select {
			case e.Approval.Resp <- true:
			default:
			}
			return
		}
		a.approval = e.Approval
		a.status = "approval"
	case "done":
		a.stopStreaming()
		a.busy = false
		a.cancel = nil
		a.approval = nil
		a.ask = nil
		a.status = "idle"
		if err := a.saveSession(); err != nil && !a.quiet {
			a.appendBlock(bNotice, "notice", "could not save session: "+err.Error(), "", false)
		}
	}
}

func (a *App) lastBlock() *Block {
	if len(a.blocks) == 0 {
		return nil
	}
	return a.blocks[len(a.blocks)-1]
}

func (a *App) appendBlock(k blockKind, label, text, meta string, streaming bool) {
	a.blocks = append(a.blocks, &Block{kind: k, label: label, text: text, meta: meta, streaming: streaming})
}

func (a *App) appendStream(k blockKind, label, delta string) {
	if delta == "" {
		return
	}
	if b := a.lastBlock(); b != nil && b.kind == k && b.streaming {
		b.text += delta
		return
	}
	a.stopStreaming()
	a.appendBlock(k, label, delta, "", true)
}

func (a *App) stopStreaming() {
	if b := a.lastBlock(); b != nil {
		b.streaming = false
	}
}

func (a *App) shutdown() {
	if a.cancel != nil {
		a.cancel()
	}
	_ = a.saveSession()
	a.once.Do(func() { close(a.quit) })
}

func (a *App) answerAsk(ans string) {
	if a.ask == nil {
		return
	}
	resp := a.ask.Resp
	a.ask = nil
	select {
	case resp <- ans:
	default:
	}
	a.status = "running"
}

func (a *App) beginFunctions() {
	on := make(map[string]bool, len(a.features))
	for k, v := range a.features {
		on[k] = v
	}
	a.functions = &functionsState{items: functionPacks, on: on}
}

func (a *App) handleFunctionsKey(k key) {
	f := a.functions
	switch k.typ {
	case "up":
		if f.sel > 0 {
			f.sel--
		}
	case "down":
		if f.sel < len(f.items)-1 {
			f.sel++
		}
	case "enter":
		name := f.items[f.sel].Name
		f.on[name] = !f.on[name]
		a.features = normalizeFeatures(enabledList(f.on))
		if a.agent != nil {
			a.agent.SetFeatures(enabledList(f.on))
		}
		if err := a.saveConfig(); err != nil && !a.quiet {
			a.appendBlock(bNotice, "notice", "could not save functions: "+err.Error(), "", false)
		}
	case "esc", "ctrl-c":
		a.functions = nil
	case "ctrl-d":
		a.shutdown()
	}
}

func enabledList(on map[string]bool) []string {
	list := make([]string, 0, len(on))
	for k, v := range on {
		if v && k != "done" {
			list = append(list, k)
		}
	}
	sort.Strings(list)
	return list
}

func (a *App) resetSession() {
	a.blocks = nil
	a.agent.Reset()
	a.sessionID = newSessionID()
	a.sessStart = time.Now()
	a.scroll = 0
	a.tokens = 0
	a.turn = 0
	a.welcome()
}

func (a *App) handleKey(k key) {
	a.dirty = true
	defer func() {
		if !a.quiet {
			a.render()
		}
		a.dirty = false
	}()
	if a.connect != nil {
		a.handleConnectKey(k)
		return
	}
	if a.functions != nil {
		a.handleFunctionsKey(k)
		return
	}
	if a.palette != nil {
		a.handlePaletteKey(k)
		return
	}
	if a.picker != nil {
		a.handlePickerKey(k)
		return
	}
	if a.choice != nil {
		a.handleChoiceKey(k)
		return
	}
	if a.sessionsM != nil {
		a.handleSessionsKey(k)
		return
	}
	if a.ask != nil {
		switch k.typ {
		case "enter":
			ans := strings.TrimSpace(string(a.input))
			a.input = nil
			a.cursor = 0
			a.answerAsk(ans)
			return
		case "esc", "ctrl-c":
			a.answerAsk("")
			return
		}
		// fall through: normal editing of the answer
	}
	if a.approval != nil {
		switch k.typ {
		case "rune":
			switch k.r {
			case 'y', 'Y':
				a.answerApproval(true, false)
			case 'n', 'N':
				a.answerApproval(false, false)
			case 'a', 'A':
				a.answerApproval(true, true)
			}
		case "enter":
			a.answerApproval(true, false)
		case "esc", "ctrl-c":
			a.answerApproval(false, false)
		}
		return
	}

	switch k.typ {
	case "rune":
		if k.r == '/' && len(a.input) == 0 {
			a.beginPalette()
			return
		}
		if k.r >= 32 {
			a.insertRunes([]rune{k.r})
		}
	case "paste":
		txt := strings.ReplaceAll(k.text, "\r\n", " ")
		txt = strings.ReplaceAll(txt, "\n", " ")
		txt = strings.ReplaceAll(txt, "\r", " ")
		a.insertRunes([]rune(sanitizeText(txt)))
	case "enter":
		a.submit()
	case "backspace":
		if a.cursor > 0 {
			a.input = append(a.input[:a.cursor-1], a.input[a.cursor:]...)
			a.cursor--
		}
	case "delete":
		if a.cursor < len(a.input) {
			a.input = append(a.input[:a.cursor], a.input[a.cursor+1:]...)
		}
	case "left":
		if a.cursor > 0 {
			a.cursor--
		}
	case "right":
		if a.cursor < len(a.input) {
			a.cursor++
		}
	case "home", "ctrl-a":
		a.cursor = 0
	case "end", "ctrl-e":
		a.cursor = len(a.input)
	case "tab":
		a.insertRunes([]rune("  "))
	case "ctrl-u":
		a.input = append([]rune{}, a.input[a.cursor:]...)
		a.cursor = 0
	case "ctrl-k":
		a.input = a.input[:a.cursor]
	case "ctrl-w":
		a.deleteWord()
	case "up":
		a.historyUp()
	case "down":
		a.historyDown()
	case "pgup":
		a.scroll += a.pageSize() / 2
		a.clampScroll()
	case "pgdn":
		a.scroll -= a.pageSize() / 2
		a.clampScroll()
	case "wheel-up":
		a.scroll += 3
		a.clampScroll()
	case "wheel-down":
		a.scroll -= 3
		a.clampScroll()
	case "ctrl-o":
		a.expanded = !a.expanded
	case "ctrl-l":
		fmt.Print("\x1b[2J")
	case "ctrl-c":
		if a.cancel != nil {
			a.cancel()
			a.status = "stopping"
			a.appendBlock(bNotice, "notice", "stopping current turn…", "", false)
		} else if len(a.input) > 0 {
			a.input = nil
			a.cursor = 0
		} else {
			a.shutdown()
		}
	case "ctrl-d":
		a.shutdown()
	case "esc":
		a.input = nil
		a.cursor = 0
	}
}

func (a *App) pageSize() int {
	n := a.height - 5
	if n < 1 {
		n = 1
	}
	return n
}

func (a *App) clampScroll() {
	if a.scroll < 0 {
		a.scroll = 0
	}
	if a.scroll > a.maxScroll {
		a.scroll = a.maxScroll
	}
	if a.scroll == 0 {
		a.histPos = len(a.hist)
	}
}

func (a *App) insertRunes(r []rune) {
	if len(r) == 0 {
		return
	}
	out := make([]rune, 0, len(a.input)+len(r))
	out = append(out, a.input[:a.cursor]...)
	out = append(out, r...)
	out = append(out, a.input[a.cursor:]...)
	a.input = out
	a.cursor += len(r)
}

func (a *App) deleteWord() {
	i := a.cursor
	for i > 0 && a.input[i-1] == ' ' {
		i--
	}
	for i > 0 && a.input[i-1] != ' ' {
		i--
	}
	a.input = append(a.input[:i], a.input[a.cursor:]...)
	a.cursor = i
}

func (a *App) historyUp() {
	if len(a.hist) == 0 {
		return
	}
	if a.histPos == len(a.hist) {
		a.draft = string(a.input)
	}
	if a.histPos > 0 {
		a.histPos--
		a.input = []rune(a.hist[a.histPos])
		a.cursor = len(a.input)
	}
}

func (a *App) historyDown() {
	if a.histPos >= len(a.hist) {
		return
	}
	a.histPos++
	if a.histPos == len(a.hist) {
		a.input = []rune(a.draft)
	} else {
		a.input = []rune(a.hist[a.histPos])
	}
	a.cursor = len(a.input)
}

type compItem struct {
	cmd  string
	desc string
}

var slashCommands = []compItem{
	{"/help", "show commands"},
	{"/clear", "clear transcript and model context"},
	{"/new", "start a new session (saves the current one)"},
	{"/sessions", "saved sessions with topics"},
	{"/init", "write a NULLCODE.md project context"},
	{"/functions", "toggle tool packs (todos, MCPs, skills…)"},
	{"/model", "choose or search a model"},
	{"/tools", "switch tool calling mode"},
	{"/think", "enable or disable model thinking"},
	{"/effort", "reasoning effort (low/medium/high)"},
	{"/compact", "summarize and free context window space"},
	{"/context", "set the context limit (auto/8k/…/128k)"},
	{"/yolo", "skip exec/write/patch approvals"},
	{"/expand", "show full tool output and reasoning"},
	{"/reasoning", "show or hide thinking blocks"},
	{"/connect", "connect a provider (token, URL, model)"},
	{"/theme", "switch light/dark theme"},
	{"/status", "backend and session details"},
	{"/quit", "leave NullCode"},
}

// paletteItems returns the commands matching the palette filter.
func (a *App) paletteItems() []compItem {
	filter := strings.ToLower(string(a.palette.filter))
	var items []compItem
	for _, c := range slashCommands {
		if filter == "" ||
			strings.Contains(strings.ToLower(c.cmd), filter) ||
			strings.Contains(strings.ToLower(c.desc), filter) {
			items = append(items, c)
		}
	}
	return items
}

func (a *App) beginPalette() {
	a.palette = &paletteState{}
}

func (a *App) closePalette() {
	a.palette = nil
}

func (a *App) handlePaletteKey(k key) {
	p := a.palette
	switch k.typ {
	case "rune":
		if k.r < 32 {
			return
		}
		p.filter = append(p.filter, k.r)
		p.sel = 0
	case "paste":
		txt := strings.ReplaceAll(k.text, "\r\n", " ")
		txt = strings.ReplaceAll(txt, "\n", " ")
		txt = strings.ReplaceAll(txt, "\r", " ")
		txt = sanitizeText(txt)
		if txt == "" {
			return
		}
		p.filter = append(p.filter, []rune(txt)...)
		p.sel = 0
	case "backspace":
		if len(p.filter) > 0 {
			p.filter = p.filter[:len(p.filter)-1]
			p.sel = 0
		} else {
			a.closePalette()
		}
	case "up":
		if p.sel > 0 {
			p.sel--
		}
	case "down":
		if p.sel < len(a.paletteItems())-1 {
			p.sel++
		}
	case "enter", "tab":
		items := a.paletteItems()
		if len(items) == 0 {
			return
		}
		sel := p.sel
		if sel < 0 || sel >= len(items) {
			sel = 0
		}
		it := items[sel]
		cmd := it.cmd
		a.closePalette()
		// Every command opens its own dialog when run without args.
		a.command(cmd)
	case "esc":
		a.closePalette()
	case "ctrl-c":
		a.closePalette()
	case "ctrl-d":
		a.shutdown()
	}
}

// providerLabel derives a display name for a provider from its base URL,
// e.g. "https://api.steelai.lol/v1" → "steelai.lol".
func providerLabel(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	host := strings.TrimPrefix(u.Host, "api.")
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}

// modelRows lists one provider's models as rows under a provider header;
// an optional filter narrows the models by substring.
func modelRows(base string, models []string, filter string) []pickerRow {
	f := strings.ToLower(filter)
	var ms []string
	for _, m := range models {
		if f != "" && !strings.Contains(strings.ToLower(m), f) {
			continue
		}
		ms = append(ms, m)
	}
	if len(ms) == 0 {
		return nil
	}
	sort.Strings(ms)
	rows := make([]pickerRow, 0, len(ms)+1)
	rows = append(rows, pickerRow{group: providerLabel(base)})
	for _, m := range ms {
		rows = append(rows, pickerRow{provider: base, model: m})
	}
	return rows
}

// pickerRows groups every connected provider's models under a provider
// header row (model == ""), sorted by provider label then model id.
func (a *App) pickerRows(filter string) []pickerRow {
	provs := append([]providerAuth(nil), a.providers...)
	sort.Slice(provs, func(i, j int) bool {
		return providerLabel(provs[i].BaseURL) < providerLabel(provs[j].BaseURL)
	})
	var rows []pickerRow
	for _, p := range provs {
		rows = append(rows, modelRows(p.BaseURL, a.providerModels[p.BaseURL], filter)...)
	}
	return rows
}

// pickerItems returns the picker rows matching the picker filter.
func (a *App) pickerItems() []pickerRow {
	return a.pickerRows(string(a.picker.filter))
}

// providerFor returns the saved provider with the given base URL.
func (a *App) providerFor(base string) (providerAuth, bool) {
	for _, p := range a.providers {
		if p.BaseURL == base {
			return p, true
		}
	}
	return providerAuth{}, false
}

// upsertProvider adds a provider to the saved list or updates its token.
func (a *App) upsertProvider(base, key string) {
	for i, p := range a.providers {
		if p.BaseURL == base {
			a.providers[i].APIKey = key
			return
		}
	}
	a.providers = append(a.providers, providerAuth{BaseURL: base, APIKey: key})
}

// connectSelect moves a selection to the nearest model row (skipping group
// headers) in the given direction, clamping when no row is found.
func connectSelect(rows []pickerRow, sel, dir int) int {
	for i := sel + dir; i >= 0 && i < len(rows); i += dir {
		if rows[i].model != "" {
			return i
		}
	}
	return sel
}

func (a *App) beginPicker() {
	a.picker = &modelPicker{}
	// Fetch the model list for every saved provider that has none yet.
	var pending []providerAuth
	for _, p := range a.providers {
		if _, ok := a.providerModels[p.BaseURL]; !ok {
			pending = append(pending, p)
		}
	}
	for _, p := range pending {
		p := p
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			models, hint, err := upstream.New(p.BaseURL, p.APIKey).Models(ctx)
			e := Event{Kind: "models", Provider: p.BaseURL}
			if err == nil {
				e.Models = models
				e.CtxHint = hint
			}
			a.post(e)
		}()
	}
}

func (a *App) pickModel(r pickerRow) {
	a.picker = nil
	m := strings.TrimSpace(r.model)
	if m == "" {
		return
	}
	msg := "model set to " + m
	if r.provider != "" && r.provider != a.cfg.BaseURL {
		if p, ok := a.providerFor(r.provider); ok {
			a.cfg.BaseURL = p.BaseURL
			a.cfg.APIKey = p.APIKey
			a.models = a.providerModels[p.BaseURL]
			if a.agent != nil {
				a.agent.SetEndpoint(p.BaseURL, p.APIKey)
			}
			msg = providerLabel(p.BaseURL) + " · model set to " + m
		}
	}
	a.model = m
	a.cfg.Model = m
	a.syncCtxLimit()
	if a.agent != nil {
		a.agent.SetModel(m)
	}
	if err := a.saveConfig(); err != nil {
		msg += " (not saved: " + err.Error() + ")"
	}
	a.appendBlock(bNotice, "notice", msg, "", false)
}

func (a *App) handlePickerKey(k key) {
	p := a.picker
	rows := a.pickerItems()
	switch k.typ {
	case "rune":
		if k.r < 32 {
			return
		}
		p.filter = append(p.filter, k.r)
		p.sel = 0
	case "paste":
		txt := sanitizeText(strings.ReplaceAll(k.text, "\r\n", " "))
		if txt == "" {
			return
		}
		p.filter = append(p.filter, []rune(txt)...)
		p.sel = 0
	case "backspace":
		if len(p.filter) > 0 {
			p.filter = p.filter[:len(p.filter)-1]
			p.sel = 0
		} else {
			a.picker = nil
		}
	case "up":
		p.sel = connectSelect(rows, p.sel, -1)
	case "down":
		p.sel = connectSelect(rows, p.sel, 1)
	case "enter", "tab":
		sel := p.sel
		if sel < 0 || sel >= len(rows) || rows[sel].model == "" {
			sel = -1
			for i, r := range rows {
				if r.model != "" {
					sel = i
					break
				}
			}
		}
		if sel >= 0 {
			a.pickModel(rows[sel])
			return
		}
		a.pickModel(pickerRow{provider: a.cfg.BaseURL, model: strings.TrimSpace(string(p.filter))})
	case "esc", "ctrl-c":
		a.picker = nil
	case "ctrl-d":
		a.shutdown()
	}
}

func (a *App) handleChoiceKey(k key) {
	c := a.choice
	switch k.typ {
	case "up":
		if c.sel > 0 {
			c.sel--
		}
	case "down":
		if c.sel < len(c.choices)-1 {
			c.sel++
		}
	case "enter", "tab":
		sel := c.sel
		if sel < 0 || sel >= len(c.choices) {
			sel = 0
		}
		value, apply := c.choices[sel].Value, c.apply
		a.choice = nil
		apply(value)
	case "esc", "ctrl-c":
		a.choice = nil
	case "ctrl-d":
		a.shutdown()
	}
}

func (a *App) beginToolsPicker() {
	a.choice = &choicePicker{
		title: "Tool calling",
		choices: []choiceItem{
			{"auto", "detect per model (frontier families use native tools)"},
			{"protocol", "NullCode glyph directives (default for fine-tunes)"},
			{"native", "always use OpenAI tool calling"},
		},
		apply: func(v string) {
			a.cfg.ToolMode = v
			if a.agent != nil {
				a.agent.SetToolMode(v)
			}
			msg := "tool calling: " + v
			if err := a.saveConfig(); err != nil {
				msg += " (not saved: " + err.Error() + ")"
			}
			a.appendBlock(bNotice, "notice", msg, "", false)
		},
	}
}

// beginSessions opens the /sessions dialog with saved sessions and topics.
func (a *App) beginSessions() {
	sessions, err := listSessions()
	if err != nil || len(sessions) == 0 {
		a.appendBlock(bNotice, "notice", "no saved sessions yet", "", false)
		return
	}
	a.sessionsM = &sessionsState{items: sessions}
}

// beginToggleModal opens a two-choice on/off dialog with the current state.
func (a *App) beginToggleModal(title string, cur bool, apply func(bool)) {
	a.choice = &choicePicker{
		title: title,
		choices: []choiceItem{
			{"on", "enabled"},
			{"off", "disabled"},
		},
		sel: map[bool]int{true: 0, false: 1}[cur],
		apply: func(v string) {
			apply(v == "on")
		},
	}
}

func (a *App) beginThemeModal() {
	a.choice = &choicePicker{
		title: "Theme",
		choices: []choiceItem{
			{"dark", "default"},
			{"light", ""},
		},
		sel: map[bool]int{true: 0, false: 1}[themeDark],
		apply: func(v string) {
			setTheme(v == "dark")
			msg := "theme " + themeName()
			if err := a.saveConfig(); err != nil {
				msg += " (not saved: " + err.Error() + ")"
			}
			a.appendBlock(bNotice, "notice", msg, "", false)
		},
	}
}

func (a *App) toggleTheme() {
	setTheme(!themeDark)
	msg := "theme " + themeName()
	if err := a.saveConfig(); err != nil {
		msg += " (not saved: " + err.Error() + ")"
	}
	a.appendBlock(bNotice, "notice", msg, "", false)
}

// beginEffortPicker opens the /effort dialog (reasoning_effort).
func (a *App) beginEffortPicker() {
	a.choice = &choicePicker{
		title: "Reasoning effort",
		choices: []choiceItem{
			{"default", "let the backend decide (sends nothing)"},
			{"minimal", "lowest thinking effort"},
			{"low", ""},
			{"medium", ""},
			{"high", "deepest thinking effort"},
		},
		apply: func(v string) {
			if v == "default" {
				v = ""
			}
			a.setEffort(v)
		},
	}
}

func (a *App) setEffort(v string) {
	if v == "default" {
		v = ""
	}
	switch v {
	case "":
	case "minimal", "low", "medium", "high":
	default:
		a.appendBlock(bNotice, "notice", "usage: /effort default|minimal|low|medium|high", "", false)
		return
	}
	a.cfg.ReasoningEffort = v
	if a.agent != nil {
		a.agent.SetEffort(v)
	}
	msg := "reasoning effort: " + v
	if msg == "reasoning effort: " {
		msg = "reasoning effort: default"
	}
	if err := a.saveConfig(); err != nil {
		msg += " (not saved: " + err.Error() + ")"
	}
	a.appendBlock(bNotice, "notice", msg, "", false)
}

// startCompact runs /compact: summarize and replace the conversation.
func (a *App) startCompact() {
	if a.busy {
		a.appendBlock(bNotice, "notice", "wait for the current turn before compacting", "", false)
		return
	}
	a.busy = true
	a.status = "compacting"
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go func() {
		if err := a.agent.Compact(ctx); err != nil {
			// Errors are emitted by Compact itself.
			_ = err
		}
		a.post(Event{Kind: "done"})
	}()
}

// ctxLimit returns the effective context limit: user-set (/context, config,
// flag) first, then the provider's advertised hint. 0 means none is known.
func (a *App) ctxLimit() int {
	if a.cfg.ContextTokens > 0 {
		return a.cfg.ContextTokens
	}
	if a.providerCtx != nil {
		return a.providerCtx[a.cfg.BaseURL]
	}
	return 0
}

// applyCtxLimit stores a user-set context limit (0 = auto) and pushes the
// effective value to the agent.
func (a *App) applyCtxLimit(n int) {
	a.cfg.ContextTokens = n
	a.syncCtxLimit()
	msg := "context limit: "
	switch {
	case n > 0:
		msg += formatCount(int64(n)) + " tok"
	default:
		msg += "auto (provider info, none otherwise)"
	}
	if err := a.saveConfig(); err != nil {
		msg += " (not saved: " + err.Error() + ")"
	}
	a.appendBlock(bNotice, "notice", msg, "", false)
}

// syncCtxLimit pushes the effective limit to the agent (trim budget).
func (a *App) syncCtxLimit() {
	if a.agent != nil {
		a.agent.SetContextLimit(a.ctxLimit())
	}
}

// beginCtxModal opens the /context dialog with limit presets.
func (a *App) beginCtxModal() {
	a.choice = &choicePicker{
		title: "Context limit",
		choices: []choiceItem{
			{"auto", "use provider info, none otherwise"},
			{"8192", "8k tokens"},
			{"16384", "16k tokens"},
			{"32768", "32k tokens"},
			{"65536", "64k tokens"},
			{"131072", "128k tokens"},
		},
		apply: func(v string) {
			n, err := strconv.Atoi(v)
			if err != nil {
				return
			}
			a.applyCtxLimit(n)
		},
	}
}

func (a *App) handleSessionsKey(k key) {
	s := a.sessionsM
	switch k.typ {
	case "up", "wheel-up":
		if s.sel > 0 {
			s.sel--
		}
	case "down", "wheel-down":
		if s.sel < len(s.items)-1 {
			s.sel++
		}
	case "enter", "tab":
		n := s.sel + 1
		a.sessionsM = nil
		a.openSession(n)
	case "esc", "ctrl-c":
		a.sessionsM = nil
	case "ctrl-d":
		a.shutdown()
	}
}

func (a *App) submit() {
	text := strings.TrimSpace(string(a.input))
	if text == "" {
		return
	}
	if a.busy {
		a.appendBlock(bNotice, "notice", "still working — Ctrl+C stops the current turn", "", false)
		return
	}
	a.input = nil
	a.cursor = 0
	a.draft = ""
	if len(a.hist) == 0 || a.hist[len(a.hist)-1] != text {
		a.hist = append(a.hist, text)
	}
	a.histPos = len(a.hist)
	a.scroll = 0
	if strings.HasPrefix(text, "/") {
		a.command(text)
		return
	}
	a.runPrompt(text)
}

// runPrompt starts one user turn, showing the prompt in the transcript.
func (a *App) runPrompt(text string) {
	a.turn++
	a.appendBlock(bUser, "you", text, "", false)
	a.busy = true
	a.status = "thinking"
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go a.agent.Run(ctx, text)
}

func (a *App) answerApproval(ok, always bool) {
	if a.approval == nil {
		return
	}
	resp := a.approval.Resp
	a.approval = nil
	select {
	case resp <- ok:
	default:
	}
	if always {
		a.yolo = true
		a.agent.SetAutoApprove(true)
		a.appendBlock(bNotice, "notice", "approvals disabled for this session", "", false)
	}
	a.status = "thinking"
}

func (a *App) command(text string) {
	fields := strings.Fields(text)
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	switch cmd {
	case "/help", "/?":
		a.appendBlock(bNotice, "help", helpText, "", false)
	case "/clear":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before clearing", "", false)
			return
		}
		_ = a.saveSession()
		a.resetSession()
	case "/init":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before initializing", "", false)
			return
		}
		a.runPrompt(initPromptText)
	case "/new":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before starting a new session", "", false)
			return
		}
		if err := a.saveSession(); err != nil {
			a.appendBlock(bNotice, "notice", "could not save session: "+err.Error(), "", false)
		}
		a.resetSession()
		a.appendBlock(bNotice, "notice", "new session started", "", false)
	case "/sessions", "/session":
		a.sessionsCommand(args)
	case "/functions", "/funcs":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before changing functions", "", false)
			return
		}
		if a.functions != nil {
			return
		}
		a.beginFunctions()
	case "/model":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before switching models", "", false)
			return
		}
		if len(args) == 0 {
			a.beginPicker()
			return
		}
		a.model = args[0]
		a.cfg.Model = a.model
		a.agent.SetModel(a.model)
		if err := a.saveConfig(); err != nil {
			a.appendBlock(bNotice, "notice", "model set to "+a.model+" (not saved: "+err.Error()+")", "", false)
		} else {
			a.appendBlock(bNotice, "notice", "model set to "+a.model, "", false)
		}
	case "/tools", "/mode":
		a.beginToolsPicker()
	case "/think":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before changing thinking", "", false)
			return
		}
		if len(args) == 0 {
			a.beginToggleModal("Model thinking", a.think, func(v bool) {
				a.think = v
				a.agent.SetThink(v)
				a.appendBlock(bNotice, "notice", "thinking "+onOff(v), "", false)
			})
			return
		}
		a.think = toggleFlag(a.think, args)
		a.agent.SetThink(a.think)
		a.appendBlock(bNotice, "notice", "thinking "+onOff(a.think), "", false)
	case "/effort":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before changing effort", "", false)
			return
		}
		if len(args) == 0 {
			a.beginEffortPicker()
			return
		}
		a.setEffort(strings.ToLower(args[0]))
	case "/compact":
		a.startCompact()
	case "/context", "/ctx":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before changing the context limit", "", false)
			return
		}
		if len(args) == 0 {
			a.beginCtxModal()
			return
		}
		if strings.ToLower(args[0]) == "auto" {
			a.applyCtxLimit(0)
			return
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 {
			a.appendBlock(bNotice, "notice", "usage: /context auto|<tokens>", "", false)
			return
		}
		a.applyCtxLimit(n)
	case "/yolo":
		if len(args) == 0 {
			a.beginToggleModal("Auto approvals", !a.yolo, func(v bool) {
				a.yolo = !v
				a.agent.SetAutoApprove(a.yolo)
				a.appendBlock(bNotice, "notice", "approvals "+onOff(!a.yolo), "", false)
			})
			return
		}
		a.yolo = toggleFlag(a.yolo, args)
		a.agent.SetAutoApprove(a.yolo)
		a.appendBlock(bNotice, "notice", "approvals "+onOff(!a.yolo), "", false)
	case "/expand":
		a.expanded = !a.expanded
		a.appendBlock(bNotice, "notice", "expanded output "+onOff(a.expanded), "", false)
	case "/reasoning":
		if len(args) == 0 {
			a.beginToggleModal("Show thinking blocks", a.showReasoning, func(v bool) {
				a.showReasoning = v
				a.appendBlock(bNotice, "notice", "reasoning display "+onOff(v), "", false)
			})
			return
		}
		a.showReasoning = toggleFlag(a.showReasoning, args)
		a.appendBlock(bNotice, "notice", "reasoning display "+onOff(a.showReasoning), "", false)
	case "/connect", "/login":
		if a.busy {
			a.appendBlock(bNotice, "notice", "wait for the current turn before connecting", "", false)
			return
		}
		if a.connect != nil {
			return
		}
		a.beginConnect()
	case "/theme":
		if len(args) == 0 {
			a.beginThemeModal()
			return
		}
		a.toggleTheme()
	case "/status":
		target := a.authFile
		if target == "" {
			target = "(unavailable)"
		}
		mdl := a.model
		if mdl == "" {
			mdl = "auto"
		}
		msg := fmt.Sprintf("root   %s\nmodel  %s\nbackend %s\nkey    %s\nconfig %s\ntheme  %s\ncontext %d messages\nusage  %s\napprovals %s",
			shortPath(a.cfg.Root), mdl, a.cfg.BaseURL, maskKey(a.cfg.APIKey), target, themeName(),
			a.agent.Messages(), formatCount(a.tokens), onOff(!a.yolo))
		a.appendBlock(bNotice, "status", msg, "", false)
	case "/quit", "/exit":
		a.shutdown()
	default:
		a.appendBlock(bNotice, "notice", "unknown command "+fields[0]+" — try /help", "", false)
	}
}

// openSession loads and restores the nth most recent saved session.
func (a *App) openSession(n int) {
	if a.busy {
		a.appendBlock(bNotice, "notice", "wait for the current turn before opening a session", "", false)
		return
	}
	sessions, lerr := listSessions()
	if lerr != nil || n < 1 || n > len(sessions) {
		a.appendBlock(bNotice, "notice", "usage: /sessions open <n> (1..10)", "", false)
		return
	}
	s := sessions[n-1]
	if s.ID == a.sessionID {
		a.appendBlock(bNotice, "notice", "already in that session", "", false)
		return
	}
	if err := a.saveSession(); err != nil {
		a.appendBlock(bNotice, "notice", "could not save current session: "+err.Error(), "", false)
	}
	st, err := loadSessionFile(s.Path)
	if err != nil {
		a.appendBlock(bError, "error", "could not load session: "+err.Error(), "", false)
		return
	}
	a.restoreSession(st)
	topic := s.Topic
	if topic == "" {
		topic = s.Preview
	}
	if topic == "" {
		a.appendBlock(bNotice, "notice", "session opened: "+s.ID, "", false)
	} else {
		a.appendBlock(bNotice, "notice", "session opened: "+topic, "", false)
	}
}

// sessionsCommand lists saved sessions, or opens one with "open <n>".
func (a *App) sessionsCommand(args []string) {
	if len(args) >= 2 && strings.ToLower(args[0]) == "open" {
		n, err := strconv.Atoi(args[1])
		if err != nil {
			a.appendBlock(bNotice, "notice", "usage: /sessions open <n> (1..10)", "", false)
			return
		}
		a.openSession(n)
		return
	}
	a.beginSessions()
}

func (a *App) restoreSession(s *storedSession) {
	a.agent.Restore(s.Messages)
	a.blocks = nil
	for _, b := range s.Blocks {
		a.blocks = append(a.blocks, &Block{kind: kindFromName(b.Kind), label: b.Label, text: b.Text, meta: b.Meta})
	}
	if s.Model != "" {
		a.model = s.Model
		a.cfg.Model = s.Model
		a.agent.SetModel(s.Model)
	}
	a.sessionID = s.ID
	a.sessStart = s.Started
	a.scroll = 0
}

func toggleFlag(cur bool, args []string) bool {
	if len(args) == 0 {
		return !cur
	}
	switch strings.ToLower(args[0]) {
	case "on", "1", "true", "yes":
		return true
	case "off", "0", "false", "no":
		return false
	}
	return !cur
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

const helpText = `Commands
  /help              this help
  /clear             clear transcript and model context
  /new               start a new session (saves the current one)
  /sessions          saved sessions with topics (open <n> works too)
  /init              write a NULLCODE.md project context
  /functions         toggle tool packs (todos, MCPs, skills…)
  /model [id]        show or switch model (grouped per provider)
  /tools             switch tool calling mode (auto/protocol/native)
  /think on|off      enable or disable model thinking
  /effort            reasoning effort (default/minimal/low/medium/high)
  /compact           summarize and replace history to free context
  /context auto|n    set the context limit (auto = provider info)
  /yolo on|off       skip exec/write/patch approvals
  /expand            show full tool output and reasoning
  /reasoning         show or hide thinking blocks
  /connect           connect a provider (token, URL, model); adds to the saved list
  /theme             switch light/dark theme
  /status            backend and session details
  /quit              leave NullCode

Type / for the command palette; arrows pick, Enter runs. Commands with
settings open their own dialog. Mouse wheel and PgUp/PgDn scroll the chat.

Tools (assistant output only)
  ⟦exec⟧ command
  ⟦read⟧ path
  ⟦list⟧ dir
  ⟦search⟧ pattern [path]
  ⟦write⟧ path …⟦end⟧
  ⟦patch⟧ path …⟦end⟧
  ⟦todo⟧ add|done|undo|list|clear …
  ⟦skill⟧ name
  ⟦mcp⟧ [server [tool [json]]]
  ⟦ask⟧ question
  ⟦done⟧

Keys
  Enter send · ↑↓ history · PgUp/PgDn or mouse wheel scroll
  Ctrl+O expand · Ctrl+C stop · Ctrl+D quit`

const initPromptText = `Initialize this project for future NullCode sessions.
Inspect the repository: layout, manifests, build/test/lint commands, and code conventions.
Then write a concise NULLCODE.md at the project root covering: project overview, build and test commands, key directories, and conventions to follow. Keep it under 60 lines.`
