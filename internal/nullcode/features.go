package nullcode

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// funcItem is one toggleable pack in the /functions menu.
type funcItem struct {
	Name string
	Desc string
}

// functionPacks are the toggleable tool packs shown in the /functions menu.
// done is always on and not listed.
var functionPacks = []funcItem{
	{"exec", "run shell commands"},
	{"read", "read files"},
	{"write", "write files"},
	{"patch", "edit files with exact replacement"},
	{"list", "list directories"},
	{"search", "search file contents"},
	{"todos", "TODO.md task list"},
	{"skills", "load instruction packs"},
	{"ask", "ask you questions mid-turn"},
	{"mcps", "MCP tool servers"},
}

// defaultFeatures are the packs enabled out of the box.
func defaultFeatures() []string {
	return []string{"exec", "read", "write", "patch"}
}

// normalizeFeatures builds the enabled set from a saved list; done is
// always on. An empty list means defaults.
func normalizeFeatures(list []string) map[string]bool {
	enabled := map[string]bool{}
	if len(list) == 0 {
		for _, f := range defaultFeatures() {
			enabled[f] = true
		}
	} else {
		for _, f := range list {
			enabled[strings.ToLower(strings.TrimSpace(f))] = true
		}
	}
	enabled["done"] = true
	return enabled
}

func (a *Agent) list(arg string) ActionResult {
	dir := strings.TrimSpace(unquotePath(arg))
	if dir == "" {
		dir = "."
	}
	path, err := a.resolve(dir)
	if err != nil {
		return ActionResult{Tool: "list", Arg: arg, Status: "error", Body: err.Error()}
	}
	st, err := os.Stat(path)
	if err != nil {
		return ActionResult{Tool: "list", Arg: arg, Status: "error", Body: err.Error()}
	}
	if !st.IsDir() {
		return ActionResult{Tool: "list", Arg: arg, Status: "error", Body: "not a directory"}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return ActionResult{Tool: "list", Arg: arg, Status: "error", Body: err.Error()}
	}
	var b strings.Builder
	for i, e := range entries {
		if i >= 500 {
			fmt.Fprintf(&b, "… +%d more entries\n", len(entries)-500)
			break
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
			fmt.Fprintf(&b, "%s\n", name)
			continue
		}
		if info, err := e.Info(); err == nil {
			fmt.Fprintf(&b, "%s (%d bytes)\n", name, info.Size())
		} else {
			fmt.Fprintf(&b, "%s\n", name)
		}
	}
	if b.Len() == 0 {
		return ActionResult{Tool: "list", Arg: arg, Status: "ok", Body: "(empty directory)"}
	}
	return ActionResult{Tool: "list", Arg: arg, Status: "ok", Body: strings.TrimRight(b.String(), "\n"), Emitted: true}
}

var searchSkipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "__pycache__": true}

func (a *Agent) search(arg string) ActionResult {
	rest := strings.TrimSpace(arg)
	pattern := ""
	if len(rest) >= 2 && (rest[0] == '"' || rest[0] == '\'') {
		q := rest[0]
		if i := strings.IndexByte(rest[1:], q); i >= 0 {
			pattern = rest[1 : 1+i]
			rest = strings.TrimSpace(rest[2+i:])
		}
	}
	if pattern == "" {
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return ActionResult{Tool: "search", Status: "error", Body: "usage: ⟦search⟧ <pattern> [path]"}
		}
		pattern = fields[0]
		rest = strings.Join(fields[1:], " ")
	}
	dir := strings.TrimSpace(rest)
	if dir != "" {
		dir = unquotePath(dir)
	} else {
		dir = "."
	}
	root := a.cfg.Root
	if dir != "." && dir != "" {
		p, err := a.resolve(dir)
		if err != nil {
			return ActionResult{Tool: "search", Arg: arg, Status: "error", Body: err.Error()}
		}
		st, err := os.Stat(p)
		if err != nil {
			return ActionResult{Tool: "search", Arg: arg, Status: "error", Body: err.Error()}
		}
		if !st.IsDir() {
			return ActionResult{Tool: "search", Arg: arg, Status: "error", Body: "search path must be a directory"}
		}
		root = p
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re, err = regexp.Compile(regexp.QuoteMeta(pattern))
		if err != nil {
			return ActionResult{Tool: "search", Arg: arg, Status: "error", Body: "bad pattern: " + err.Error()}
		}
	}
	var b strings.Builder
	matches, hits := 0, 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || matches >= 100 {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if searchSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > 512<<10 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if re.MatchString(line) {
				hits++
				if matches < 100 {
					rel, rerr := filepath.Rel(a.cfg.Root, path)
					if rerr != nil {
						rel = path
					}
					fmt.Fprintf(&b, "%s: %s\n", rel, strings.TrimSpace(line))
					matches++
					if b.Len() > 8000 {
						return filepath.SkipAll
					}
				}
			}
		}
		return nil
	})
	if b.Len() == 0 {
		return ActionResult{Tool: "search", Arg: arg, Status: "ok", Body: fmt.Sprintf("no matches for %q", pattern)}
	}
	body := strings.TrimRight(b.String(), "\n")
	if hits > matches {
		body += fmt.Sprintf("\n… (%d total matches, showing %d)", hits, matches)
	}
	return ActionResult{Tool: "search", Arg: arg, Status: "ok", Body: body, Emitted: true}
}

