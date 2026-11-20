package nullcode

import (
	"strings"
	"testing"
)

func collect(t *testing.T, chunks ...string) (*Filter, string, []Action, []string) {
	t.Helper()
	f := &Filter{}
	var prose, notes strings.Builder
	var actions []Action
	h := filterHooks{
		prose:  func(s string) { prose.WriteString(s) },
		action: func(a Action) {},
		note:   func(s string) { notes.WriteString(s + "\n") },
	}
	for _, c := range chunks {
		f.Feed(c, h)
	}
	actions = f.Finish(h)
	return f, prose.String(), actions, []string{}
}

func TestFilterExecAndProse(t *testing.T) {
	f, prose, actions, _ := collect(t,
		"I'll inspect the repo.\n",
		"\u27e6exec\u27e7 ls -la\n",
		"\u27e6read\u27e7 go.mod\n",
		"\u27e6done\u27e7\n",
	)
	if got := strings.TrimSpace(prose); got != "I'll inspect the repo." {
		t.Fatalf("prose = %q", got)
	}
	if f.Prose() != prose {
		t.Fatal("filter prose mismatch")
	}
	if len(actions) != 3 {
		t.Fatalf("actions = %+v", actions)
	}
	if actions[0].Kind != "exec" || actions[0].Arg != "ls -la" {
		t.Fatalf("exec = %+v", actions[0])
	}
	if actions[1].Kind != "read" || actions[1].Arg != "go.mod" {
		t.Fatalf("read = %+v", actions[1])
	}
	if actions[2].Kind != "done" {
		t.Fatalf("done = %+v", actions[2])
	}
}

func TestFilterWriteBodyIsHidden(t *testing.T) {
	src := "Writing main.go.\n\u27e6write\u27e7 main.go\npackage main\n\nfunc main() {}\n\u27e6end\u27e7\nDone.\n"
	_, prose, actions, _ := collect(t, src)
	if strings.Contains(prose, "package main") {
		t.Fatalf("write body leaked into prose: %q", prose)
	}
	if strings.TrimSpace(prose) != "Writing main.go.\nDone." {
		t.Fatalf("prose = %q", prose)
	}
	if len(actions) != 1 || actions[0].Kind != "write" || actions[0].Arg != "main.go" {
		t.Fatalf("actions = %+v", actions)
	}
	if actions[0].Body != "package main\n\nfunc main() {}\n" {
		t.Fatalf("body = %q", actions[0].Body)
	}
}

func TestFilterSplitChunks(t *testing.T) {
	_, prose, actions, _ := collect(t,
		"hi\n\u27e6ex",
		"ec\u27e7 echo ",
		"hello\n\u27e6do",
		"ne\u27e7\n",
	)
	if len(actions) != 2 || actions[0].Arg != "echo hello" || actions[1].Kind != "done" {
		t.Fatalf("actions = %+v", actions)
	}
	if !strings.Contains(prose, "hi") {
		t.Fatalf("prose = %q", prose)
	}
}

func TestFilterUnknownDirective(t *testing.T) {
	_, _, actions, _ := collect(t, "\u27e6teleport\u27e7 now\n")
	if len(actions) != 1 || actions[0].Kind != "unknown" || actions[0].Arg != "teleport" {
		t.Fatalf("actions = %+v", actions)
	}
}

func TestFilterIncompleteWriteRuns(t *testing.T) {
	f := &Filter{}
	var notes []string
	h := filterHooks{note: func(s string) { notes = append(notes, s) }}
	f.Feed("text\n\u27e6write\u27e7 a.txt\nhello\n", h)
	actions := f.Finish(h)
	// An unterminated block still runs so the model gets feedback instead
	// of a silent no-op.
	if len(actions) != 1 || actions[0].Kind != "write" || actions[0].Body != "hello\n" || !actions[0].Incomplete {
		t.Fatalf("incomplete write should run, got %+v", actions)
	}
	if len(notes) == 0 {
		t.Fatal("expected a warning")
	}
}

func TestFilterInlineEndMarker(t *testing.T) {
	_, _, actions, _ := collect(t, "\u27e6write\u27e7 a.txt\nhello\u27e6end\u27e7 trailing\n")
	if len(actions) != 1 || actions[0].Kind != "write" || actions[0].Body != "hello\n" {
		t.Fatalf("actions = %+v", actions)
	}
}

func TestFilterStrayEndIgnored(t *testing.T) {
	_, _, actions, _ := collect(t, "\u27e6end\u27e7\nplain text\n")
	if len(actions) != 0 {
		t.Fatalf("stray end should be ignored, got %+v", actions)
	}
}

