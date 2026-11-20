package nullcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nullabby/nullcode/internal/upstream"
)

// Config is the NullCode runtime configuration.
const maxEditBytes = 8 << 20

// Config controls the agent and backend connection.
type Config struct {
	BaseURL         string
	APIKey          string
	Model           string
	Temperature     float64
	TopP            float64
	MaxTokens       int
	MaxSteps        int
	Timeout         time.Duration
	Think           bool
	AutoApprove     bool
	Root            string
	ContextTokens   int
	MaxResultChars  int
	Features        []string // enabled tool packs; nil means defaults
	ToolMode        string   // "auto" (default), "protocol", or "native"
	ReasoningEffort string   // "" (default), "minimal", "low", "medium", "high"
	Version         string
}

// Event is emitted by the agent to the TUI.
type Event struct {
	Kind     string // status, reasoning, content, action, body, output, action_done, notice, error, usage, models, ask, done
	Text     string
	Action   Action
	Meta     string
	Usage    int64
	Provider string // backend URL a model list belongs to
	CtxHint  int    // context window hint advertised by the provider
	Models   []string
	Approval *Approval
	Ask      *AskPrompt
}

// Approval asks the TUI to allow a mutating tool call.
type Approval struct {
	Kind   string
	Detail string
	Resp   chan bool
}

// AskPrompt asks the TUI for a free-text answer from the user.
type AskPrompt struct {
	Question string
	Resp     chan string
}

// ActionResult is one tool result fed back to the model.
type ActionResult struct {
	Tool    string
	Arg     string
	Status  string
	Body    string
	Added   int
	Removed int
	Emitted bool // Body was already streamed to the UI
}

// native renders a tool result as plain text for native tool-calling models.
func (r ActionResult) native() string {
	head := r.Tool + ": " + r.Status
	if r.Arg != "" {
		head += "\narg: " + r.Arg
	}
	if r.Body != "" {
		return head + "\n" + r.Body
	}
	return head
}

func (r ActionResult) envelope() string {
	head := glyphOpen + "result" + glyphClose + " " + r.Tool
	if r.Status != "" {
		head += " " + r.Status
	}
	var b strings.Builder
	b.WriteString(head)
	if r.Arg != "" {
		b.WriteString("\n")
		switch {
		case r.Tool == "exec":
			b.WriteString("command: ")
		case r.Tool == "read" || r.Tool == "write" || r.Tool == "patch" || r.Tool == "list":
			b.WriteString("path: ")
		default:
			b.WriteString("input: ")
		}
		b.WriteString(r.Arg)
	}
	if r.Body != "" {
		b.WriteString("\n")
		b.WriteString(r.Body)
	}
	b.WriteString("\n")
	b.WriteString(endMarker)
	b.WriteString("\n")
	return b.String()
}

// Agent owns the conversation and executes NullCode actions.
type Agent struct {
	cfg  Config
	up   *upstream.Upstream
	emit func(Event)

	messages []upstream.Message
	mu       sync.Mutex
	auto     atomic.Bool
	shell    string
	shellArg func(string) []string
	features map[string]bool
}

// NewAgent constructs an agent bound to an OpenAI-compatible endpoint.
func NewAgent(cfg Config, emit func(Event)) *Agent {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 32
	}
	if cfg.MaxResultChars <= 0 {
		cfg.MaxResultChars = 24000
	}
	// ContextTokens 0 means "no limit known": history trimming is disabled
	// until the provider, the user, or /context supplies one.
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	if emit == nil {
		emit = func(Event) {}
	}
	if cfg.Root == "" {
		cfg.Root = "."
	}
	if abs, err := filepath.Abs(cfg.Root); err == nil {
		cfg.Root = abs
	}
	if real, err := filepath.EvalSymlinks(cfg.Root); err == nil {
		cfg.Root = real
	}
	a := &Agent{
		cfg:   cfg,
		up:    upstream.New(cfg.BaseURL, cfg.APIKey),
		emit:  emit,
		shell: "sh",
	}
	a.shellArg = func(cmd string) []string { return []string{"-c", cmd} }
	if runtime.GOOS == "windows" {
		a.shell = "powershell"
		a.shellArg = func(cmd string) []string {
			prelude := "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; $OutputEncoding=[System.Text.Encoding]::UTF8; $ProgressPreference='SilentlyContinue'; "
			return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", prelude + "& {\n" + cmd + "\n}\nexit $LASTEXITCODE"}
		}
	}
	a.auto.Store(cfg.AutoApprove)
	a.features = normalizeFeatures(cfg.Features)
	return a
}

// Reset clears the conversation.
func (a *Agent) Reset() {
	a.mu.Lock()
	a.messages = nil
	a.mu.Unlock()
}

// SetModel changes the model used for subsequent requests.
func (a *Agent) SetModel(m string) { a.cfg.Model = m }