func (a *Agent) todoPath() string {
	return filepath.Join(a.cfg.Root, "TODO.md")
}

func (a *Agent) todo(arg string) ActionResult {
	path := a.todoPath()
	fields := strings.Fields(arg)
	op := "list"
	if len(fields) > 0 {
		op = strings.ToLower(fields[0])
	}
	var items []string
	if prev, err := os.ReadFile(path); err == nil {
		items = strings.Split(strings.TrimRight(string(prev), "\n"), "\n")
		if len(items) == 1 && items[0] == "" {
			items = nil
		}
	}
	switch op {
	case "add":
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(arg, fields[0]), " "))
		if text == "" {
			return ActionResult{Tool: "todo", Arg: arg, Status: "error", Body: "usage: ⟦todo⟧ add <text>"}
		}
		items = append(items, "- [ ] "+text)
	case "done", "undo":
		if len(fields) < 2 {
			return ActionResult{Tool: "todo", Arg: arg, Status: "error", Body: "usage: ⟦todo⟧ " + op + " <item number>"}
		}
		n := 0
		if _, err := fmt.Sscanf(fields[1], "%d", &n); err != nil || n < 1 || n > len(items) {
			return ActionResult{Tool: "todo", Arg: arg, Status: "error", Body: fmt.Sprintf("no item %s (1..%d)", fields[1], len(items))}
		}
		line := items[n-1]
		if op == "done" {
			items[n-1] = strings.Replace(line, "- [ ]", "- [x]", 1)
		} else {
			items[n-1] = strings.Replace(line, "- [x]", "- [ ]", 1)
		}
	case "clear":
		var keep []string
		for _, line := range items {
			if !strings.HasPrefix(strings.TrimSpace(line), "- [x]") {
				keep = append(keep, line)
			}
		}
		items = keep
	case "list":
		if len(items) == 0 {
			return ActionResult{Tool: "todo", Arg: arg, Status: "ok", Body: "(no todos)", Emitted: true}
		}
		return ActionResult{Tool: "todo", Arg: arg, Status: "ok", Body: strings.Join(items, "\n"), Emitted: true}
	default:
		return ActionResult{Tool: "todo", Arg: arg, Status: "error", Body: "usage: ⟦todo⟧ add|done|undo|list|clear"}
	}
	body := ""
	if len(items) > 0 {
		body = strings.Join(items, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return ActionResult{Tool: "todo", Arg: arg, Status: "error", Body: err.Error()}
	}
	return ActionResult{Tool: "todo", Arg: arg, Status: "ok", Body: fmt.Sprintf("TODO.md updated (%d items): %s", len(items), strings.Join(items, " | ")), Emitted: true}
}

func skillDirs(a *Agent) []string {
	dirs := []string{filepath.Join(a.cfg.Root, ".nullcode", "skills")}
	if dir, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(dir, "nullcode", "skills"))
	}
	return dirs
}

func (a *Agent) skill(arg string) ActionResult {
	name := strings.TrimSpace(unquotePath(arg))
	dirs := skillDirs(a)
	if name == "" {
		var found []string
		for _, dir := range dirs {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					found = append(found, strings.TrimSuffix(e.Name(), ".md"))
				}
			}
		}
		if len(found) == 0 {
			return ActionResult{Tool: "skill", Status: "error", Body: "no skills installed; add markdown files to .nullcode/skills/ or the nullcode config skills dir"}
		}
		sort.Strings(found)
		return ActionResult{Tool: "skill", Status: "ok", Body: "available skills: " + strings.Join(found, ", "), Emitted: true}
	}
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, name+".md"))
		if err != nil {
			continue
		}
		return ActionResult{Tool: "skill", Arg: name, Status: "ok",
			Body: "skill " + name + " loaded; follow these instructions:\n\n" + string(data), Emitted: true}
	}
	var avail []string
	for _, dir := range dirs {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					avail = append(avail, strings.TrimSuffix(e.Name(), ".md"))
				}
			}
		}
	}
	sort.Strings(avail)
	msg := "skill " + name + " not found"
	if len(avail) > 0 {
		msg += "; available: " + strings.Join(avail, ", ")
	}
	return ActionResult{Tool: "skill", Arg: name, Status: "error", Body: msg}
}
