// Package nullcode — NullCode, a minimal coding TUI for the Nullabby
// model family, with switchable light and dark themes.
package nullcode

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Palette mirrors the Nullabby studio CSS variables.
type rgb struct{ r, g, b int }

var (
	cBlue     rgb
	cBlueDeep rgb
	cBlueSoft rgb
	cSky      rgb
	cCoral    rgb
	cLime     rgb
	cYellow   rgb
	cInk      rgb
	cMuted    rgb
	cCloud    rgb
	cWhite    rgb
	cLine     rgb
	cUserBG   rgb
	cGreen    rgb

	themeDark bool
)

// setTheme switches the palette. Rendering happens on the UI goroutine,
// so reassigning the package colors is safe.
func setTheme(dark bool) {
	themeDark = dark
	if dark {
		cCloud = rgb{0x14, 0x18, 0x24}
		cWhite = rgb{0x1c, 0x22, 0x30}
		cUserBG = rgb{0x1b, 0x25, 0x36}
		cSky = rgb{0x17, 0x32, 0x42}
		cBlue = rgb{0x42, 0x63, 0xd6}
		cBlueDeep = rgb{0x93, 0xaa, 0xf2}
		cBlueSoft = rgb{0xcc, 0xd9, 0xff}
		cCoral = rgb{0xff, 0x8d, 0x80}
		cLime = rgb{0xdc, 0xed, 0x89}
		cYellow = rgb{0xff, 0xd9, 0x6e}
		cInk = rgb{0xd7, 0xdc, 0xe6}
		cMuted = rgb{0x8a, 0x92, 0xa6}
		cLine = rgb{0x2b, 0x32, 0x47}
		cGreen = rgb{0x63, 0xc0, 0x8d}
		return
	}
	cBlue = rgb{0x20, 0x3b, 0xb4}
	cBlueDeep = rgb{0x12, 0x23, 0x6f}
	cBlueSoft = rgb{0xdc, 0xe7, 0xff}
	cSky = rgb{0xc9, 0xec, 0xff}
	cCoral = rgb{0xff, 0x75, 0x67}
	cLime = rgb{0xdc, 0xed, 0x89}
	cYellow = rgb{0xff, 0xd9, 0x6e}
	cInk = rgb{0x17, 0x1a, 0x24}
	cMuted = rgb{0x68, 0x70, 0x80}
	cCloud = rgb{0xf4, 0xf6, 0xfb}
	cWhite = rgb{0xff, 0xff, 0xff}
	cLine = rgb{0xe2, 0xe6, 0xf2}
	cUserBG = rgb{0xef, 0xf4, 0xff}
	cGreen = rgb{0x2f, 0x7d, 0x4f}
}

func themeName() string {
	if themeDark {
		return "dark"
	}
	return "light"
}

// reResolverPort strips the " on <resolver-ip>:53" clause that Go appends to
// a failed DNS lookup. Go's dial error is shaped like
// "dial tcp: lookup <host> on <ip>:53: <cause>", where <ip> is the local
// resolver the query was sent to (the systemd-resolved loopback stub,
// 127.0.0.53, on most Linux boxes). That address is an artifact of the
// resolver, not of the backend being contacted, so surfacing it in the UI
// reads as "the program resolved <host> to 127.0.0.53". Dropping the clause
// leaves the actionable part: "lookup <host>: no such host". Only port 53
// (DNS) is stripped; real backend ports such as [::1]:8081 are preserved.
var reResolverPort = regexp.MustCompile(` on [0-9][0-9a-fA-F:.]*:53:`)

// cleanNetErr removes the local-resolver address from a network error so the
// UI shows why the lookup failed without naming the resolver's own IP.
func cleanNetErr(err error) string {
	if err == nil {
		return ""
	}
	s := reResolverPort.ReplaceAllString(err.Error(), ":")
	// A stripped clause can leave a doubled "host:" + "no such host" as
	// "host:: ..." only when the cause itself started with a colon; normalize.
	s = strings.ReplaceAll(s, ":: ", ": ")
	return s
}

func init() { setTheme(true) }

type sty struct {
	fg, bg    rgb
	hasFg     bool
	hasBg     bool
	bold      bool
	dim       bool
	italic    bool
	underline bool
}