// SetEndpoint switches the backend client used for subsequent requests.
func (a *Agent) SetEndpoint(base, key string) {
	a.cfg.BaseURL = base
	a.cfg.APIKey = key
	a.up = upstream.New(base, key)
}

// SetThink toggles the chat template's enable_thinking flag.
func (a *Agent) SetThink(v bool) { a.cfg.Think = v }

// SetEffort changes the reasoning_effort sent to backends that support it.
func (a *Agent) SetEffort(v string) { a.cfg.ReasoningEffort = v }

// SystemPrompt returns the NullCode system prompt for the given config and
// enabled tool packs — used by nullcodistill to match the agent's prompt.
func SystemPrompt(cfg Config, features []string) string {
	a := NewAgent(cfg, nil)
	a.features = normalizeFeatures(features)
	return a.systemPrompt()
}

// SetContextLimit changes the context budget used for history trimming.
// 0 disables trimming (no limit known).
func (a *Agent) SetContextLimit(n int) { a.cfg.ContextTokens = n }

// SetAutoApprove toggles approval prompts.
func (a *Agent) SetAutoApprove(v bool) { a.auto.Store(v) }

// SetFeatures replaces the enabled tool packs.
func (a *Agent) SetFeatures(list []string) { a.features = normalizeFeatures(list) }

// Snapshot returns a copy of the conversation for session persistence.
func (a *Agent) Snapshot() []upstream.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]upstream.Message(nil), a.messages...)
}

// Restore replaces the conversation with a saved session's messages.
func (a *Agent) Restore(msgs []upstream.Message) {
	a.mu.Lock()
	a.messages = msgs
	a.mu.Unlock()
}

// ContextSize approximates the conversation size in tokens (chars/4).
func (a *Agent) ContextSize() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, m := range a.messages {
		n += len(m.Content) + 16
	}
	return n / 4
}

// Compact asks the model to summarize the conversation and replaces the
// history with the summary, freeing context window space.
func (a *Agent) Compact(ctx context.Context) error {
	a.mu.Lock()
	if len(a.messages) < 3 {
		a.mu.Unlock()
		a.emit(Event{Kind: "notice", Text: "nothing to compact yet"})
		return nil
	}
	sys := a.messages[0].Content
	var b strings.Builder
	for _, m := range a.messages[1:] {
		b.WriteString(strings.ToUpper(m.Role[:1]) + m.Role[1:] + ": ")
		b.WriteString(truncateMiddle(m.Content, 2500))
		b.WriteString("\n\n")
	}
	transcript := b.String()
	a.mu.Unlock()

	a.emit(Event{Kind: "status", Text: "compacting"})
	var summary strings.Builder
	_, _, err := a.up.Stream(ctx, upstream.Options{
		Model:     a.cfg.Model,
		MaxTokens: 700,
		Messages: []upstream.Message{
			{Role: "system", Content: "You compress coding-agent conversations into terse context summaries. Output only the summary."},
			{Role: "user", Content: transcript + "\nSummarize the conversation above: decisions made, files changed, current task state, next steps. Keep it under 300 words."},
		},
	}, func(kind, text string) {
		if kind == "content" {
			summary.WriteString(text)
		}
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			a.emit(Event{Kind: "notice", Text: "compact stopped"})
		} else {
			a.emit(Event{Kind: "error", Text: "compact failed: " + err.Error()})
		}
		return err
	}
	out := strings.TrimSpace(summary.String())
	if out == "" {
		a.emit(Event{Kind: "notice", Text: "compact produced an empty summary — history kept"})
		return nil
	}
	a.mu.Lock()
	a.messages = []upstream.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: "Summary of the earlier conversation:\n" + out},
	}
	a.mu.Unlock()
	a.emit(Event{Kind: "notice", Text: "context compacted — history replaced with a summary"})
	return nil
}

// Probe checks the backend and returns its advertised model IDs and any
// context window hint the provider included.
func (a *Agent) Probe(ctx context.Context) ([]string, int, error) {
	return a.up.Models(ctx)
}

// Messages exposes a copy of the current conversation size.
func (a *Agent) Messages() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.messages)
}

// frontierModels are model families that follow native OpenAI tool calling
// reliably; used by the "auto" tool mode heuristic.
var frontierModels = []string{
	"deepseek", "claude", "gpt", "o1", "o3", "o4", "gemini",
	"kimi", "glm", "grok", "mistral",
}

// nativeTools decides whether to use native OpenAI tool calling instead of
// the NullCode glyph protocol. "protocol" and "native" force a mode;
// "auto" (default) picks native only for known frontier families.
func (a *Agent) nativeTools() bool {
	switch strings.ToLower(strings.TrimSpace(a.cfg.ToolMode)) {
	case "native":
		return true
	case "protocol":
		return false
	}
	m := strings.ToLower(a.cfg.Model)
	for _, kw := range frontierModels {
		if strings.Contains(m, kw) {
			return true
		}
	}
	return false
}

