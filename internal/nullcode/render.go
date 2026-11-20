package nullcode

import (
	"fmt"
	"strings"
)

type seg struct {
	text string
	st   sty
}

func blankLine(w int) string {
	return band(w, bgc(cCloud), nil)
}

func (a *App) render() {
	fmt.Print(a.frame())
}

func (a *App) frame() string {
	w, h := a.width, a.height
	if w < 20 {
		w = 20
	}
	if h < 6 {
		h = 6
	}
	t := h - 5
	if t < 1 {
		t = 1
	}
	lines := make([]string, 0, h)
	lines = append(lines, a.renderHeader(w))
	lines = append(lines, a.renderTranscript(w, t)...)
	lines = append(lines, a.renderInput(w)...)
	lines = append(lines, a.renderFooter(w))
	for len(lines) < h {
		lines = append(lines, blankLine(w))
	}
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	if a.connect != nil || a.functions != nil || a.palette != nil || a.picker != nil ||
		a.choice != nil || a.sessionsM != nil {
		mw := w - 10
		if mw > 84 {
			mw = 84
		}
		if mw < 24 {
			mw = maxInt(16, w-4)
		}
		var ml []string
		switch {
		case a.connect != nil:
			ml = a.modalLines(mw)
		case a.functions != nil:
			ml = a.functionsModal(mw)
		case a.picker != nil:
			ml = a.modelModal(mw)
		case a.choice != nil:
			ml = a.choiceModal(mw)
		case a.sessionsM != nil:
			ml = a.sessionsModal(mw)
		default:
			ml = a.paletteModal(mw)
		}
		if len(ml) > h-2 {
			ml = ml[:maxInt(1, h-2)]
		}
		mh := len(ml)
		top := (h - mh) / 2
		if top < 1 {
			top = 1
		}
		if top+mh > h {
			top = maxInt(1, h-mh)
		}
		left := (w - mw) / 2
		for i, row := range ml {
			if top+i < len(lines) {
				lines[top+i] = overlayRow(w, row, mw, left)
			}
		}
	}

	var b strings.Builder
	b.Grow(w * h * 3)
	b.WriteString("\x1b[?25l\x1b[H")
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(line)
	}
	if a.approval == nil && a.connect == nil && a.functions == nil && a.palette == nil &&
		a.picker == nil && a.choice == nil && a.sessionsM == nil {
		col := a.cursorCol
		if col < 1 {
			col = 1
		}
		if col > w {
			col = w
		}
		row := t + 3
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", row, col)
	}
	return b.String()
}

func (a *App) renderHeader(w int) string {
	return band(w, bgc(cBlue), func(l *lw) {
		l.put(fgbg(cWhite, cBlue).boldOn(), " NullCode")
		modelMax := w/2 - 14
		if modelMax < 0 {
			modelMax = 0
		}
		if modelMax > 26 {
			modelMax = 26
		}
		mdl := a.model
		if mdl == "" {
			mdl = "auto"
		}
		if modelMax >= 6 {
			l.put(fgbg(cYellow, cBlue).boldOn(), "  "+clipWidth(mdl, modelMax))
		}
		segs := a.statusView()
		maxStatus := w - l.w - 3
		if maxStatus < 2 {
			maxStatus = 2
		}
		clipped := make([]seg, 0, len(segs))
		sw := 0
		for _, s := range segs {
			rem := maxStatus - sw
			if rem <= 0 {
				break
			}
			txt := clipWidth(s.text, rem)
			if txt == "" {
				break
			}
			clipped = append(clipped, seg{text: txt, st: s.st})
			sw += strWidth(txt)
		}
		gap := w - l.w - sw - 2
		if gap > 0 {
			l.fill(bgc(cBlue), gap)
		}
		for _, s := range clipped {
			l.put(s.st, s.text)
		}
		l.put(bgc(cBlue), " ")
	})
}

