package nullcode

import "strings"

// markdown.go renders model output as terminal markdown. Models answer in
// markdown, so the transcript parses it instead of dumping raw asterisks:
// headings, fenced code, lists, quotes, tables and inline emphasis all
// become styled, width-wrapped transcript lines. The parser is forgiving —
// during streaming it sees partial input and never panics on unclosed
// markers.

type frag struct {
	text string
	st   sty
}

type mdLine []frag

func fragsWidth(fs []frag) int {
	n := 0
	for _, f := range fs {
		n += strWidth(f.text)
	}
	return n
}

// mdLines parses markdown text and returns styled lines wrapped to width.
func mdLines(text string, width int, fg, bg rgb) []mdLine {
	if width < 8 {
		width = 8
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	lines := strings.Split(text, "\n")
	var out []mdLine
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		switch {
		case t == "":
			i++
		case strings.HasPrefix(t, "```"):
			var code []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				code = append(code, lines[i])
				i++
			}
			if i < len(lines) {
				i++
			}
			out = append(out, mdCodeLines(code, width, bg)...)
		case mdIsHeading(t):
			level, body := mdHeading(t)
			st := fgc(cInk).boldOn()
			switch {
			case level == 1:
				st = fgc(cBlue).boldOn().underOn()
			case level == 2:
				st = fgc(cBlue).boldOn()
			case level <= 3:
				st = fgc(cBlueDeep).boldOn()
			}
			for _, ln := range wrapFrags(mdInline(body, fg, bg), width, width) {
				out = append(out, applyStyle(ln, st))
			}
			i++
		case mdIsHR(t):
			out = append(out, mdLine{{strings.Repeat("─", minInt(width, 42)), fgc(cLine)}})
			i++
		case strings.HasPrefix(t, ">"):
			var quote []string
			for i < len(lines) {
				q := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(q, ">") {
					break
				}
				quote = append(quote, strings.TrimPrefix(q, ">"))
				i++
			}
			bar := frag{"▏ ", fgc(cMuted)}
			for j, ln := range wrapFrags(mdInline(strings.TrimSpace(strings.Join(quote, " ")), fg, bg), width, width-2) {
				if j == 0 {
					out = append(out, append(mdLine{bar}, ln...))
				} else {
					out = append(out, append(mdLine{{"  ", fgc(cMuted)}}, ln...))
				}
			}
		case mdIsTableRow(t):
			rows, header := mdTable(lines, &i)
			out = append(out, mdTableLines(rows, header, width, fg, bg)...)
		case mdIsBullet(t) || mdIsOrdered(t):
			items := mdListItems(lines, &i)
			for _, it := range items {
				out = append(out, mdListItemLines(it, width, fg, bg)...)
			}
		default:
			var para []string
			for i < len(lines) {
				p := strings.TrimSpace(lines[i])
				if p == "" || mdStartsBlock(p) {
					break
				}
				para = append(para, p)
				i++
			}
			if len(para) > 0 {
				out = append(out, wrapFrags(mdInline(strings.Join(para, " "), fg, bg), width, width)...)
			}
		}
	}
	return out
}

// mdStartsBlock reports whether a trimmed line opens a new markdown block,
// ending paragraph collection.
func mdStartsBlock(t string) bool {
	return strings.HasPrefix(t, "```") || mdIsHeading(t) || mdIsHR(t) ||
		strings.HasPrefix(t, ">") || mdIsTableRow(t) || mdIsBullet(t) || mdIsOrdered(t)
}

func mdIsHeading(t string) bool {
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && n < len(t) && t[n] == ' '
}

func mdHeading(t string) (level int, body string) {
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return n, strings.TrimSpace(t[n:])
}

func mdIsHR(t string) bool {
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for _, r := range t {
		if rune(r) != rune(c) && r != ' ' {
			return false
		}
	}
	return true
}

// mdIsBullet reports "- ", "* " or "+ " list markers.
func mdIsBullet(t string) bool {
	if len(t) < 2 {
		return false
	}
	if t[0] != '-' && t[0] != '*' && t[0] != '+' {
		return false
	}
	return t[1] == ' '
}

// mdIsOrdered reports "1. " or "1) " list markers.
func mdIsOrdered(t string) bool {
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == 0 || i > 4 || i >= len(t) {
		return false
	}
	return (t[i] == '.' || t[i] == ')') && i+1 < len(t) && t[i+1] == ' '
}