func toolObj(props map[string]any, req []string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": req}
}

// toolDefs builds the native function-calling definitions for enabled packs.
func (a *Agent) toolDefs() []upstream.ToolDef {
	str := map[string]any{"type": "string"}
	var defs []upstream.ToolDef
	add := func(name, desc string, params map[string]any) {
		defs = append(defs, upstream.ToolDef{
			Type: "function",
			Function: upstream.ToolFunc{
				Name: name, Description: desc, Parameters: params,
			},
		})
	}
	if a.features["exec"] {
		add("exec", "Run a shell command in the project root and return its combined stdout/stderr.", toolObj(map[string]any{"command": str}, []string{"command"}))
	}
	if a.features["read"] {
		add("read", "Read a text file relative to the project root.", toolObj(map[string]any{"path": str}, []string{"path"}))
	}
	if a.features["list"] {
		add("list", "List the entries of a directory relative to the project root.", toolObj(map[string]any{"dir": str}, nil))
	}
	if a.features["search"] {
		add("search", "Search file contents under the project root with a regular expression.", toolObj(map[string]any{"pattern": str, "path": str}, []string{"pattern"}))
	}
	if a.features["write"] {
		add("write", "Create or overwrite a file with the complete content.", toolObj(map[string]any{"path": str, "content": str}, []string{"path", "content"}))
	}
	if a.features["patch"] {
		add("patch", "Replace an exact old text with new text inside a file.", toolObj(map[string]any{"path": str, "old": str, "new": str}, []string{"path", "old", "new"}))
	}
	if a.features["todos"] {
		add("todo", "Manage the project TODO.md task list.", toolObj(map[string]any{
			"op":   map[string]any{"type": "string", "enum": []string{"add", "done", "undo", "list", "clear"}},
			"text": str,
		}, []string{"op"}))
	}
	if a.features["skills"] {
		add("skill", "Load instructions from an installed skill pack by name.", toolObj(map[string]any{"name": str}, []string{"name"}))
	}
	if a.features["mcps"] {
		add("mcp", "Call a tool on a configured MCP server, or list servers/tools.", toolObj(map[string]any{"server": str, "tool": str, "args": str}, nil))
	}
	if a.features["ask"] {
		add("ask", "Ask the user a question and wait for their typed answer.", toolObj(map[string]any{"question": str}, []string{"question"}))
	}
	return defs
}

// nativeCallAction maps a native tool call to a NullCode action.
func nativeCallAction(name, argsJSON string) Action {
	args := map[string]any{}
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &args)
	}
	get := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "exec":
		return Action{Kind: "exec", Arg: get("command")}
	case "read":
		return Action{Kind: "read", Arg: get("path")}
	case "list":
		return Action{Kind: "list", Arg: get("dir")}
	case "search":
		return Action{Kind: "search", Arg: `"` + get("pattern") + `" ` + get("path")}
	case "write":
		return Action{Kind: "write", Arg: get("path"), Body: get("content")}
	case "patch":
		return Action{Kind: "patch", Arg: get("path"), Body: "<<<\n" + get("old") + "\n>>>\n" + get("new")}
	case "todo":
		return Action{Kind: "todo", Arg: strings.TrimSpace(get("op") + " " + get("text"))}
	case "skill":
		return Action{Kind: "skill", Arg: get("name")}
	case "mcp":
		return Action{Kind: "mcp", Arg: strings.TrimSpace(get("server") + " " + get("tool") + " " + get("args"))}
	case "ask":
		return Action{Kind: "ask", Arg: get("question")}
	}
	return Action{Kind: "unknown", Arg: name}
}

// Run processes one user prompt, executing actions until the model stops,
// emits ⟦done⟧, or the step budget runs out. It always emits a final "done".
func (a *Agent) Run(ctx context.Context, prompt string) {
	defer a.emit(Event{Kind: "done"})
	a.emit(Event{Kind: "status", Text: "thinking"})
	a.mu.Lock()
	if len(a.messages) == 0 {
		a.messages = append(a.messages, upstream.Message{Role: "system", Content: a.systemPrompt()})
	}
	a.messages = append(a.messages, upstream.Message{Role: "user", Content: prompt})
	a.mu.Unlock()

	if a.nativeTools() {
		a.runNative(ctx)
		return
	}
	a.runProtocol(ctx)
}

