package nullcode

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// NullCode wire syntax. A directive must be the first non-space token on a
// line and is fenced with U+27E6/U+27E7. User text is never parsed: only
// assistant answer text passes through Filter.
const (
	glyphOpen  = "\u27e6"
	glyphClose = "\u27e7"
	endMarker  = glyphOpen + "end" + glyphClose
)

// endMarkerAt returns the index and length of the end marker in s, or -1.
func endMarkerAt(s string) (idx, n int) {
	g := strings.Index(s, endMarker)
	if g < 0 {
		return -1, 0
	}
	return g, len(endMarker)
}

// Action is one decoded NullCode directive.
type Action struct {
	Kind       string `json:"kind"`
	Arg        string `json:"arg,omitempty"`
	Body       string `json:"-"`
	Line       int    `json:"line"`
	Incomplete bool   `json:"-"`
}

func (a Action) String() string {
	if a.Arg == "" {
		return a.Kind
	}
	return a.Kind + " " + a.Arg
}

func isKnownTool(kind string) bool {
	switch kind {
	case "exec", "read", "write", "patch", "done",
		"list", "search", "todo", "skill", "mcp", "ask":
		return true
	}
	return false
}

// Filter splits streamed assistant output into display prose and actions.
// write/patch bodies are swallowed instead of being echoed to the terminal.
type Filter struct {
	pending string
	lineNo  int

	inBody   bool
	bodyKind string
	bodyArg  string
	bodyLine int
	body     strings.Builder
	bodyN    int

	native  bool
	nativeL []string

	actions []Action
	prose   strings.Builder
	notes   []string
}

type filterHooks struct {
	prose  func(string)
	action func(Action)
	body   func(Action, int)
	note   func(string)
}

func (f *Filter) Feed(chunk string, h filterHooks) {
	f.pending += chunk
	for {
		i := strings.IndexByte(f.pending, '\n')
		if i < 0 {
			return
		}
		line := strings.TrimSuffix(f.pending[:i], "\r")
		f.pending = f.pending[i+1:]
		f.process(line, h)
	}
}

func (f *Filter) Finish(h filterHooks) []Action {
	if f.pending != "" {
		f.process(strings.TrimSuffix(f.pending, "\r"), h)
		f.pending = ""
	}
	if f.native {
		f.native = false
		f.nativeL = nil
		if h.note != nil {
			h.note("incomplete <tool_call> block at end of message")
		}
		f.notes = append(f.notes, "incomplete tool_call dropped")
	}
	if f.inBody {
		// The message ended inside a write/patch block: keep the action so
		// it still runs and the model gets feedback, instead of dropping
		// it silently.
		act := Action{Kind: f.bodyKind, Arg: f.bodyArg, Body: f.body.String(), Line: f.bodyLine, Incomplete: true}
		f.inBody = false
		f.actions = append(f.actions, act)
		if h.action != nil {
			h.action(act)
		}
		if h.note != nil {
			h.note(fmt.Sprintf("message ended inside a %s block without %s — running it anyway", act.Kind, endMarker))
		}
	}
	return f.actions
}

func (f *Filter) process(line string, h filterHooks) {
	f.lineNo++
	if f.native {
		if strings.TrimSpace(line) == "</tool_call>" {
			f.native = false
			f.flushNative(h)
			return
		}
		f.nativeL = append(f.nativeL, line)
		return
	}
	if strings.TrimSpace(line) == "<tool_call>" {
		f.native = true
		f.nativeL = f.nativeL[:0]
		return
	}
	if t := strings.TrimSpace(line); strings.HasPrefix(t, "<tool_call>") {
		rest := strings.TrimPrefix(t, "<tool_call>")
		if i := strings.Index(rest, "</tool_call>"); i >= 0 {
			f.native = false
			f.nativeL = []string{rest[:i]}
			f.flushNative(h)
			return
		}
		f.native = true
		f.nativeL = f.nativeL[:0]
		if rest != "" {
			f.nativeL = append(f.nativeL, rest)
		}
		return
	}
	if f.inBody {
		if idx, n := endMarkerAt(line); idx >= 0 {
			before := strings.TrimRight(line[:idx], " \t\r")
			after := line[idx+n:]
			if strings.TrimSpace(before) != "" {
				f.body.WriteString(before)
				f.body.WriteByte('\n')
				f.bodyN++
			}
			act := Action{Kind: f.bodyKind, Arg: f.bodyArg, Body: f.body.String(), Line: f.bodyLine}
			f.inBody = false
			f.body.Reset()
			f.bodyN = 0
			f.actions = append(f.actions, act)
			if h.action != nil {
				h.action(act)
			}
			// Anything after the marker is a new line (prose or the next
			// directive).
			if strings.TrimSpace(after) != "" {
				f.process(after, h)
			}
			return
		}
		f.body.WriteString(line)
		f.body.WriteByte('\n')
		f.bodyN++
		if h.body != nil && f.bodyN%8 == 0 {
			h.body(Action{Kind: f.bodyKind, Arg: f.bodyArg, Line: f.bodyLine}, f.bodyN)
		}
		return
	}

	kind, arg, ok := parseDirective(line)
	if !ok {
		f.prose.WriteString(line)
		f.prose.WriteByte('\n')
		if h.prose != nil {
			h.prose(line + "\n")
		}
		return
	}

	if !isKnownTool(kind) {
		if kind == "end" {
			// Stray end marker outside a block: ignore it.
			return
		}
		act := Action{Kind: "unknown", Arg: kind, Line: f.lineNo}
		f.actions = append(f.actions, act)
		if h.action != nil {
			h.action(act)
		}
		if h.note != nil {
			h.note("unknown directive " + glyphOpen + kind + glyphClose)
		}
		return
	}

	act := Action{Kind: kind, Arg: arg, Line: f.lineNo}
	switch kind {
	case "write", "patch":
		f.inBody = true
		f.bodyKind = kind
		f.bodyArg = arg
		f.bodyLine = f.lineNo
		f.body.Reset()
		f.bodyN = 0
		if h.action != nil {
			h.action(Action{Kind: kind, Arg: arg, Line: f.lineNo})
		}
	default:
		f.actions = append(f.actions, act)
		if h.action != nil {
			h.action(act)
		}
	}
}

