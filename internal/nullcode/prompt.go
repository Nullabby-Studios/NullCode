package nullcode

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

func (a *Agent) systemPrompt() string {
	shell := "sh -c"
	if runtime.GOOS == "windows" {
		shell = "powershell -NoProfile -Command"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `You are NullCode, a terminal coding agent. Root: %s. OS: %s. Date: %s.
The exec tool always runs: %s — this is fixed. Never run shell-detection
commands (echo $SHELL, uname, ps); the shell is already known.
The transcript renders markdown: format answers with headings, lists,
tables and fenced code blocks.
`, a.cfg.Root, runtime.GOOS, time.Now().Format("2006-01-02"), shell)

	if a.nativeTools() {
		b.WriteString(`
# Acting
Use the provided tools. The shell above is fixed — never probe it. Relative
paths only, never "..". Prefer write with the complete file over patch. After
code changes, run the build/test/lint. Never start servers or interactive
programs. When done, reply with a short summary and no tool call.
`)
		return b.String()
	}
	var d []string
	if a.features["exec"] {
		d = append(d, "exec <shell command>")
	}
	if a.features["read"] {
		d = append(d, "read <path>")
	}
	if a.features["list"] {
		d = append(d, "list <dir>")
	}
	if a.features["search"] {
		d = append(d, "search <pattern> [path]")
	}
	if a.features["write"] {
		d = append(d, "write <path>\n<the complete file content>\nend")
	}
	if a.features["patch"] {
		d = append(d, "patch <path>\n<<<\nexact old text\n>>>\nreplacement text\nend")
	}
	if a.features["todos"] {
		d = append(d, "todo add|done|undo|list|clear [text or item number]")
	}
	if a.features["skills"] {
		d = append(d, "skill <name>")
	}
	if a.features["mcps"] {
		d = append(d, "mcp | mcp <server> | mcp <server> <tool> <json args>")
	}
	if a.features["ask"] {
		d = append(d, "ask <question for the user>")
	}
	d = append(d, "done")

	fmt.Fprintf(&b, `
# Acting
You act ONLY through directives: a line whose first word is the tool name,
e.g. "exec go test ./..." or "write src/fix.go". Describing an action in prose
does nothing — only a directive runs. So whenever the task involves files,
commands, tests or the user, emit the directive now, in the same turn; do not
stop after explaining your plan. You may chain several directives in one turn.
Every write/patch ends with an "end" line right after its last content line.
Only the tools below exist; a turn with no directive produces no action.
Results arrive between a "result ..." line and an "end" line.

%s

# Rules
- Relative paths only; never ".." or absolute.
- exec: non-interactive, has a timeout, no servers/REPLs; use -y/--no-pager.
  Windows shell is PowerShell: chain with ";" not "&&".
- After code changes run the build/test/lint as a directive.
- On an error: fix it or stop; never repeat a failing action.
- Done: one-paragraph summary, then a done line.

`, strings.Join(d, "\n"))
	a.writeExample(&b)
	return b.String()
}

// writeExample appends a worked multi-tool example built only from the enabled
// tools, so a small model imitates chaining several directives together.
func (a *Agent) writeExample(b *strings.Builder) {
	var lines []string
	if a.features["read"] {
		lines = append(lines, "read src/main.go")
	}
	if a.features["list"] {
		lines = append(lines, "list .")
	}
	if a.features["write"] {
		lines = append(lines, "write src/fix.go\nfunc Main() {}\nend")
	}
	if a.features["patch"] {
		lines = append(lines, "patch src/main.go\n<<<\nold\n>>>\nnew\nend")
	}
	if a.features["search"] {
		lines = append(lines, `search "TODO" .`)
	}
	if a.features["exec"] {
		lines = append(lines, "exec go test ./...")
	}
	if len(lines) == 0 {
		lines = append(lines, "exec ls")
	}
	b.WriteString("# Example\n")
	b.WriteString("I'll read the file and fix it.\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("It now compiles.\ndone\n")
}