// runNative runs the agent loop using native OpenAI tool calling.
func (a *Agent) runNative(ctx context.Context) {
	for step := 0; step < a.cfg.MaxSteps; step++ {
		if ctx.Err() != nil {
			return
		}
		if step > 0 {
			a.emit(Event{Kind: "status", Text: fmt.Sprintf("step %d/%d", step+1, a.cfg.MaxSteps)})
		}
		a.trim()
		a.mu.Lock()
		msgs := append([]upstream.Message(nil), a.messages...)
		a.mu.Unlock()

		var content strings.Builder
		usage, calls, err := a.up.Stream(ctx, upstream.Options{
			Model:           a.cfg.Model,
			Messages:        msgs,
			Temperature:     a.cfg.Temperature,
			TopP:            a.cfg.TopP,
			MaxTokens:       a.cfg.MaxTokens,
			ReasoningEffort: a.cfg.ReasoningEffort,
			Tools:           a.toolDefs(),
		}, func(kind, text string) {
			if kind == "reasoning" {
				a.emit(Event{Kind: "reasoning", Text: text})
				return
			}
			content.WriteString(text)
			a.emit(Event{Kind: "content", Text: text})
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				a.emit(Event{Kind: "notice", Text: "stopped"})
			} else {
				a.emit(Event{Kind: "error", Text: err.Error()})
			}
			return
		}
		if ctx.Err() != nil {
			a.emit(Event{Kind: "notice", Text: "stopped"})
			return
		}
		if usage.TotalTokens > 0 {
			a.emit(Event{Kind: "usage", Usage: usage.TotalTokens})
		}

		if len(calls) == 0 {
			a.mu.Lock()
			a.messages = append(a.messages, upstream.Message{Role: "assistant", Content: content.String()})
			a.mu.Unlock()
			return
		}

		um := upstream.Message{Role: "assistant", Content: content.String()}
		for _, c := range calls {
			um.ToolCalls = append(um.ToolCalls, c)
		}
		a.mu.Lock()
		a.messages = append(a.messages, um)
		a.mu.Unlock()

		denied := false
		for _, c := range calls {
			var res ActionResult
			if denied {
				res = ActionResult{Tool: c.Function.Name, Status: "skipped",
					Body: "skipped: the previous tool was denied by the user"}
				a.emit(Event{Kind: "action_done", Action: Action{Kind: res.Tool}, Meta: res.Status})
			} else {
				act := nativeCallAction(c.Function.Name, c.Function.Arguments)
				a.emit(Event{Kind: "action", Action: act})
				res = a.runAction(ctx, act)
				if res.Body != "" && !res.Emitted {
					a.emit(Event{Kind: "output", Text: res.Body + "\n"})
				}
				a.emit(Event{Kind: "action_done", Action: Action{Kind: res.Tool, Arg: res.Arg}, Meta: res.Status})
				if res.Status == "denied" {
					denied = true
				}
			}
			a.mu.Lock()
			a.messages = append(a.messages, upstream.Message{
				Role: "tool", ToolCallID: c.ID, Name: c.Function.Name, Content: res.native(),
			})
			a.mu.Unlock()
		}
	}
	a.emit(Event{Kind: "error", Text: fmt.Sprintf("step budget exhausted (%d); send another message to continue", a.cfg.MaxSteps)})
}

// runProtocol runs the agent loop using the NullCode glyph protocol.
func (a *Agent) runProtocol(ctx context.Context) {
	for step := 0; step < a.cfg.MaxSteps; step++ {
		if ctx.Err() != nil {
			return
		}
		if step > 0 {
			a.emit(Event{Kind: "status", Text: fmt.Sprintf("step %d/%d", step+1, a.cfg.MaxSteps)})
		}
		a.trim()
		a.mu.Lock()
		msgs := append([]upstream.Message(nil), a.messages...)
		a.mu.Unlock()

		var filter Filter
		splitter := &ThinkSplitter{}
		hooks := filterHooks{
			prose: func(s string) { a.emit(Event{Kind: "content", Text: s}) },
			action: func(act Action) {
				a.emit(Event{Kind: "action", Action: act})
				if act.Kind == "write" || act.Kind == "patch" {
					a.emit(Event{Kind: "status", Text: "receiving " + act.Kind + " body"})
				}
			},
			body: func(act Action, lines int) {
				a.emit(Event{Kind: "body", Action: act, Meta: fmt.Sprintf("%d lines", lines)})
			},
			note: func(s string) { a.emit(Event{Kind: "notice", Text: s}) },
		}
		onText := func(chunk string) {
			splitter.Feed(chunk, func(kind, text string) {
				if kind == "reasoning" {
					a.emit(Event{Kind: "reasoning", Text: text})
					return
				}
				filter.Feed(text, hooks)
			})
		}

		extras := map[string]any{}
		if !a.cfg.Think {
			extras["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
		}
		usage, _, err := a.up.Stream(ctx, upstream.Options{
			Model:           a.cfg.Model,
			Messages:        msgs,
			Temperature:     a.cfg.Temperature,
			TopP:            a.cfg.TopP,
			MaxTokens:       a.cfg.MaxTokens,
			ReasoningEffort: a.cfg.ReasoningEffort,
			Extras:          extras,
		}, func(kind, text string) {
			if kind == "reasoning" {
				a.emit(Event{Kind: "reasoning", Text: text})
				return
			}
			onText(text)
		})
		splitter.Flush(func(kind, text string) {
			if kind == "reasoning" {
				a.emit(Event{Kind: "reasoning", Text: text})
				return
			}
			filter.Feed(text, hooks)
		})
		actions := filter.Finish(hooks)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				a.emit(Event{Kind: "notice", Text: "stopped"})
			} else {
				a.emit(Event{Kind: "error", Text: err.Error()})
			}
			return
		}
		if ctx.Err() != nil {
			a.emit(Event{Kind: "notice", Text: "stopped"})
			return
		}
		if usage.TotalTokens > 0 {
			a.emit(Event{Kind: "usage", Usage: usage.TotalTokens})
		}

		a.mu.Lock()
		a.messages = append(a.messages, upstream.Message{
			Role:    "assistant",
			Content: assistantHistory(filter.Prose(), actions),
		})
		a.mu.Unlock()

		if len(actions) == 0 {
			return
		}
		finished := false
		var results strings.Builder
		for _, act := range actions {
			if ctx.Err() != nil {
				return
			}
			if act.Kind == "done" {
				finished = true
				break
			}
			res := a.runAction(ctx, act)
			if res.Body != "" && !res.Emitted {
				a.emit(Event{Kind: "output", Text: res.Body + "\n"})
			}
			a.emit(Event{Kind: "action_done", Action: Action{Kind: res.Tool, Arg: res.Arg}, Meta: res.Status})
			results.WriteString(res.envelope())
			results.WriteString("\n")
			if res.Status == "denied" {
				break
			}
			if ctx.Err() != nil {
				return
			}
		}
		if results.Len() == 0 {
			return
		}
		msg := results.String()
		msg = truncateMiddle(msg, a.cfg.MaxResultChars)
		a.mu.Lock()
		a.messages = append(a.messages, upstream.Message{Role: "user", Content: msg})
		a.mu.Unlock()
		if finished {
			return
		}
	}
	a.emit(Event{Kind: "error", Text: fmt.Sprintf("step budget exhausted (%d); send another message to continue", a.cfg.MaxSteps)})
}