func (f *Filter) flushNative(h filterHooks) {
	lines := f.nativeL
	f.nativeL = nil
	name := ""
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "<function=") && strings.HasSuffix(t, ">") {
			name = strings.TrimSuffix(strings.TrimPrefix(t, "<function="), ">")
			break
		}
	}
	params := parseNativeParams(lines)
	if name == "" {
		if n, p, ok := parseNativeJSON(lines); ok {
			name = n
			params = p
		}
	}
	if name == "" {
		if h.note != nil {
			h.note("ignored tool_call without a function name")
		}
		return
	}
	act := nativeAction(name, params)
	act.Line = f.lineNo
	if act.Kind == "unknown" {
		f.actions = append(f.actions, act)
		if h.action != nil {
			h.action(act)
		}
		if h.note != nil {
			h.note("unknown native tool " + name)
		}
		return
	}
	if act.Kind == "write" || act.Kind == "patch" {
		f.actions = append(f.actions, act)
		if h.action != nil {
			h.action(Action{Kind: act.Kind, Arg: act.Arg, Line: act.Line})
		}
		if h.body != nil {
			lines := strings.Count(act.Body, "\n")
			if act.Body != "" {
				lines++
			}
			h.body(Action{Kind: act.Kind, Arg: act.Arg, Line: act.Line}, lines)
		}
		return
	}
	f.actions = append(f.actions, act)
	if h.action != nil {
		h.action(act)
	}
}

func parseNativeParams(lines []string) map[string]string {
	params := make(map[string]string)
	key := ""
	var buf []string
	flush := func() {
		if key != "" {
			params[key] = strings.Join(buf, "\n")
			key = ""
			buf = nil
		}
	}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if key != "" {
			if t == "</parameter>" {
				flush()
			} else {
				buf = append(buf, line)
			}
			continue
		}
		if !strings.HasPrefix(t, "<parameter=") {
			continue
		}
		rest := strings.TrimPrefix(t, "<parameter=")
		i := strings.IndexByte(rest, '>')
		if i < 0 {
			continue
		}
		name := rest[:i]
		val := rest[i+1:]
		if j := strings.Index(val, "</parameter>"); j >= 0 {
			params[name] = val[:j]
			continue
		}
		key = name
		if val != "" {
			buf = append(buf, val)
		}
	}
	flush()
	return params
}

func parseNativeJSON(lines []string) (string, map[string]string, bool) {
	s := strings.Join(lines, "\n")
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i < 0 || j <= i {
		return "", nil, false
	}
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), &call); err != nil || call.Name == "" {
		return "", nil, false
	}
	params := make(map[string]string)
	add := func(raw []byte) {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return
		}
		for k, v := range obj {
			params[k] = stringifyArg(v)
		}
	}
	if len(call.Arguments) > 0 {
		add(call.Arguments)
		if len(params) == 0 {
			var inner string
			if err := json.Unmarshal(call.Arguments, &inner); err == nil {
				add([]byte(inner))
			}
		}
	}
	return call.Name, params, true
}