func mdIndent(raw string) int {
	return len(raw) - len(strings.TrimLeft(raw, " "))
}

type mdItem struct {
	depth int
	ord   string
	task  string // "", "done", "open"
	text  string
}

// mdListItems collects a run of list lines; non-bullet lines attach to the
// previous item as lazy continuation.
func mdListItems(lines []string, i *int) []mdItem {
	var items []mdItem
	for *i < len(lines) {
		raw := lines[*i]
		t := strings.TrimSpace(raw)
		if t == "" {
			break
		}
		depth := mdIndent(raw) / 2
		switch {
		case mdIsBullet(t):
			text := strings.TrimSpace(t[2:])
			items = append(items, mdItem{depth: depth, task: mdTask(text), text: mdTaskText(text)})
			*i++
		case mdIsOrdered(t):
			idx := strings.IndexAny(t, ".)")
			text := strings.TrimSpace(t[idx+1:])
			items = append(items, mdItem{depth: depth, ord: t[:idx+1], task: mdTask(text), text: mdTaskText(text)})
			*i++
		case len(items) > 0:
			items[len(items)-1].text += " " + t
			*i++
		default:
			return items
		}
	}
	return items
}

func mdTask(text string) string {
	switch {
	case strings.HasPrefix(text, "[x] "), strings.HasPrefix(text, "[X] "):
		return "done"
	case strings.HasPrefix(text, "[ ] "):
		return "open"
	}
	return ""
}

func mdTaskText(text string) string {
	if mdTask(text) != "" {
		return strings.TrimSpace(text[4:])
	}
	return text
}

func mdListItemLines(it mdItem, width int, fg, bg rgb) []mdLine {
	ind := it.depth * 2
	var pre mdLine
	switch {
	case it.task == "done":
		pre = mdLine{{strings.Repeat(" ", ind) + "☑ ", fgc(cGreen)}}
	case it.task == "open":
		pre = mdLine{{strings.Repeat(" ", ind) + "☐ ", fgc(cMuted)}}
	case it.ord != "":
		pre = mdLine{{strings.Repeat(" ", ind) + it.ord + " ", fgc(cBlue)}}
	default:
		mark := "•"
		if it.depth%2 == 1 {
			mark = "◦"
		}
		pre = mdLine{{strings.Repeat(" ", ind) + mark + " ", fgc(cBlue)}}
	}
	preW := fragsWidth(pre)
	lines := wrapFrags(mdInline(it.text, fg, bg), width, width-preW)
	if len(lines) == 0 {
		lines = []mdLine{{}}
	}
	pad := frag{strings.Repeat(" ", preW), fgc(fg)}
	for j := range lines {
		if j == 0 {
			lines[j] = append(append(mdLine{}, pre...), lines[j]...)
		} else {
			lines[j] = append(mdLine{pad}, lines[j]...)
		}
	}
	return lines
}

func mdCodeLines(code []string, width int, bg rgb) []mdLine {
	gutter := fgc(cLine)
	codeSt := fgc(cBlueDeep)
	var out []mdLine
	for _, ln := range code {
		ln = strings.TrimRight(ln, " ")
		for _, p := range hardWrap(ln, maxInt(1, width-2)) {
			out = append(out, mdLine{{"│ ", gutter}, {p, codeSt}})
		}
	}
	return out
}

// mdTable collects consecutive table rows. When the second row is a
// separator (|---|---|) the first row is a header.
func mdTable(lines []string, i *int) (rows [][]string, header bool) {
	var raws []string
	for *i < len(lines) {
		t := strings.TrimSpace(lines[*i])
		if !mdIsTableRow(t) {
			break
		}
		raws = append(raws, t)
		*i++
	}
	if len(raws) >= 2 && mdIsTableSep(raws[1]) {
		header = true
		raws = append(raws[:1], raws[2:]...)
	}
	for _, r := range raws {
		rows = append(rows, mdCells(r))
	}
	return rows, header
}

func mdIsTableRow(t string) bool {
	return strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 2
}