func assistantHistory(prose string, actions []Action) string {
	prose = strings.TrimSpace(prose)
	lines := make([]string, 0, len(actions)+1)
	if prose != "" {
		lines = append(lines, prose)
	}
	for _, act := range actions {
		if act.Kind == "done" {
			continue
		}
		arg := act.Arg
		if act.Kind == "write" || act.Kind == "patch" || act.Kind == "read" {
			arg = unquotePath(arg)
		}
		lines = append(lines, glyphOpen+act.Kind+glyphClose+" "+arg)
		// Record write/patch bodies with their closing marker so the model
		// sees complete examples of its own block syntax in context.
		if act.Kind == "write" || act.Kind == "patch" {
			body := strings.TrimRight(act.Body, "\n")
			if body != "" {
				lines = append(lines, body)
			}
			lines = append(lines, endMarker)
		}
	}
	return strings.Join(lines, "\n")
}

func (a *Agent) approve(ctx context.Context, kind, detail string) bool {
	if a.cfg.AutoApprove {
		return true
	}
	ap := &Approval{Kind: kind, Detail: detail, Resp: make(chan bool, 1)}
	select {
	case <-ctx.Done():
		return false
	default:
	}
	a.emit(Event{Kind: "status", Text: "waiting for approval"})
	a.emit(Event{Kind: "approval", Approval: ap})
	select {
	case ok := <-ap.Resp:
		return ok
	case <-ctx.Done():
		return false
	}
}

// askUser shows a free-text question in the TUI and waits for the answer.
func (a *Agent) askUser(ctx context.Context, question string) string {
	if a.cfg.AutoApprove {
		return ""
	}
	ap := &AskPrompt{Question: question, Resp: make(chan string, 1)}
	select {
	case <-ctx.Done():
		return ""
	default:
	}
	a.emit(Event{Kind: "status", Text: "waiting for your answer"})
	a.emit(Event{Kind: "ask", Ask: ap})
	select {
	case ans := <-ap.Resp:
		return ans
	case <-ctx.Done():
		return ""
	}
}

