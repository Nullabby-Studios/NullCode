package nullcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nullabby/nullcode/internal/upstream"
)

func TestCaptureWriterCaps(t *testing.T) {
	var out strings.Builder
	w := &captureWriter{limit: 8, displayLimit: 8, onOutput: func(s string) { out.WriteString(s) }}
	_, _ = w.Write([]byte("abcdefghij"))
	if w.total != 10 || w.buf.String() != "abcdefgh" {
		t.Fatalf("total=%d buf=%q", w.total, w.buf.String())
	}
	if !strings.Contains(out.String(), "truncated for display") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestInside(t *testing.T) {
	root := filepath.Clean(`C:\work\proj`)
	if !inside(root, filepath.Join(root, "a", "b")) {
		t.Fatal("child should be inside")
	}
	if inside(root, filepath.Clean(`C:\work\other`)) {
		t.Fatal("sibling should not be inside")
	}
	if inside(root, filepath.Clean(`C:\work\proj\..\escape`)) {
		t.Fatal("dotdot should not be inside")
	}
}

func TestLineDelta(t *testing.T) {
	add, rem := lineDelta("a\nb\nc\n", "a\nc\nd\n")
	if add != 1 || rem != 1 {
		t.Fatalf("add=%d rem=%d", add, rem)
	}
	if a, r := lineDelta("same", "same"); a != 0 || r != 0 {
		t.Fatalf("same file: %d %d", a, r)
	}
}

func TestTruncateMiddle(t *testing.T) {
	s := strings.Repeat("x", 1000)
	got := truncateMiddle(s, 100)
	if len(got) <= 100 {
		t.Fatalf("expected omission note, got %d bytes", len(got))
	}
	if !strings.Contains(got, "omitted") {
		t.Fatalf("missing note: %q", got)
	}
}

func TestSanitizeText(t *testing.T) {
	in := "\x1b[31mred\x1b[0m\r\nline\x00\x07"
	got := sanitizeText(in)
	if strings.ContainsAny(got, "\x1b\x00\x07") || strings.Contains(got, "\r") {
		t.Fatalf("control characters leaked: %q", got)
	}
	if got != "red\nline" {
		t.Fatalf("got %q", got)
	}
}

func TestActionResultEnvelope(t *testing.T) {
	e := ActionResult{Tool: "exec", Arg: "ls", Status: "exit=0", Body: "a.txt"}.envelope()
	for _, want := range []string{"result", "command: ls", "a.txt", "end"} {
		if !strings.Contains(e, want) {
			t.Fatalf("envelope missing %q: %q", want, e)
		}
	}
}

func TestAgentWritesFile(t *testing.T) {
	dir := t.TempDir()
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		parts := []string{
			"<thi", "nk>plan</think>", "Writing a file.\n",
			glyphOpen + "write" + glyphClose + " a.txt\n",
			"hello\n",
			endMarker + "\n",
			glyphOpen + "done" + glyphClose + "\n",
		}
		for _, p := range parts {
			b, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]string{"content": p}}},
			})
			fmt.Fprintf(w, "data: %s\n\n", b)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var events []Event
	cfg := Config{
		BaseURL:     srv.URL,
		Model:       "test",
		MaxSteps:    4,
		AutoApprove: true,
		Root:        dir,
		MaxTokens:   64,
		Think:       true,
	}
	a := NewAgent(cfg, func(e Event) { events = append(events, e) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.Run(ctx, "write a.txt")

	data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("file not written: %v (events %+v)", err, events)
	}
	if string(data) != "hello\n" {
		t.Fatalf("content = %q", data)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
	var sawReasoning, sawAction, sawDiff, sawDone bool
	for _, e := range events {
		switch e.Kind {
		case "reasoning":
			sawReasoning = true
		case "action":
			sawAction = true
		case "diff":
			sawDiff = true
			if !strings.Contains(e.Text, "+ hello") {
				t.Fatalf("diff body = %q", e.Text)
			}
		case "done":
			sawDone = true
		}
	}
	if !sawReasoning || !sawAction || !sawDiff || !sawDone {
		t.Fatalf("events missing: reason=%v action=%v diff=%v done=%v (%+v)", sawReasoning, sawAction, sawDiff, sawDone, events)
	}
}

func TestAgentMultiStepHistory(t *testing.T) {
	dir := t.TempDir()
	requests := 0
	var history []upstream.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 2 {
			var req struct {
				Messages []upstream.Message `json:"messages"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &req)
			history = req.Messages
		}
		w.Header().Set("Content-Type", "text/event-stream")
		parts := []string{"<think>secret plan</think>Checking.\n"}
		if requests == 1 {
			parts = append(parts, glyphOpen+"exec"+glyphClose+" echo hi\n")
		} else {
			parts = append(parts, glyphOpen+"done"+glyphClose+"\n")
		}
		for _, p := range parts {
			b, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]string{"content": p}}},
			})
			fmt.Fprintf(w, "data: %s\n\n", b)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := NewAgent(Config{
		BaseURL:     srv.URL,
		Model:       "test",
		MaxSteps:    4,
		AutoApprove: true,
		Root:        dir,
		MaxTokens:   128,
		Think:       true,
	}, func(Event) {})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.Run(ctx, "run echo")

	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
	if len(history) < 4 {
		t.Fatalf("history = %+v", history)
	}
	if history[2].Role != "assistant" {
		t.Fatalf("assistant history = %+v", history[2])
	}
	if !strings.Contains(history[2].Content, "exec echo hi") || !strings.Contains(history[2].Content, "Checking.") {
		t.Fatalf("action history = %q", history[2].Content)
	}
	if strings.Contains(history[2].Content, "secret plan") || strings.Contains(history[2].Content, "<think>") {
		t.Fatalf("reasoning leaked into history: %q", history[2].Content)
	}
	if history[3].Role != "user" || !strings.Contains(history[3].Content, "result exec exit=0") {
		t.Fatalf("result history = %+v", history[3])
	}
}

func TestAgentDeniedWrite(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		body := glyphOpen + "write" + glyphClose + " nope.txt\nx\n" + endMarker + "\n" + glyphOpen + "done" + glyphClose + "\n"
		b, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]string{"content": body}}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, Model: "test", MaxSteps: 2, Root: dir, MaxTokens: 64}
	a := NewAgent(cfg, func(e Event) {
		if e.Kind == "approval" && e.Approval != nil {
			e.Approval.Resp <- false
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.Run(ctx, "write")
	if _, err := os.Stat(filepath.Join(dir, "nope.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied write created file: %v", err)
	}
}