func TestFilterDirectiveNeedsLineStart(t *testing.T) {
	_, prose, actions, _ := collect(t, "explain \u27e6exec\u27e7 rm -rf /\n")
	if len(actions) != 0 {
		t.Fatalf("mid-line directive parsed: %+v", actions)
	}
	if !strings.Contains(prose, "rm -rf") {
		t.Fatalf("prose = %q", prose)
	}
}

func TestFilterIndentedDirective(t *testing.T) {
	_, _, actions, _ := collect(t, "  \u27e6exec\u27e7 go test ./...\n")
	if len(actions) != 1 || actions[0].Arg != "go test ./..." {
		t.Fatalf("actions = %+v", actions)
	}
}

func TestParsePatchBody(t *testing.T) {
	old, new, err := parsePatchBody("<<<\nold line\nsecond\n>>>\nnew line\n")
	if err != nil {
		t.Fatal(err)
	}
	if old != "old line\nsecond" {
		t.Fatalf("old = %q", old)
	}
	if new != "new line\n" {
		t.Fatalf("new = %q", new)
	}
}

func TestThinkSplitter(t *testing.T) {
	var reason, answer strings.Builder
	s := &ThinkSplitter{}
	fn := func(kind, text string) {
		if kind == "reasoning" {
			reason.WriteString(text)
		} else {
			answer.WriteString(text)
		}
	}
	s.Feed("<thi", fn)
	s.Feed("nk>plan</thi", fn)
	s.Feed("nk>answer", fn)
	s.Flush(fn)
	if reason.String() != "plan" {
		t.Fatalf("reason = %q", reason.String())
	}
	if answer.String() != "answer" {
		t.Fatalf("answer = %q", answer.String())
	}
}

func TestThinkSplitterStrayClose(t *testing.T) {
	var reason, answer strings.Builder
	s := &ThinkSplitter{}
	fn := func(kind, text string) {
		if kind == "reasoning" {
			reason.WriteString(text)
		} else {
			answer.WriteString(text)
		}
	}
	s.Feed("thinking</think>done", fn)
	s.Flush(fn)
	if reason.String() != "thinking" || answer.String() != "done" {
		t.Fatalf("reason=%q answer=%q", reason.String(), answer.String())
	}
}

func TestFilterNativeToolCalls(t *testing.T) {
	src := "Checking.\n<tool_call>\n<function=exec>\n<parameter=command>\ngo test ./...\n</parameter>\n</function>\n</tool_call>\n" +
		"<tool_call>\n<function=write>\n<parameter=path>\nmain.go\n</parameter>\n<parameter=content>\npackage main\n</parameter>\n</function>\n</tool_call>\n" +
		"<tool_call>\n<function=patch>\n<parameter=path>\na.go\n</parameter>\n<parameter=old_string>\nold\n</parameter>\n<parameter=new_string>\nnew\n</parameter>\n</function>\n</tool_call>\n"
	_, prose, actions, _ := collect(t, src)
	if strings.TrimSpace(prose) != "Checking." {
		t.Fatalf("prose = %q", prose)
	}
	if len(actions) != 3 {
		t.Fatalf("actions = %+v", actions)
	}
	if actions[0].Kind != "exec" || actions[0].Arg != "go test ./..." {
		t.Fatalf("exec = %+v", actions[0])
	}
	if actions[1].Kind != "write" || actions[1].Arg != "main.go" || actions[1].Body != "package main" {
		t.Fatalf("write = %+v", actions[1])
	}
	if actions[2].Kind != "patch" || actions[2].Arg != "a.go" || actions[2].Body != "<<<\nold\n>>>\nnew" {
		t.Fatalf("patch = %+v", actions[2])
	}
}

func TestAssistantHistoryKeepsActions(t *testing.T) {
	got := assistantHistory("look", []Action{
		{Kind: "exec", Arg: "ls"},
		{Kind: "write", Arg: "a.go", Body: "package main\n"},
		{Kind: "done"},
	})
	// Writes are recorded with their body and closing marker so the model
	// sees complete examples of the block syntax in context.
	want := "look\n⟦exec⟧ ls\n⟦write⟧ a.go\npackage main\n⟦end⟧"
	if got != want {
		t.Fatalf("history = %q", got)
	}
}

func TestFilterNativeJSONToolCall(t *testing.T) {
	src := "<tool_call>\n{\"name\":\"exec\",\"arguments\":{\"command\":\"go test ./...\"}}\n</tool_call>\n"
	_, prose, actions, _ := collect(t, src)
	if strings.TrimSpace(prose) != "" {
		t.Fatalf("prose = %q", prose)
	}
	if len(actions) != 1 || actions[0].Kind != "exec" || actions[0].Arg != "go test ./..." {
		t.Fatalf("actions = %+v", actions)
	}
}