func (a *Agent) runAction(ctx context.Context, act Action) ActionResult {
	if act.Incomplete {
		return ActionResult{Tool: act.Kind, Arg: act.Arg, Status: "error", Body: "action body was truncated; nothing was written"}
	}
	if act.Kind != "done" && !a.features[act.Kind] {
		return ActionResult{Tool: act.Kind, Arg: act.Arg, Status: "error",
			Body: "tool '" + act.Kind + "' is disabled; enable it with /functions"}
	}
	switch act.Kind {
	case "exec":
		if act.Arg == "" {
			return ActionResult{Tool: "exec", Status: "error", Body: "empty command"}
		}
		if !a.approve(ctx, "exec", act.Arg) {
			return ActionResult{Tool: "exec", Arg: act.Arg, Status: "denied", Body: "the user denied this command"}
		}
		a.emit(Event{Kind: "status", Text: "running"})
		return a.exec(ctx, act.Arg)
	case "read":
		return a.read(act.Arg)
	case "write":
		if act.Arg == "" {
			return ActionResult{Tool: "write", Status: "error", Body: "empty path"}
		}
		detail := fmt.Sprintf("%s (%d lines, %d bytes)", unquotePath(act.Arg), strings.Count(act.Body, "\n"), len(act.Body))
		if !a.approve(ctx, "write", detail) {
			return ActionResult{Tool: "write", Arg: act.Arg, Status: "denied", Body: "the user denied this write"}
		}
		return a.write(act.Arg, act.Body)
	case "patch":
		if act.Arg == "" {
			return ActionResult{Tool: "patch", Status: "error", Body: "empty path"}
		}
		detail := fmt.Sprintf("%s (%d lines)", unquotePath(act.Arg), strings.Count(act.Body, "\n"))
		if !a.approve(ctx, "patch", detail) {
			return ActionResult{Tool: "patch", Arg: act.Arg, Status: "denied", Body: "the user denied this patch"}
		}
		return a.patch(act.Arg, act.Body)
	case "list":
		return a.list(act.Arg)
	case "search":
		return a.search(act.Arg)
	case "todo":
		return a.todo(act.Arg)
	case "skill":
		return a.skill(act.Arg)
	case "mcp":
		return a.mcp(act.Arg)
	case "ask":
		if act.Arg == "" {
			return ActionResult{Tool: "ask", Status: "error", Body: "empty question"}
		}
		ans := a.askUser(ctx, act.Arg)
		if ans == "" {
			return ActionResult{Tool: "ask", Arg: act.Arg, Status: "ok", Body: "the user did not provide an answer"}
		}
		return ActionResult{Tool: "ask", Arg: act.Arg, Status: "ok", Body: "the user answered: " + ans}
	default:
		return ActionResult{Tool: "unknown", Arg: act.Arg, Status: "error", Body: "unknown tool " + act.Arg + "; use exec, read, write, patch, or done"}
	}
}

var (
	reCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	reOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
)

func sanitizeText(s string) string {
	s = reCSI.ReplaceAllString(s, "")
	s = reOSC.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 32 || r == 0x7f:
			return -1
		case r >= 0x80 && r < 0xa0:
			return -1
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			return -1
		default:
			return r
		}
	}, s)
	return s
}

type captureWriter struct {
	mu           sync.Mutex
	limit        int
	displayLimit int
	emitted      int
	buf          bytes.Buffer
	total        int
	onOutput     func(string)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	w.total += n
	if w.limit <= 0 || w.buf.Len() < w.limit {
		remain := w.limit - w.buf.Len()
		if w.limit <= 0 || remain > len(p) {
			remain = len(p)
		}
		w.buf.Write(p[:remain])
	}
	if w.onOutput != nil && (w.displayLimit <= 0 || w.emitted < w.displayLimit) {
		out := p
		if w.displayLimit > 0 {
			rem := w.displayLimit - w.emitted
			if rem < len(out) {
				out = out[:rem]
			}
		}
		w.emitted += len(out)
		w.onOutput(sanitizeText(string(out)))
		if w.displayLimit > 0 && w.emitted >= w.displayLimit {
			w.onOutput("\n…[output truncated for display]…\n")
		}
	}
	return n, nil
}

func (a *Agent) exec(parent context.Context, cmdline string) ActionResult {
	ctx, cancel := context.WithTimeout(parent, a.cfg.Timeout)
	defer cancel()
	limit := a.cfg.MaxResultChars
	w := &captureWriter{limit: limit, displayLimit: limit, onOutput: func(s string) {
		a.emit(Event{Kind: "output", Text: s})
	}}
	cmd := exec.CommandContext(ctx, a.shell, a.shellArg(cmdline)...)
	cmd.Dir = a.cfg.Root
	env := []string{"NO_COLOR=1", "TERM=dumb", "CLICOLOR=0", "GIT_TERMINAL_PROMPT=0"}
	if runtime.GOOS == "windows" {
		env = append(env, "PAGER=", "GIT_PAGER=")
	} else {
		env = append(env, "PAGER=cat", "GIT_PAGER=cat")
	}
	cmd.Env = append(os.Environ(), env...)
	prepareCmd(cmd)
	cmd.Cancel = func() error { return killCmd(cmd) }
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()

	status := "exit=0"
	exitCode := 0
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = "timeout"
		} else if errors.Is(parent.Err(), context.Canceled) {
			status = "canceled"
		} else {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				exitCode = ee.ExitCode()
				status = fmt.Sprintf("exit=%d", exitCode)
			} else {
				status = "error"
			}
		}
	}
	body := w.buf.String()
	if w.total > len(body) {
		body += fmt.Sprintf("\n…[output truncated: %d of %d bytes shown]…", len(body), w.total)
	}
	if strings.TrimSpace(body) == "" {
		body = "(no output)"
	}
	if status == "error" && err != nil {
		body = err.Error() + "\n" + body
		a.emit(Event{Kind: "output", Text: err.Error() + "\n"})
	}
	if w.total == 0 {
		a.emit(Event{Kind: "output", Text: "(no output)\n"})
	}
	return ActionResult{Tool: "exec", Arg: cmdline, Status: status, Body: strings.TrimRight(body, "\n"), Emitted: true}
}

