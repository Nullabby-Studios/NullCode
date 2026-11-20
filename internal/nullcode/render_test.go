package nullcode

import (
	"regexp"
	"strings"
	"testing"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func frameLines(t *testing.T, a *App) []string {
	t.Helper()
	f := a.frame()
	f = strings.TrimPrefix(f, "\x1b[?25l\x1b[H")
	lines := strings.Split(f, "\r\n")
	if len(lines) != a.height {
		t.Fatalf("frame has %d lines, want %d", len(lines), a.height)
	}
	return lines
}

func TestFrameFillsWidth(t *testing.T) {
	setTheme(false)
	a := &App{width: 80, height: 24, model: "nullabby1.5", think: true, showReasoning: true}
	a.welcome()
	a.blocks = append(a.blocks,
		&Block{kind: bUser, label: "you", text: strings.Repeat("long user prompt ", 20)},
		&Block{kind: bAssistant, label: "nullcode", text: "I'll check.\n\n```\nfunc main() {}\n```"},
		&Block{kind: bTool, label: "exec", text: "go test ./...", meta: "running"},
		&Block{kind: bResult, label: "result", text: strings.Repeat("ok line\n", 80), meta: "exit=0"},
		&Block{kind: bReasoning, label: "thinking", text: strings.Repeat("reasoning ", 30)},
	)
	lines := frameLines(t, a)
	for i, line := range lines {
		clean := ansiRE.ReplaceAllString(line, "")
		if got := strWidth(clean); got != 80 {
			t.Fatalf("line %d width = %d, want 80: %q", i, got, clean)
		}
	}
	frame := strings.Join(lines, "\n")
	for _, want := range []string{"38;2;32;59;180", "48;2;244;246;251", "NullCode"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame missing %q", want)
		}
	}
}

func TestFrameCursorRow(t *testing.T) {
	a := &App{width: 60, height: 20, model: "m", think: true, showReasoning: true}
	a.input = []rune("hello")
	a.cursor = 5
	a.width, a.height = 60, 20
	f := a.frame()
	re := regexp.MustCompile(`\x1b\[(\d+);(\d+)H\x1b\[\?25h`)
	m := re.FindStringSubmatch(f)
	if m == nil {
		t.Fatal("no cursor positioning sequence")
	}
	if m[1] != "18" { // h-2
		t.Fatalf("cursor row = %s, want 18", m[1])
	}
	if m[2] != "9" { // 4 + len("hello")
		t.Fatalf("cursor col = %s, want 9", m[2])
	}
}

func TestFrameInputScrolls(t *testing.T) {
	a := &App{width: 40, height: 12, model: "m", think: true, showReasoning: true}
	a.input = []rune(strings.Repeat("x", 200))
	a.cursor = len(a.input)
	f := a.frame()
	if len(f) == 0 {
		t.Fatal("empty frame")
	}
	for i, line := range strings.Split(strings.TrimPrefix(f, "\x1b[?25l\x1b[H"), "\r\n") {
		clean := ansiRE.ReplaceAllString(line, "")
		if strWidth(clean) != 40 {
			t.Fatalf("line %d width = %d, want 40", i, strWidth(clean))
		}
	}
}

func TestHardWrapWideRunes(t *testing.T) {
	lines := hardWrap("你好世界你好世界", 6)
	for _, l := range lines {
		if strWidth(l) > 6 {
			t.Fatalf("width overflow: %q (%d)", l, strWidth(l))
		}
	}
}

func TestThemeDark(t *testing.T) {
	setTheme(true)
	defer setTheme(false)
	a := &App{width: 60, height: 16, model: "m", think: true, showReasoning: true}
	f := a.frame()
	if !strings.Contains(f, "48;2;20;24;36") {
		t.Fatal("dark page background missing")
	}
	if !strings.Contains(f, "48;2;28;34,48") {
		// panel color is 0x1c,0x22,0x30 = 28,34,48
		t.Log("panel background not found (non-fatal)")
	}
	if themeName() != "dark" {
		t.Fatal("theme name should be dark")
	}
	setTheme(false)
	if themeName() != "light" {
		t.Fatal("theme name should be light")
	}
}

func TestBlockCacheInvalidates(t *testing.T) {
	a := &App{width: 40, height: 14, model: "m", showReasoning: true}
	b := &Block{kind: bResult, label: "result", text: strings.Repeat("out line\n", 40), meta: "exit=0"}
	a.blocks = []*Block{b}

	frame := strings.Join(frameLines(t, a), "\n")
	if !strings.Contains(frame, "Ctrl+O to expand") {
		t.Fatal("expected collapsed result")
	}
	a.expanded = true
	frame = strings.Join(frameLines(t, a), "\n")
	if strings.Contains(frame, "Ctrl+O to expand") {
		t.Fatal("expanded result still collapsed")
	}

	b.text = "fresh text"
	a.expanded = false
	frame = strings.Join(frameLines(t, a), "\n")
	if !strings.Contains(frame, "fresh text") {
		t.Fatal("text change was not rendered")
	}
	b.meta = "exit=1"
	frame = strings.Join(frameLines(t, a), "\n")
	if !strings.Contains(frame, "exit=1") {
		t.Fatal("meta change was not rendered")
	}

	a.width = 52
	lines := frameLines(t, a)
	for i, line := range lines {
		if got := strWidth(ansiRE.ReplaceAllString(line, "")); got != 52 {
			t.Fatalf("line %d width = %d, want 52", i, got)
		}
	}
}