func mdCells(row string) []string {
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

func mdIsTableSep(t string) bool {
	inner := strings.NewReplacer("|", "", ":", "", " ", "", "-", "").Replace(t)
	if inner != "" {
		return false
	}
	return strings.Contains(t, "-")
}

func mdTableLines(rows [][]string, header bool, width int, fg, bg rgb) []mdLine {
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return nil
	}
	ws := make([]int, cols)
	for _, r := range rows {
		for c, cell := range r {
			if w := strWidth(cell); w > ws[c] {
				ws[c] = w
			}
		}
	}
	const gap = 2
	total := gap * (cols - 1)
	for _, w := range ws {
		total += w
	}
	if total > width && total > 0 {
		scale := float64(width) / float64(total)
		for c := range ws {
			nw := int(float64(ws[c]) * scale)
			if nw < 3 {
				nw = 3
			}
			ws[c] = nw
		}
	}
	var out []mdLine
	for ri, r := range rows {
		var line mdLine
		for c := 0; c < cols; c++ {
			cell := ""
			if c < len(r) {
				cell = r[c]
			}
			if c > 0 {
				line = append(line, frag{"  ", fgc(fg)})
			}
			if ri == 0 && header {
				line = append(line, frag{clipWidth(cell, ws[c]), fgc(cInk).boldOn()})
				line = append(line, frag{strings.Repeat(" ", maxInt(0, ws[c]-strWidth(cell))), fgc(fg)})
				continue
			}
			line = append(line, mdInline(clipWidth(cell, ws[c]), fg, bg)...)
			line = append(line, frag{strings.Repeat(" ", maxInt(0, ws[c]-strWidth(cell))), fgc(fg)})
		}
		out = append(out, line)
		if ri == 0 && header {
			var sep mdLine
			for c := 0; c < cols; c++ {
				if c > 0 {
					sep = append(sep, frag{"  ", fgc(fg)})
				}
				sep = append(sep, frag{strings.Repeat("─", ws[c]), fgc(cLine)})
			}
			out = append(out, sep)
		}
	}
	return out
}