func (a *Agent) resolve(p string) (string, error) {
	p = unquotePath(p)
	if p == "" {
		return "", errors.New("empty path")
	}
	root := a.cfg.Root
	if !filepath.IsAbs(root) {
		r, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		root = r
	}
	root = filepath.Clean(root)
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if !inside(root, p) {
		return "", fmt.Errorf("path escapes project root: %s", p)
	}
	// Follow symlinks for the path or its nearest existing parent.
	if real, err := filepath.EvalSymlinks(p); err == nil {
		if !inside(root, real) {
			return "", fmt.Errorf("path escapes project root through a symlink: %s", p)
		}
	} else if real, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		if !inside(root, filepath.Join(real, filepath.Base(p))) {
			return "", fmt.Errorf("path escapes project root through a symlink: %s", p)
		}
	}
	return p, nil
}

func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (a *Agent) read(arg string) ActionResult {
	path, err := a.resolve(arg)
	if err != nil {
		return ActionResult{Tool: "read", Arg: arg, Status: "error", Body: err.Error()}
	}
	st, err := os.Stat(path)
	if err != nil {
		return ActionResult{Tool: "read", Arg: arg, Status: "error", Body: err.Error()}
	}
	if st.IsDir() {
		return ActionResult{Tool: "read", Arg: arg, Status: "error", Body: "is a directory; use ⟦exec⟧ to list files"}
	}
	max := a.cfg.MaxResultChars
	f, err := os.Open(path)
	if err != nil {
		return ActionResult{Tool: "read", Arg: arg, Status: "error", Body: err.Error()}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return ActionResult{Tool: "read", Arg: arg, Status: "error", Body: err.Error()}
	}
	truncated := len(data) > max
	if truncated {
		data = data[:max]
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return ActionResult{Tool: "read", Arg: arg, Status: "ok", Body: fmt.Sprintf("binary file (%d bytes)", st.Size())}
	}
	body := sanitizeText(string(data))
	if truncated {
		body += fmt.Sprintf("\n…[truncated: %d of %d bytes shown]…", max, st.Size())
	}
	a.emit(Event{Kind: "output", Text: body + "\n"})
	return ActionResult{Tool: "read", Arg: arg, Status: "ok", Body: body, Emitted: true}
}

func (a *Agent) write(arg, body string) ActionResult {
	path, err := a.resolve(arg)
	if err != nil {
		return ActionResult{Tool: "write", Arg: arg, Status: "error", Body: err.Error()}
	}
	var old string
	mode := os.FileMode(0o644)
	if prev, err := os.ReadFile(path); err == nil && len(prev) <= maxEditBytes {
		old = string(prev)
	}
	if st, err := os.Stat(path); err == nil && st.Mode().Perm() != 0 {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ActionResult{Tool: "write", Arg: arg, Status: "error", Body: err.Error()}
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		return ActionResult{Tool: "write", Arg: arg, Status: "error", Body: err.Error()}
	}
	add, rem := lineDelta(old, body)
	rel, _ := filepath.Rel(a.cfg.Root, path)
	if rel == "" {
		rel = arg
	}
	summary := fmt.Sprintf("wrote %s (%d bytes, +%d -%d lines)", rel, len(body), add, rem)
	if d := diffFor(old, body, 40); d != "" {
		a.emit(Event{Kind: "diff", Text: d, Meta: summary})
	} else {
		a.emit(Event{Kind: "output", Text: summary + "\n"})
	}
	return ActionResult{Tool: "write", Arg: rel, Status: "ok", Body: summary, Added: add, Removed: rem, Emitted: true}
}

