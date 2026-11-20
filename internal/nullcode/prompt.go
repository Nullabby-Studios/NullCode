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
	fmt.Fprintf(&b, `You are NullCode, a coding agent running in a terminal for the Nullabby model family.
Project root: %s
OS: %s
Shell used by the exec tool: %s
Date: %s
`, a.cfg.Root, runtime.GOOS, shell, time.Now().Format("2006-01-02"))

	if a.nativeTools() {
		b.WriteString(`
# Acting
Use the provided tools. Relative paths only, never "..". Prefer write with the
complete file over patch. After code changes, run the build/test/lint. Never
start servers or interactive programs. When done, reply with a short summary
and no tool call.
`)
		return b.String()
	}

	var d []string
	if a.features["exec"] {
		d = append(d, "⟦exec⟧ <shell command>")
	}
	if a.features["read"] {
		d = append(d, "⟦read⟧ <path>")
	}
	if a.features["list"] {
		d = append(d, "⟦list⟧ <dir>")
	}
	if a.features["search"] {
		d = append(d, "⟦search⟧ <pattern> [path]")
	}
	if a.features["write"] {
		d = append(d, "⟦write⟧ <path>\n<the complete file content>\n⟦end⟧")
	}
	if a.features["patch"] {
		d = append(d, "⟦patch⟧ <path>\n<<<\nexact old text\n>>>\nreplacement text\n⟦end⟧")
	}
	if a.features["todos"] {
		d = append(d, "⟦todo⟧ add|done|undo|list|clear [text or item number]")
	}
	if a.features["skills"] {
		d = append(d, "⟦skill⟧ <name>")
	}
	if a.features["mcps"] {
		d = append(d, "⟦mcp⟧ | ⟦mcp⟧ <server> | ⟦mcp⟧ <server> <tool> <json args>")
	}
	if a.features["ask"] {
		d = append(d, "⟦ask⟧ <question for the user>")
	}
	d = append(d, "⟦done⟧")

	fmt.Fprintf(&b, `
# Acting
Act only via directives. A directive is a line starting with the exact glyphs
shown (U+27E6 ⟦, U+27E7 ⟧); never in backticks, quotes, or code fences. Close
every write/patch with ⟦end⟧ on its own line, right after the last content
line. Only the tools below exist; no directive means nothing happens.

%s

Results come back between ⟦result⟧ and ⟦end⟧ lines.

# Rules
- Relative paths only; never ".." or absolute paths.
- exec is non-interactive with a timeout: never start servers, editors, or
  REPLs; use -y / --no-pager. Windows shell is PowerShell: use ";" not "&&".
- Several directives per turn run in order. One line of prose, then the action.
- After code changes run the build/test/lint. On an error, fix it or stop;
  never repeat a failing action.
- When done: one-paragraph summary, then ⟦done⟧.

# Example
`, strings.Join(d, "\n"))
	switch {
	case a.features["exec"]:
		b.WriteString("I'll check the project.\n⟦exec⟧ ls\n")
	case a.features["read"]:
		b.WriteString("I'll check the project.\n⟦read⟧ go.mod\n")
	default:
		b.WriteString("I'll check the project.\n⟦list⟧ .\n")
	}
	return b.String()
}