func (a *App) statusView() []seg {
	if a.approval != nil {
		return []seg{
			{"● ", fgbg(cCoral, cBlue)},
			{"approval", fgbg(cWhite, cBlue).boldOn()},
		}
	}
	if a.busy {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		sp := frames[a.spinner%len(frames)]
		st := a.status
		if st == "" {
			st = "working"
		}
		return []seg{
			{sp + " ", fgbg(cYellow, cBlue)},
			{st, fgbg(cWhite, cBlue)},
		}
	}
	return []seg{
		{"● ", fgbg(cLime, cBlue)},
		{"ready", fgbg(cWhite, cBlue)},
	}
}

// paletteModal builds the command palette dialog: a filter row plus the
// matching slash commands. Enter runs the selected command, which opens its
// own dialog when it needs input.
func (a *App) paletteModal(mw int) []string {
	p := a.palette
	out := []string{modalTop(mw, "Commands")}
	out = append(out, band(mw, bgc(cWhite), func(l *lw) {
		l.put(fgbg(cBlue, cWhite).boldOn(), "  › /")
		l.put(fgbg(cInk, cWhite), string(p.filter))
		l.put(fgbg(cWhite, cBlue), " ")
		l.put(fgbg(cMuted, cWhite), "  "+clipWidth("type to filter", maxInt(0, mw-l.w-2)))
	}))
	out = append(out, band(mw, bgc(cWhite), nil))
	items := a.paletteItems()
	if len(items) == 0 {
		out = append(out, modalHint(mw, "no matching commands"))
	} else {
		if p.sel >= len(items) {
			p.sel = len(items) - 1
		}
		if p.sel < 0 {
			p.sel = 0
		}
		const maxVisible = 8
		start := 0
		if p.sel >= maxVisible {
			start = p.sel - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(items) {
			end = len(items)
		}
		for i := start; i < end; i++ {
			it := items[i]
			i := i
			if i == p.sel {
				out = append(out, band(mw, bgc(cWhite), func(l *lw) {
					l.put(fgbg(cWhite, cBlue).boldOn(), "  ❯ ")
					l.put(fgbg(cWhite, cBlue).boldOn(), clipWidth(it.cmd, maxInt(0, mw-l.w-2)))
					if it.desc != "" {
						l.put(fgbg(cBlueSoft, cBlue), "  "+clipWidth(it.desc, maxInt(0, mw-l.w-4)))
					}
				}))
			} else {
				out = append(out, band(mw, bgc(cWhite), func(l *lw) {
					l.put(fgbg(cMuted, cWhite), "    ")
					l.put(fgbg(cInk, cWhite), clipWidth(it.cmd, maxInt(0, mw-l.w-2)))
					if it.desc != "" {
						l.put(fgbg(cMuted, cWhite), "  "+clipWidth(it.desc, maxInt(0, mw-l.w-4)))
					}
				}))
			}
		}
		if hidden := len(items) - end; hidden > 0 {
			out = append(out, modalHint(mw, "… +"+fmt.Sprint(hidden)+" more"))
		}
	}
	out = append(out, band(mw, bgc(cWhite), nil))
	out = append(out, modalHint(mw, "↑↓ choose · enter run · esc close"))
	out = append(out, modalBottom(mw))
	return out
}

// modalModelList renders provider-grouped model rows with a highlighted
// selection, scrolling so the selection stays visible.
func modalModelList(mw int, rows []pickerRow, sel *int, maxVisible int) []string {
	if *sel >= len(rows) {
		*sel = len(rows) - 1
	}
	if *sel < 0 {
		*sel = 0
	}
	if rows[*sel].model == "" {
		*sel = connectSelect(rows, *sel, 1)
	}
	var out []string
	start := 0
	if *sel >= maxVisible {
		start = *sel - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(rows) {
		end = len(rows)
	}
	for i := start; i < end; i++ {
		r := rows[i]
		i := i
		if r.group != "" {
			out = append(out, band(mw, bgc(cWhite), func(l *lw) {
				l.put(fgbg(cBlue, cWhite).boldOn(), "  "+clipWidth(r.group, maxInt(0, mw-l.w-2)))
			}))
			continue
		}
		if i == *sel {
			out = append(out, band(mw, bgc(cWhite), func(l *lw) {
				l.put(fgbg(cWhite, cBlue).boldOn(), "  ❯ "+clipWidth(r.model, maxInt(0, mw-l.w-4)))
			}))
		} else {
			out = append(out, band(mw, bgc(cWhite), func(l *lw) {
				l.put(fgbg(cMuted, cWhite), "    ")
				l.put(fgbg(cInk, cWhite), clipWidth(r.model, maxInt(0, mw-l.w-2)))
			}))
		}
	}
	if hidden := len(rows) - end; hidden > 0 {
		out = append(out, modalHint(mw, "… +"+fmt.Sprint(hidden)+" more"))
	}
	return out
}

// modelModal builds the /model dialog: a search bar plus models grouped by
// provider. Enter selects; with no matches the search text is used as a
// custom model id.
func (a *App) modelModal(mw int) []string {
	p := a.picker
	out := []string{modalTop(mw, "Models")}
	out = append(out, band(mw, bgc(cWhite), func(l *lw) {
		l.put(fgbg(cBlue, cWhite).boldOn(), "  › ")
		l.put(fgbg(cInk, cWhite), string(p.filter))
		l.put(fgbg(cWhite, cBlue), " ")
		hint := "search models"
		switch {
		case len(a.providers) == 0:
			hint = "no providers — /connect first"
		case len(a.providerModels) == 0:
			hint = "loading models…"
		}
		l.put(fgbg(cMuted, cWhite), "  "+clipWidth(hint, maxInt(0, mw-l.w-2)))
	}))
	out = append(out, band(mw, bgc(cWhite), nil))
	rows := a.pickerRows(string(p.filter))
	if len(rows) == 0 {
		out = append(out, modalHint(mw, "no matches — press enter to use the search text as a model id"))
	} else {
		out = append(out, modalModelList(mw, rows, &p.sel, 10)...)
	}
	out = append(out, band(mw, bgc(cWhite), nil))
	out = append(out, modalHint(mw, "↑↓ choose · enter select · esc close"))
	out = append(out, modalBottom(mw))
	return out
}

// choiceModal builds a generic choice dialog (e.g. /tools tool mode).
func (a *App) choiceModal(mw int) []string {
	c := a.choice
	out := []string{modalTop(mw, c.title)}
	out = append(out, band(mw, bgc(cWhite), nil))
	for i, it := range c.choices {
		i := i
		if i == c.sel {
			out = append(out, band(mw, bgc(cWhite), func(l *lw) {
				l.put(fgbg(cWhite, cBlue).boldOn(), "  ❯ "+clipWidth(it.Value, maxInt(0, mw-8)))
				if it.Desc != "" {
					l.put(fgbg(cBlueSoft, cBlue), "  "+clipWidth(it.Desc, maxInt(0, mw-l.w-4)))
				}
			}))
		} else {
			out = append(out, band(mw, bgc(cWhite), func(l *lw) {
				l.put(fgbg(cInk, cWhite), "    "+clipWidth(it.Value, maxInt(0, mw-8)))
				if it.Desc != "" {
					l.put(fgbg(cMuted, cWhite), "  "+clipWidth(it.Desc, maxInt(0, mw-l.w-4)))
				}
			}))
		}
	}
	out = append(out, band(mw, bgc(cWhite), nil))
	out = append(out, modalHint(mw, "↑↓ choose · enter select · esc close"))
	out = append(out, modalBottom(mw))
	return out
}

// sessionsModal builds the /sessions dialog: saved sessions with topics.
func (a *App) sessionsModal(mw int) []string {
	s := a.sessionsM
	out := []string{modalTop(mw, "Sessions")}
	out = append(out, band(mw, bgc(cWhite), func(l *lw) {
		l.put(fgbg(cMuted, cWhite), "  "+clipWidth("most recent first · enter to open", maxInt(0, mw-4)))
	}))
	out = append(out, band(mw, bgc(cWhite), nil))
	if s.sel >= len(s.items) {
		s.sel = len(s.items) - 1
	}
	if s.sel < 0 {
		s.sel = 0
	}
	const maxVisible = 10
	start := 0
	if s.sel >= maxVisible {
		start = s.sel - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(s.items) {
		end = len(s.items)
	}
	for i := start; i < end; i++ {
		it := s.items[i]
		i := i
		topic := it.Topic
		if topic == "" {
			topic = it.Preview
		}
		if r := []rune(topic); len(r) > mw-24 {
			topic = string(r[:maxInt(8, mw-24)]) + "…"
		}
		mark := "  "
		if it.ID == a.sessionID {
			mark = "* "
		}
		meta := fmt.Sprint(it.Msgs) + " msgs"
		if i == s.sel {
			out = append(out, band(mw, bgc(cWhite), func(l *lw) {
				l.put(fgbg(cWhite, cBlue).boldOn(), "  ❯ ")
				l.put(fgbg(cWhite, cBlue).boldOn(), fmt.Sprintf("%d ", i+1))
				l.put(fgbg(cWhite, cBlue).boldOn(), mark+clipWidth(topic, maxInt(0, mw-l.w-2)))
				l.put(fgbg(cBlueSoft, cBlue), "  "+clipWidth(meta, maxInt(0, mw-l.w-4)))
			}))
		} else {
			out = append(out, band(mw, bgc(cWhite), func(l *lw) {
				l.put(fgbg(cMuted, cWhite), "    ")
				l.put(fgbg(cInk, cWhite), fmt.Sprintf("%d ", i+1))
				l.put(fgbg(cInk, cWhite), mark+clipWidth(topic, maxInt(0, mw-l.w-2)))
				l.put(fgbg(cMuted, cWhite), "  "+clipWidth(meta, maxInt(0, mw-l.w-4)))
			}))
		}
	}
	out = append(out, band(mw, bgc(cWhite), nil))
	out = append(out, modalHint(mw, "↑↓ choose · enter open · esc close"))
	out = append(out, modalBottom(mw))
	return out
}

func (a *App) renderTranscript(w, h int) []string {
	var all []string
	first := true
	for _, b := range a.blocks {
		if b.kind == bReasoning && !a.showReasoning {
			continue
		}
		if !first {
			all = append(all, blankLine(w))
		}
		first = false
		all = append(all, a.blockLines(b, w)...)
	}
	a.maxScroll = len(all) - h
	if a.maxScroll < 0 {
		a.maxScroll = 0
	}
	if a.scroll > a.maxScroll {
		a.scroll = a.maxScroll
	}
	end := len(all) - a.scroll
	if end < 0 {
		end = 0
	}
	start := end - h
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, h)
	out = append(out, all[start:end]...)
	for len(out) < h {
		out = append(out, blankLine(w))
	}
	if len(out) > h {
		out = out[len(out)-h:]
	}
	return out
}

func (a *App) blockLines(b *Block, w int) []string {
	if b.cacheW == w && b.cacheExp == a.expanded && b.cacheLen == len(b.text) &&
		b.cacheMeta == b.meta && b.cacheState == b.streaming {
		return b.cache
	}
	var bg, labelCol, bodyCol rgb
	dim := false
	switch b.kind {
	case bUser:
		bg, labelCol, bodyCol = cUserBG, cBlueDeep, cInk
	case bAssistant:
		bg, labelCol, bodyCol = cCloud, cBlue, cInk
	case bReasoning:
		bg, labelCol, bodyCol = cCloud, cMuted, cMuted
		dim = true
	case bTool:
		bg, labelCol, bodyCol = cSky, cCoral, cBlueDeep
	case bDiff:
		bg, labelCol, bodyCol = cWhite, cBlueDeep, cInk
	case bResult:
		bg, labelCol, bodyCol = cWhite, cBlueDeep, cInk
	case bError:
		bg, labelCol, bodyCol = cWhite, cCoral, cCoral
	default:
		bg, labelCol, bodyCol = cCloud, cBlueDeep, cMuted
	}
	out := make([]string, 0, 16)
	out = append(out, band(w, bgc(bg), func(l *lw) {
		l.put(fgbg(labelCol, bg).boldOn(), "▌ ")
		l.put(fgbg(labelCol, bg).boldOn(), b.label)
		if b.meta != "" {
			avail := w - l.w - 2
			if avail > 2 {
				l.put(metaStyle(bg, b.meta), "  "+clipWidth(b.meta, avail-2))
			}
		}
		if b.streaming {
			if w-l.w > 3 {
				l.put(fgbg(cMuted, bg), "  …")
			}
		}
	}))

	text := strings.TrimRight(sanitizeText(b.text), "\n")
	if text == "" {
		b.setCache(out, w, a.expanded)
		return out
	}
	width := w - 4
	if width < 4 {
		width = 4
	}
	var lines []string
	if b.kind == bTool || b.kind == bResult || b.kind == bError || b.kind == bDiff {
		lines = hardWrap(text, width)
	} else {
		lines = wordWrap(text, width)
	}
	limit := 0
	switch b.kind {
	case bResult, bDiff:
		limit = 26
	case bReasoning:
		limit = 12
	case bError:
		limit = 40
	}
	if a.expanded && limit > 0 {
		limit = 2000
	}
	hidden := 0
	if limit > 0 && len(lines) > limit {
		hidden = len(lines) - limit
		lines = lines[:limit]
	}
	body := fgbg(bodyCol, bg)
	if dim {
		body = body.dimOn()
	}
	for _, line := range lines {
		st := body
		if b.kind == bDiff {
			switch {
			case strings.HasPrefix(line, "+ "):
				st = fgbg(cGreen, bg)
			case strings.HasPrefix(line, "- "):
				st = fgbg(cCoral, bg)
			}
		}
		out = append(out, band(w, bgc(bg), func(l *lw) {
			l.put(st, "  "+line)
		}))
	}
	if hidden > 0 {
		msg := fmt.Sprintf("… +%d lines (Ctrl+O to expand)", hidden)
		out = append(out, band(w, bgc(bg), func(l *lw) {
			l.put(fgbg(cMuted, bg), "  "+clipWidth(msg, width))
		}))
	}
	b.setCache(out, w, a.expanded)
	return out
}

func (b *Block) setCache(lines []string, w int, expanded bool) {
	b.cache = lines
	b.cacheW = w
	b.cacheExp = expanded
	b.cacheLen = len(b.text)
	b.cacheMeta = b.meta
	b.cacheState = b.streaming
}

func metaStyle(bg rgb, meta string) sty {
	m := strings.ToLower(meta)
	st := fgbg(cMuted, bg)
	switch {
	case strings.Contains(m, "denied"), strings.Contains(m, "error"),
		strings.Contains(m, "timeout"), strings.Contains(m, "canceled"),
		(strings.Contains(m, "exit=") && !strings.Contains(m, "exit=0")):
		st = fgbg(cCoral, bg).boldOn()
	case strings.Contains(m, "exit=0"), m == "ok", strings.HasPrefix(m, "+"):
		st = fgbg(cGreen, bg)
	}
	return st
}

func (a *App) renderInput(w int) []string {
	top := borderLine(w, "─")
	mid := band(w, bgc(cWhite), func(l *lw) {
		if a.approval != nil {
			l.put(fgbg(cCoral, cWhite).boldOn(), "› ")
			keys := "[y] yes  [n] no  [a] always"
			if w < 60 {
				keys = "[y/n/a]"
			}
			keysW := strWidth(keys)
			prefix := "allow " + a.approval.Kind + "?"
			availPrefix := w - l.w - keysW - 2
			if availPrefix < 4 {
				availPrefix = 0
			}
			if availPrefix > 0 {
				l.put(fgbg(cInk, cWhite).boldOn(), clipWidth(prefix, availPrefix))
			}
			detail := strings.ReplaceAll(sanitizeText(a.approval.Detail), "\n", " ")
			space := w - l.w - keysW - 2
			if space > 8 {
				l.put(fgbg(cMuted, cWhite), "  "+clipWidth(detail, space-2))
			}
			gap := w - l.w - keysW - 2
			if gap > 0 {
				l.fill(bgc(cWhite), gap)
			}
			l.put(fgbg(cBlue, cWhite).boldOn(), keys+" ")
			a.cursorCol = 1
			return
		}

		prompt := "› "
		promptSt := fgbg(cBlue, cWhite).boldOn()
		if a.ask != nil {
			prompt = "? "
			promptSt = fgbg(cYellow, cWhite).boldOn()
		}
		l.put(promptSt, prompt)
		if len(a.input) == 0 {
			base := l.w
			l.put(fgbg(cWhite, cBlue), " ")
			a.cursorCol = base + 1
			ph := "Ask NullCode to build something…  /help for commands"
			if a.busy {
				ph = "working… Ctrl+C stops the turn"
			}
			if a.palette != nil {
				ph = "command palette open — esc to close"
			}
			if a.ask != nil {
				ph = "type your answer · enter send"
			}
			l.put(fgbg(cMuted, cWhite), " "+clipWidth(ph, w-l.w-2))
			return
		}

		avail := w - 7
		if avail < 8 {
			avail = 8
		}
		start := 0
		if a.cursor > avail-1 {
			start = a.cursor - (avail - 1)
		}
		if start > 0 {
			avail--
		}
		end := start + avail
		if end > len(a.input) {
			end = len(a.input)
		}
		base := l.w
		prefixW := 0
		if start > 0 {
			l.put(fgbg(cMuted, cWhite), "…")
			prefixW = 1
		}
		for i := start; i < end; i++ {
			if i == a.cursor {
				a.cursorCol = base + prefixW + strWidth(string(a.input[start:i])) + 1
				l.put(fgbg(cWhite, cBlue), string(a.input[i]))
			} else {
				l.put(fgbg(cInk, cWhite), string(a.input[i]))
			}
		}
		if a.cursor == len(a.input) {
			a.cursorCol = base + prefixW + strWidth(string(a.input[start:end])) + 1
			l.put(fgbg(cWhite, cBlue), " ")
		} else if a.cursor == end && end < len(a.input) {
			a.cursorCol = base + prefixW + strWidth(string(a.input[start:end])) + 1
		}
		if end < len(a.input) {
			l.put(fgbg(cMuted, cWhite), "…")
		}
	})
	bottom := borderLine(w, "─")
	return []string{top, mid, bottom}
}

func borderLine(w int, ch string) string {
	l := &lw{}
	l.put(bgc(cCloud), " ")
	l.put(fgbg(cLine, cCloud), strings.Repeat(ch, maxInt(0, w-2)))
	l.put(bgc(cCloud), " ")
	return l.str()
}

func maskInput(s string) string {
	if s == "" {
		return ""
	}
	return strings.Repeat("•", strWidth(s))
}

// modalTop draws the dialog's top border with an embedded title.
func modalTop(mw int, title string) string {
	l := &lw{}
	tw := strWidth(title)
	l.put(fgbg(cBlue, cWhite), "╭─ ")
	l.put(fgbg(cBlue, cWhite).boldOn(), title)
	l.put(fgbg(cBlue, cWhite), " ")
	dash := mw - 5 - tw
	if dash < 0 {
		dash = 0
	}
	l.put(fgbg(cBlue, cWhite), strings.Repeat("─", dash))
	l.put(fgbg(cBlue, cWhite), "╮")
	return l.str()
}

func modalBottom(mw int) string {
	l := &lw{}
	l.put(fgbg(cBlue, cWhite), "╰"+strings.Repeat("─", maxInt(0, mw-2))+"╯")
	return l.str()
}

func modalHint(mw int, text string) string {
	return band(mw, bgc(cWhite), func(l *lw) {
		l.put(fgbg(cMuted, cWhite), "  "+clipWidth(text, maxInt(0, mw-4)))
	})
}

func modalField(mw int, label, value, placeholder string) string {
	return band(mw, bgc(cWhite), func(l *lw) {
		l.put(fgbg(cInk, cWhite).boldOn(), "  "+label+" ")
		if value != "" {
			l.put(fgbg(cInk, cWhite), clipWidth(value, maxInt(0, mw-l.w-2)))
		} else if placeholder != "" {
			l.put(fgbg(cMuted, cWhite), clipWidth(placeholder, maxInt(0, mw-l.w-2)))
		}
	})
}

// modalLines builds the /connect dialog content for the current step.
func (a *App) modalLines(mw int) []string {
	c := a.connect
	out := []string{modalTop(mw, "Connect provider")}
	out = append(out, band(mw, bgc(cWhite), func(l *lw) {
		l.put(fgbg(cMuted, cWhite), "  "+clipWidth("sign in — token, URL and model are saved", maxInt(0, mw-4)))
	}))
	out = append(out, band(mw, bgc(cWhite), nil))
	switch c.step {
	case 0:
		out = append(out, modalField(mw, "API token", maskInput(string(c.token)), "paste your token…"))
		if string(c.token) == "" && c.curKey != "" {
			out = append(out, modalHint(mw, "empty = keep your current token"))
		}
		out = append(out, band(mw, bgc(cWhite), nil))
		out = append(out, modalHint(mw, "enter continue · esc cancel"))
	case 1:
		out = append(out, modalField(mw, "Backend URL", string(c.url), "empty = "+c.curBase))
		out = append(out, band(mw, bgc(cWhite), nil))
		out = append(out, modalHint(mw, "enter continue · esc cancel"))
	case 2:
		if c.fetching {
			out = append(out, modalHint(mw, "fetching models…"))
		} else {
			base := c.base
			if base == "" {
				base = a.cfg.BaseURL
			}
			rows := modelRows(base, c.models, "")
			if len(rows) == 0 {
				out = append(out, modalHint(mw, "no models advertised — enter for manual entry"))
			} else {
				out = append(out, modalModelList(mw, rows, &c.sel, 8)...)
			}
		}
		out = append(out, band(mw, bgc(cWhite), nil))
		out = append(out, modalHint(mw, "↑↓ choose · enter select · esc cancel"))
	case 3:
		out = append(out, modalField(mw, "Model ID", string(c.custom), "type the model id…"))
		if c.status != "" {
			out = append(out, modalHint(mw, c.status))
		}
		out = append(out, band(mw, bgc(cWhite), nil))
		out = append(out, modalHint(mw, "enter connect · esc cancel"))
	}
	out = append(out, modalBottom(mw))
	return out
}

// functionsModal builds the /functions dialog: toggle tool packs.
func (a *App) functionsModal(mw int) []string {
	f := a.functions
	out := []string{modalTop(mw, "Functions")}
	out = append(out, band(mw, bgc(cWhite), func(l *lw) {
		l.put(fgbg(cMuted, cWhite), "  "+clipWidth("toggle tool packs for the agent", maxInt(0, mw-4)))
	}))
	out = append(out, band(mw, bgc(cWhite), nil))
	for i, it := range f.items {
		mark, markSt := "[ ]", fgbg(cMuted, cWhite)
		if f.on[it.Name] {
			mark, markSt = "[x]", fgbg(cGreen, cWhite)
		}
		i := i
		out = append(out, band(mw, bgc(cWhite), func(l *lw) {
			if i == f.sel {
				l.put(fgbg(cWhite, cBlue).boldOn(), "  ❯ ")
				l.put(fgbg(cWhite, cBlue).boldOn(), mark+" ")
				l.put(fgbg(cWhite, cBlue).boldOn(), clipWidth(it.Name, maxInt(0, mw-l.w-2)))
				l.put(fgbg(cBlueSoft, cBlue), "  "+clipWidth(it.Desc, maxInt(0, mw-l.w-4)))
			} else {
				l.put(fgbg(cMuted, cWhite), "    ")
				l.put(markSt, mark+" ")
				l.put(fgbg(cInk, cWhite), clipWidth(it.Name, maxInt(0, mw-l.w-2)))
				l.put(fgbg(cMuted, cWhite), "  "+clipWidth(it.Desc, maxInt(0, mw-l.w-4)))
			}
		}))
	}
	out = append(out, band(mw, bgc(cWhite), nil))
	out = append(out, modalHint(mw, "enter toggle · esc close"))
	out = append(out, modalBottom(mw))
	return out
}

// overlayRow paints a modal row centered on a full-width screen line.
func overlayRow(w int, row string, rowW, left int) string {
	l := &lw{}
	l.put(bgc(cCloud), strings.Repeat(" ", maxInt(0, left)))
	l.b.WriteString(row)
	l.w += rowW
	l.fill(bgc(cCloud), maxInt(0, w-left-rowW))
	return l.str()
}

func (a *App) renderFooter(w int) string {
	left := "enter send · ctrl+c stop · ctrl+d quit · pgup/pgdn scroll"
	if a.expanded {
		left = "enter send · ctrl+c stop · ctrl+o collapse · ctrl+d quit"
	}
	var right []seg
	sep := seg{" · ", fgbg(cMuted, cCloud)}
	if a.yolo {
		right = append(right, seg{"yolo", fgbg(cCoral, cCloud).boldOn()})
	}
	if !a.think {
		right = append(right, seg{"think off", fgbg(cMuted, cCloud)})
	}
	if a.agent != nil {
		used := a.agent.ContextSize()
		limit := a.ctxLimit()
		text := formatCount(int64(used)) + " ctx"
		st := fgbg(cMuted, cCloud)
		if limit > 0 {
			text = formatCount(int64(used)) + "/" + formatCount(int64(limit)) + " ctx"
			if used*10 >= limit*9 {
				st = fgbg(cCoral, cCloud).boldOn()
			}
		}
		right = append(right, seg{text, st})
	}
	right = append(right, seg{formatCount(a.tokens) + " tok", fgbg(cMuted, cCloud)})
	if a.turn > 0 {
		right = append(right, seg{fmt.Sprintf("turn %d", a.turn), fgbg(cMuted, cCloud)})
	}
	rightW := 0
	for i, s := range right {
		if i > 0 {
			rightW += strWidth(sep.text)
		}
		rightW += strWidth(s.text)
	}
	if rightW > w-8 {
		right = []seg{{text: clipWidth(formatCount(a.tokens)+" tok", w-8), st: fgbg(cMuted, cCloud)}}
		rightW = strWidth(right[0].text)
	}
	return band(w, bgc(cCloud), func(l *lw) {
		leftMax := w - rightW - 4
		if leftMax > 0 {
			l.put(fgbg(cMuted, cCloud), clipWidth(left, leftMax))
		}
		gap := w - l.w - rightW - 2
		if gap > 0 {
			l.fill(bgc(cCloud), gap)
		}
		for i, s := range right {
			if i > 0 {
				l.put(sep.st, sep.text)
			}
			l.put(s.st, s.text)
		}
		l.put(bgc(cCloud), " ")
	})
}

func formatCount(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