func (a *Agent) patch(arg, body string) ActionResult {
	path, err := a.resolve(arg)
	if err != nil {
		return ActionResult{Tool: "patch", Arg: arg, Status: "error", Body: err.Error()}
	}
	oldBlock, newBlock, err := parsePatchBody(body)
	if err != nil {
		return ActionResult{Tool: "patch", Arg: arg, Status: "error", Body: err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ActionResult{Tool: "patch", Arg: arg, Status: "error", Body: err.Error()}
	}
	if len(data) > maxEditBytes {
		return ActionResult{Tool: "patch", Arg: arg, Status: "error", Body: "file is too large to patch; use a script with exec"}
	}
	content := string(data)
	candidates := []struct{ old, new string }{
		{oldBlock, newBlock},
		{oldBlock + "\n", strings.TrimSuffix(newBlock, "\n") + "\n"},
		{strings.TrimSuffix(oldBlock, "\n"), strings.TrimSuffix(newBlock, "\n")},
	}
	replaced := false
	count := 0
	var usedOld, usedNew string
	for _, c := range candidates {
		if c.old == "" {
			continue
		}
		if n := strings.Count(content, c.old); n > 0 {
			count = n
			content = strings.Replace(content, c.old, c.new, 1)
			usedOld, usedNew = c.old, c.new
			replaced = true
			break
		}
	}
	if !replaced {
		hint := strings.TrimSpace(oldBlock)
		if len(hint) > 120 {
			hint = hint[:120] + "…"
		}
		return ActionResult{Tool: "patch", Arg: arg, Status: "error", Body: "old text not found. Re-read the file and try ⟦write⟧ with the complete file. Search text began: " + hint}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ActionResult{Tool: "patch", Arg: arg, Status: "error", Body: err.Error()}
	}
	add, rem := lineDelta(usedOld, usedNew)
	diffOld, diffNew := usedOld, usedNew
	if count > 1 {
		add, rem = lineDelta(oldBlock, newBlock)
		diffOld, diffNew = oldBlock, newBlock
	}
	rel, _ := filepath.Rel(a.cfg.Root, path)
	summary := fmt.Sprintf("patched %s (+%d -%d lines)", rel, add, rem)
	if count > 1 {
		summary += fmt.Sprintf(" (first of %d matches)", count)
	}
	if d := diffFor(diffOld, diffNew, 40); d != "" {
		a.emit(Event{Kind: "diff", Text: d, Meta: summary})
	} else {
		a.emit(Event{Kind: "output", Text: summary + "\n"})
	}
	return ActionResult{Tool: "patch", Arg: rel, Status: "ok", Body: summary, Added: add, Removed: rem, Emitted: true}
}

// diffFor builds a compact diff preview of a change: removed lines prefixed
// with "- " and added lines with "+ ", capped at maxLines.
func diffFor(oldS, newS string, maxLines int) string {
	if oldS == newS {
		return ""
	}
	var removed, added []string
	switch {
	case oldS == "":
		added = strings.Split(strings.TrimRight(newS, "\n"), "\n")
	case newS == "":
		removed = strings.Split(strings.TrimRight(oldS, "\n"), "\n")
	default:
		have := map[string]int{}
		for _, l := range strings.Split(oldS, "\n") {
			have[l]++
		}
		for _, l := range strings.Split(newS, "\n") {
			if have[l] > 0 {
				have[l]--
			} else {
				added = append(added, l)
			}
		}
		for _, l := range strings.Split(oldS, "\n") {
			if have[l] > 0 {
				have[l]--
				removed = append(removed, l)
			}
		}
	}
	var out []string
	for _, l := range removed {
		out = append(out, "- "+l)
	}
	for _, l := range added {
		out = append(out, "+ "+l)
	}
	if len(out) == 0 {
		return ""
	}
	tail := ""
	if len(out) > maxLines {
		tail = fmt.Sprintf("… %d more changed lines", len(out)-maxLines)
		out = out[:maxLines]
	}
	s := strings.Join(out, "\n")
	if tail != "" {
		s += "\n" + tail
	}
	return s
}

func lineDelta(oldS, newS string) (added, removed int) {
	if oldS == newS {
		return 0, 0
	}
	have := map[string]int{}
	for _, l := range strings.Split(oldS, "\n") {
		have[l]++
	}
	for _, l := range strings.Split(newS, "\n") {
		if have[l] > 0 {
			have[l]--
		} else {
			added++
		}
	}
	for _, n := range have {
		removed += n
	}
	return added, removed
}

func truncateMiddle(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head
	return s[:head] + fmt.Sprintf("\n…[%d bytes omitted]…\n", len(s)-head-tail) + s[len(s)-tail:]
}

func (a *Agent) trim() {
	a.mu.Lock()
	defer a.mu.Unlock()
	maxChars := a.cfg.ContextTokens * 4
	if maxChars <= 0 {
		// No context limit known — never trim automatically.
		return
	}
	size := func() int {
		n := 0
		for _, m := range a.messages {
			n += len(m.Content) + 16
		}
		return n
	}
	for len(a.messages) > 4 && size() > maxChars {
		keep := make([]upstream.Message, 0, len(a.messages)-2)
		keep = append(keep, a.messages[0])
		keep = append(keep, a.messages[3:]...)
		a.messages = keep
	}
	if size() > maxChars {
		over := size() - maxChars
		for i := 1; i < len(a.messages)-1 && over > 0; i++ {
			c := a.messages[i].Content
			cut := over + 1024
			if cut > len(c) {
				cut = len(c)
			}
			if cut <= 0 {
				continue
			}
			a.messages[i].Content = c[:len(c)-cut] + "\n…[trimmed]…"
			break
		}
	}
}