func fgc(c rgb) sty        { return sty{fg: c, hasFg: true} }
func bgc(c rgb) sty        { return sty{bg: c, hasBg: true} }
func fgbg(f, b rgb) sty    { return sty{fg: f, bg: b, hasFg: true, hasBg: true} }
func (s sty) boldOn() sty  { s.bold = true; return s }
func (s sty) dimOn() sty   { s.dim = true; return s }
func (s sty) italicOn() sty { s.italic = true; return s }
func (s sty) underOn() sty { s.underline = true; return s }

func (s sty) prefix() string {
	var b strings.Builder
	b.WriteString("\x1b[")
	first := true
	add := func(code string) {
		if !first {
			b.WriteByte(';')
		}
		b.WriteString(code)
		first = false
	}
	if s.bold {
		add("1")
	}
	if s.dim {
		add("2")
	}
	if s.italic {
		add("3")
	}
	if s.underline {
		add("4")
	}
	if s.hasFg {
		add(fmt.Sprintf("38;2;%d;%d;%d", s.fg.r, s.fg.g, s.fg.b))
	}
	if s.hasBg {
		add(fmt.Sprintf("48;2;%d;%d;%d", s.bg.r, s.bg.g, s.bg.b))
	}
	b.WriteByte('m')
	return b.String()
}

func (s sty) paint(text string) string {
	if text == "" {
		return ""
	}
	return s.prefix() + text + "\x1b[0m"
}

const ansiReset = "\x1b[0m"

// lw accumulates one physical terminal row and tracks its visible width.
type lw struct {
	b strings.Builder
	w int
}

func (l *lw) put(s sty, text string) {
	if text == "" {
		return
	}
	l.b.WriteString(s.prefix())
	l.b.WriteString(text)
	l.b.WriteString(ansiReset)
	l.w += strWidth(text)
}

func (l *lw) fill(s sty, n int) {
	if n <= 0 {
		return
	}
	l.b.WriteString(s.prefix())
	l.b.WriteString(strings.Repeat(" ", n))
	l.b.WriteString(ansiReset)
	l.w += n
}

func (l *lw) str() string { return l.b.String() }

// band paints a full-width row with a base background.
func band(w int, base sty, fn func(*lw)) string {
	if w < 1 {
		w = 1
	}
	l := &lw{}
	l.put(base, " ")
	if fn != nil {
		fn(l)
	}
	if l.w < w-1 {
		l.fill(base, w-1-l.w)
	}
	l.put(base, " ")
	return l.str()
}

func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0
	case isWide(r):
		return 2
	default:
		return 1
	}
}

func isWide(r rune) bool {
	return r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1f64f) ||
		(r >= 0x1f900 && r <= 0x1f9ff) ||
		(r >= 0x20000 && r <= 0x3fffd))
}

func strWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func clipWidth(s string, max int) string {
	if max <= 0 {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		rw := runeWidth(r)
		if n+rw > max {
			break
		}
		b.WriteRune(r)
		n += rw
	}
	return b.String()
}

func hardWrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		if raw == "" {
			out = append(out, "")
			continue
		}
		var cur strings.Builder
		n := 0
		for _, r := range raw {
			rw := runeWidth(r)
			if n+rw > width && cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				n = 0
			}
			cur.WriteRune(r)
			n += rw
		}
		out = append(out, cur.String())
	}
	return out
}

func wordWrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		indent := raw[:len(raw)-len(strings.TrimLeft(raw, " "))]
		words := strings.Fields(raw)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		indentW := strWidth(indent)
		avail := width - indentW
		if avail < 1 {
			avail = 1
		}
		cur := ""
		for _, word := range words {
			if strWidth(word) > avail {
				if cur != "" {
					out = append(out, cur)
				}
				for _, piece := range hardWrap(word, avail) {
					out = append(out, indent+piece)
				}
				cur = ""
				continue
			}
			if cur == "" {
				cur = indent + word
				continue
			}
			if strWidth(cur)+1+strWidth(word) <= width {
				cur += " " + word
			} else {
				out = append(out, cur)
				cur = indent + word
			}
		}
		if cur != "" {
			out = append(out, cur)
		}
	}
	return out
}
