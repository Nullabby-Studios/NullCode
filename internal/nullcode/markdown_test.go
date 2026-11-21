package nullcode

import (
	"strings"
	"testing"
)

func mdText(lines []mdLine) string {
	var b strings.Builder
	for _, ln := range lines {
		for _, f := range ln {
			b.WriteString(f.text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestMDHeading(t *testing.T) {
	setTheme(false)
	lines := mdLines("# Title\n\n## Sub", 60, cInk, cCloud)
	got := mdText(lines)
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Sub") {
		t.Fatalf("heading text missing: %q", got)
	}
	if !lines[0][0].st.bold {
		t.Fatal("heading should be bold")
	}
	if !lines[0][0].st.underline {
		t.Fatal("h1 should be underlined")
	}
}

func TestMDInlineStyles(t *testing.T) {
	setTheme(false)
	lines := mdLines("plain **bold** *ital* `code` ~~gone~~", 60, cInk, cCloud)
	var frags []frag
	for _, ln := range lines {
		frags = append(frags, ln...)
	}
	var bold, ital, code, dim bool
	for _, f := range frags {
		switch {
		case f.text == "bold" && f.st.bold:
			bold = true
		case f.text == "ital" && f.st.italic:
			ital = true
		case f.text == "code" && f.st.fg == cCoral:
			code = true
		case f.text == "gone" && f.st.dim:
			dim = true
		}
	}
	if !bold || !ital || !code || !dim {
		t.Fatalf("bold=%v ital=%v code=%v dim=%v frags=%+v", bold, ital, code, dim, frags)
	}
	got := mdText(lines)
	if strings.Contains(got, "**") || strings.Contains(got, "`") {
		t.Fatalf("markers leaked: %q", got)
	}
}

func TestMDSnakeCaseStaysLiteral(t *testing.T) {
	setTheme(false)
	lines := mdLines("call my_func_name(x) now", 60, cInk, cCloud)
	got := mdText(lines)
	if !strings.Contains(got, "my_func_name") {
		t.Fatalf("snake_case mangled: %q", got)
	}
	for _, ln := range lines {
		for _, f := range ln {
			if f.st.italic {
				t.Fatalf("snake_case italicized: %+v", f)
			}
		}
	}
}

func TestMDFence(t *testing.T) {
	setTheme(false)
	lines := mdLines("before\n\n```go\nfunc main() {\n\treturn\n}\n```\n\nafter", 60, cInk, cCloud)
	got := mdText(lines)
	for _, want := range []string{"│ ", "func main() {", "    return", "after"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fence output missing %q:\n%s", want, got)
		}
	}
}

func TestMDFenceUnclosed(t *testing.T) {
	setTheme(false)
	lines := mdLines("```\nstill streaming", 60, cInk, cCloud)
	got := mdText(lines)
	if !strings.Contains(got, "still streaming") || !strings.Contains(got, "│ ") {
		t.Fatalf("unclosed fence not rendered as code: %q", got)
	}
}

func TestMDLists(t *testing.T) {
	setTheme(false)
	lines := mdLines("- one\n- two\n  - nested\n\n1. first\n2. second", 60, cInk, cCloud)
	got := mdText(lines)
	for _, want := range []string{"• one", "• two", "◦ nested", "1. first", "2. second"} {
		if !strings.Contains(got, want) {
			t.Fatalf("list output missing %q:\n%s", want, got)
		}
	}
}

func TestMDTaskList(t *testing.T) {
	setTheme(false)
	lines := mdLines("- [x] done thing\n- [ ] open thing", 60, cInk, cCloud)
	got := mdText(lines)
	if !strings.Contains(got, "☑ done thing") || !strings.Contains(got, "☐ open thing") {
		t.Fatalf("task list wrong: %q", got)
	}
}

func TestMDQuote(t *testing.T) {
	setTheme(false)
	lines := mdLines("> quoted text\n> more", 60, cInk, cCloud)
	got := mdText(lines)
	if !strings.Contains(got, "▏ quoted text more") {
		t.Fatalf("quote wrong: %q", got)
	}
}

func TestMDTable(t *testing.T) {
	setTheme(false)
	lines := mdLines("| Name | Value |\n|------|-------|\n| a    | 1     |", 60, cInk, cCloud)
	got := mdText(lines)
	if !strings.Contains(got, "Name") || !strings.Contains(got, "─") || !strings.Contains(got, "a") {
		t.Fatalf("table wrong:\n%s", got)
	}
	// header must be bold
	found := false
	for _, f := range lines[0] {
		if f.text == "Name" && f.st.bold {
			found = true
		}
	}
	if !found {
		t.Fatal("table header not bold")
	}
}

func TestMDWrapsToWidth(t *testing.T) {
	setTheme(false)
	lines := mdLines(strings.Repeat("word ", 40), 30, cInk, cCloud)
	for _, ln := range lines {
		w := 0
		for _, f := range ln {
			w += strWidth(f.text)
		}
		if w > 30 {
			t.Fatalf("line overflow %d: %+v", w, ln)
		}
	}
}

func TestMDEmptyAndBlank(t *testing.T) {
	setTheme(false)
	lines := mdLines("", 60, cInk, cCloud)
	if len(lines) != 0 {
		t.Fatalf("empty text should render nothing, got %d lines", len(lines))
	}
}

func TestMDLinks(t *testing.T) {
	setTheme(false)
	lines := mdLines("see [docs](https://example.com) here", 60, cInk, cCloud)
	got := mdText(lines)
	if !strings.Contains(got, "docs") || !strings.Contains(got, "https://example.com") {
		t.Fatalf("link wrong: %q", got)
	}
	var link bool
	for _, ln := range lines {
		for _, f := range ln {
			if f.text == "docs" && f.st.underline && f.st.fg == cBlue {
				link = true
			}
		}
	}
	if !link {
		t.Fatal("link label not styled")
	}
}

func TestMDEscapes(t *testing.T) {
	setTheme(false)
	lines := mdLines(`literal \*not bold\* here`, 60, cInk, cCloud)
	got := mdText(lines)
	if !strings.Contains(got, "*not bold*") {
		t.Fatalf("escape wrong: %q", got)
	}
	for _, ln := range lines {
		for _, f := range ln {
			if f.st.bold {
				t.Fatal("escaped markers should not be bold")
			}
		}
	}
}