// mdInline parses inline markdown into styled fragments: bold, italic,
// inline code, strikethrough, links and bare URLs, with backslash escapes.
func mdInline(text string, fg, bg rgb) []frag {
	plain := fgc(fg)
	var out []frag
	emit := func(text string, st sty) {
		if text != "" {
			out = append(out, frag{text: text, st: st})
		}
	}
	i := 0
	var buf strings.Builder
	flush := func() {
		emit(buf.String(), plain)
		buf.Reset()
	}
	for i < len(text) {
		c := text[i]
		switch {
		case c == '\\' && i+1 < len(text):
			buf.WriteByte(text[i+1])
			i += 2
		case c == '`':
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				flush()
				emit(text[i+1:i+1+end], fgc(cCoral))
				i += end + 2
			} else {
				buf.WriteByte(c)
				i++
			}
		case strings.HasPrefix(text[i:], "**"):
			if end := strings.Index(text[i+2:], "**"); end >= 0 {
				flush()
				emit(text[i+2:i+2+end], plain.boldOn())
				i += end + 4
			} else {
				buf.WriteString("**")
				i += 2
			}
		case strings.HasPrefix(text[i:], "__"):
			if end := strings.Index(text[i+2:], "__"); end >= 0 {
				flush()
				emit(text[i+2:i+2+end], plain.boldOn())
				i += end + 4
			} else {
				buf.WriteString("__")
				i += 2
			}
		case strings.HasPrefix(text[i:], "~~"):
			if end := strings.Index(text[i+2:], "~~"); end >= 0 {
				flush()
				emit(text[i+2:i+2+end], plain.dimOn())
				i += end + 4
			} else {
				buf.WriteString("~~")
				i += 2
			}
		case c == '*':
			if end := strings.IndexByte(text[i+1:], '*'); end >= 0 && text[i+1] != ' ' {
				flush()
				emit(text[i+1:i+1+end], plain.italicOn())
				i += end + 2
			} else {
				buf.WriteByte(c)
				i++
			}
		case c == '_':
			if mdUnderscorePair(text, i) {
				if end := strings.IndexByte(text[i+1:], '_'); end >= 0 {
					flush()
					emit(text[i+1:i+1+end], plain.italicOn())
					i += end + 2
					continue
				}
			}
			buf.WriteByte(c)
			i++
		case c == '[':
			if rb := strings.IndexByte(text[i:], ']'); rb > 0 && i+rb+1 < len(text) && text[i+rb+1] == '(' {
				if end := strings.IndexByte(text[i+rb+2:], ')'); end >= 0 {
					label := text[i+1 : i+rb]
					url := text[i+rb+2 : i+rb+2+end]
					flush()
					if label == "" || label == url {
						emit(url, fgc(cBlue).underOn())
					} else {
						emit(label+" ", fgc(cBlue).underOn())
						emit("("+url+")", fgc(cMuted))
					}
					i = i + rb + end + 3
					continue
				}
			}
			buf.WriteByte(c)
			i++
		case strings.HasPrefix(text[i:], "http://") || strings.HasPrefix(text[i:], "https://"):
			end := i
			for end < len(text) && text[end] != ' ' && text[end] != ')' {
				end++
			}
			flush()
			emit(text[i:end], fgc(cBlue).underOn())
			i = end
		default:
			buf.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

// mdUnderscorePair guards _italic_: both underscores must sit at word
// boundaries so snake_case identifiers stay literal.
func mdUnderscorePair(text string, i int) bool {
	if i+1 >= len(text) || text[i+1] == ' ' || text[i+1] == '_' {
		return false
	}
	if i > 0 {
		p := text[i-1]
		if isAlnum(p) {
			return false
		}
	}
	end := strings.IndexByte(text[i+1:], '_')
	if end < 0 {
		return false
	}
	if end == 0 {
		return false
	}
	if i+1+end+1 < len(text) {
		n := text[i+1+end+1]
		if isAlnum(n) {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// splitWords splits a fragment run into words; a word may span several
// fragments (e.g. **bo**ld) and keeps each fragment's own style.
func splitWords(frags []frag) [][]frag {
	var words [][]frag
	var cur []frag
	add := func(f frag) {
		if n := len(cur); n > 0 && cur[n-1].st == f.st {
			cur[n-1].text += f.text
		} else {
			cur = append(cur, f)
		}
	}
	endWord := func() {
		if len(cur) > 0 {
			words = append(words, cur)
			cur = nil
		}
	}
	for _, f := range frags {
		start := 0
		for j := 0; j < len(f.text); j++ {
			if f.text[j] == ' ' {
				if start < j {
					add(frag{text: f.text[start:j], st: f.st})
				}
				endWord()
				start = j + 1
			}
		}
		if start < len(f.text) {
			add(frag{text: f.text[start:], st: f.st})
		}
	}
	endWord()
	return words
}

// mergeFrags joins adjacent fragments that share a style, keeping the ANSI
// escape count per line low.
func mergeFrags(line mdLine) mdLine {
	if len(line) < 2 {
		return line
	}
	out := mdLine{}
	for _, f := range line {
		if n := len(out); n > 0 && out[n-1].st == f.st {
			out[n-1].text += f.text
		} else {
			out = append(out, f)
		}
	}
	return out
}

// hardWrapFrags wraps a word across lines at width, preserving styles.
func hardWrapFrags(frags []frag, width int) []mdLine {
	if width < 1 {
		width = 1
	}
	var out []mdLine
	var cur mdLine
	n := 0
	for _, f := range frags {
		for _, r := range f.text {
			w := runeWidth(r)
			if n+w > width && len(cur) > 0 {
				out = append(out, mergeFrags(cur))
				cur = nil
				n = 0
			}
			cur = append(cur, frag{string(r), f.st})
			n += w
		}
	}
	if len(cur) > 0 {
		out = append(out, mergeFrags(cur))
	}
	return out
}

// wrapFrags wraps a fragment run into lines fitting avail columns. The
// caller prepends its own indents.
func wrapFrags(frags []frag, width, avail int) []mdLine {
	if avail < 1 {
		avail = 1
	}
	var out []mdLine
	var cur mdLine
	curW := 0
	push := func() {
		if len(cur) > 0 {
			out = append(out, mergeFrags(cur))
			cur = nil
			curW = 0
		}
	}
	for _, w := range splitWords(frags) {
		ww := fragsWidth(w)
		if ww > avail {
			push()
			for _, ln := range hardWrapFrags(w, avail) {
				out = append(out, ln)
			}
			continue
		}
		add := ww
		if curW > 0 {
			add++
		}
		if curW+add > avail {
			push()
		}
		if curW > 0 {
			cur = append(cur, frag{" ", sty{}})
		}
		cur = append(cur, w...)
		curW += add
	}
	push()
	return out
}

// applyStyle restyles every fragment in a line (used for headings).
func applyStyle(line mdLine, st sty) mdLine {
	for i := range line {
		line[i].st = st
	}
	return line
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