func stringifyArg(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

func nativeAction(name string, p map[string]string) Action {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "functions.")
	switch name {
	case "exec", "run", "shell", "bash", "terminal", "execute":
		return Action{Kind: "exec", Arg: strings.TrimSpace(firstParam(p, "command", "cmd", "input", "script"))}
	case "read", "read_file", "view", "open":
		return Action{Kind: "read", Arg: strings.TrimSpace(firstParam(p, "path", "file", "filename", "target_file"))}
	case "write", "write_file", "create", "create_file":
		return Action{
			Kind: "write",
			Arg:  strings.TrimSpace(firstParam(p, "path", "file", "filename", "target_file")),
			Body: trimNewlines(firstParam(p, "content", "text", "body", "file_text")),
		}
	case "patch", "edit", "edit_file", "apply_patch", "replace":
		arg := strings.TrimSpace(firstParam(p, "path", "file", "filename", "target_file"))
		old := trimNewlines(firstParam(p, "old", "old_string", "old_str", "old_text", "search", "target"))
		repl := trimNewlines(firstParam(p, "new", "new_string", "new_str", "new_text", "replace", "replacement"))
		if old == "" {
			return Action{Kind: "unknown", Arg: name}
		}
		return Action{Kind: "patch", Arg: arg, Body: "<<<\n" + old + "\n>>>\n" + repl}
	case "done", "finish", "stop":
		return Action{Kind: "done"}
	}
	return Action{Kind: "unknown", Arg: name}
}

func firstParam(p map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := p[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func trimNewlines(s string) string { return strings.Trim(s, "\r\n") }

// Actions returns all decoded actions, including block bodies.
func (f *Filter) Actions() []Action { return f.actions }

// Prose returns assistant text with actions and block bodies removed.
func (f *Filter) Prose() string { return f.prose.String() }

// Notes returns parser warnings.
func (f *Filter) Notes() []string { return f.notes }

func parseDirective(line string) (kind, arg string, ok bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, glyphOpen) {
		return "", "", false
	}
	rest := t[len(glyphOpen):]
	end := strings.Index(rest, glyphClose)
	if end < 0 {
		return "", "", false
	}
	kind = strings.ToLower(strings.TrimSpace(rest[:end]))
	if kind == "" {
		return "", "", false
	}
	arg = strings.TrimSpace(rest[end+len(glyphClose):])
	return kind, arg, true
}

func unquotePath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parsePatchBody decodes the <<< old >>> new block used by ⟦patch⟧.
func parsePatchBody(body string) (old, new string, err error) {
	lines := strings.Split(body, "\n")
	start, mid := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case "<<<":
			if start < 0 {
				start = i
			}
		case ">>>":
			if start >= 0 {
				mid = i
			}
		}
		if mid >= 0 {
			break
		}
	}
	if start < 0 || mid < 0 {
		return "", "", errors.New("patch body needs <<< old text >>> new text on their own lines")
	}
	old = strings.Join(lines[start+1:mid], "\n")
	new = strings.Join(lines[mid+1:], "\n")
	if old == "" {
		return "", "", errors.New("patch old text is empty")
	}
	return old, new, nil
}

// ThinkSplitter separates Qwen <think>…</think> reasoning from answer text,
// including when a tag is split across SSE chunks.
type ThinkSplitter struct {
	buf     string
	inThink bool
	sawTag  bool
}

func (t *ThinkSplitter) Feed(chunk string, fn func(kind, text string)) {
	t.buf += chunk
	for {
		open := strings.Index(t.buf, "<think>")
		close := strings.Index(t.buf, "</think>")
		switch {
		case open < 0 && close < 0:
			keep := partialTagSuffix(t.buf, "<think>", "</think>")
			if len(t.buf) > keep {
				emit(t, t.buf[:len(t.buf)-keep], fn)
				t.buf = t.buf[len(t.buf)-keep:]
			}
			return
		case close >= 0 && (open < 0 || close < open):
			if close > 0 {
				// Text before a closing tag is reasoning even when the
				// opening tag was stripped by the server.
				t.inThink = true
				emit(t, t.buf[:close], fn)
				t.buf = t.buf[close:]
			}
			t.buf = t.buf[len("</think>"):]
			t.inThink = false
			t.sawTag = true
		default:
			if open > 0 {
				emit(t, t.buf[:open], fn)
				t.buf = t.buf[open:]
			}
			t.buf = t.buf[len("<think>"):]
			t.inThink = true
			t.sawTag = true
		}
	}
}

func (t *ThinkSplitter) Flush(fn func(kind, text string)) {
	if t.buf != "" {
		emit(t, t.buf, fn)
		t.buf = ""
	}
}

func emit(t *ThinkSplitter, s string, fn func(kind, text string)) {
	if s == "" || fn == nil {
		return
	}
	if t.inThink {
		fn("reasoning", s)
	} else {
		fn("content", s)
	}
}

func partialTagSuffix(s string, tags ...string) int {
	best := 0
	for _, tag := range tags {
		max := len(tag) - 1
		if len(s) < max {
			max = len(s)
		}
		for n := max; n > 0; n-- {
			if strings.HasSuffix(s, tag[:n]) {
				if n > best {
					best = n
				}
				break
			}
		}
	}
	return best
}
