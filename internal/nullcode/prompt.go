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
Act only via directives: a line whose first word is the tool name, e.g.
exec <command> or write <path>. Close every write/patch with an end line
right after the last content line. Only the tools below exist; no directive
means nothing happens. Results arrive between a result line and an end line.

%s

# Rules
- Relative paths only; never ".." or absolute.
- exec: non-interactive, has a timeout, no servers/REPLs; use -y/--no-pager.
  Windows shell is PowerShell: chain with ";" not "&&".
- Multiple directives per turn run in order. One line of prose, then act.
- After code changes run build/test/lint. On an error: fix it or stop; never
  repeat a failing action.
- Done: one-paragraph summary, then a done line.

# Example
`, strings.Join(d, "\n"))
	switch {
	case a.features["exec"]:
		b.WriteString("I'll check the project.\nexec ls\n")
	case a.features["read"]:
		b.WriteString("I'll check the project.\nread go.mod\n")
	default:
		b.WriteString("I'll check the project.\nlist .\n")
	}
	return b.String()
}
