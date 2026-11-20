package nullcode

import (
	"strings"
	"testing"
)

func TestFrameManySizes(t *testing.T) {
	for _, w := range []int{20, 24, 30, 40, 60, 80, 120} {
		for _, h := range []int{8, 10, 12, 24, 40} {
			a := &App{width: w, height: h, model: "nullabby1.5-merged", think: false, showReasoning: true, yolo: true}
			a.welcome()
			a.blocks = append(a.blocks,
				&Block{kind: bUser, label: "you", text: strings.Repeat("a long user prompt ", 8)},
				&Block{kind: bAssistant, label: "nullcode", text: strings.Repeat("word ", 40)},
				&Block{kind: bTool, label: "exec", text: "go test ./...", meta: "exit=0"},
				&Block{kind: bResult, label: "result", text: strings.Repeat("ok\n", 100), meta: "exit=0"},
				&Block{kind: bError, label: "error", text: "something failed", meta: ""},
				&Block{kind: bNotice, label: "notice", text: "note"},
			)
			a.input = []rune(strings.Repeat("typed text ", 30))
			a.cursor = len(a.input)
			a.status = "waiting for approval"
			lines := frameLines(t, a)
			for i, line := range lines {
				clean := ansiRE.ReplaceAllString(line, "")
				if got := strWidth(clean); got != w {
					t.Fatalf("w=%d h=%d line %d width=%d", w, h, i, got)
				}
			}
			a.approval = &Approval{Kind: "exec", Detail: strings.Repeat("rm -rf ", 40)}
			lines = frameLines(t, a)
			for i, line := range lines {
				clean := ansiRE.ReplaceAllString(line, "")
				if got := strWidth(clean); got != w {
					t.Fatalf("approval w=%d h=%d line %d width=%d", w, h, i, got)
				}
			}
		}
	}
}
