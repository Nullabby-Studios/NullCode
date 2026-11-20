package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamDedupesMirroredReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"We\",\"reasoning_content\":\"We\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\" need\",\"reasoning_content\":\" need\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	u := New(srv.URL, "")
	var got []string
	_, _, err := u.Stream(context.Background(), Options{}, func(kind, text string) {
		got = append(got, kind+":"+text)
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	// Mirrored reasoning fields must be emitted once, not twice.
	want := []string{"reasoning:We", "reasoning: need", "content:Done."}
	if len(got) != len(want) {
		t.Fatalf("deltas = %q", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("delta %d = %q, want %q", i, got[i], w)
		}
	}
}

func TestStreamReasoningOnlyField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// vLLM-style backends only fill "reasoning".
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"plan\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	u := New(srv.URL, "")
	var got []string
	_, _, err := u.Stream(context.Background(), Options{}, func(kind, text string) {
		got = append(got, kind+":"+text)
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	want := []string{"reasoning:plan", "content:answer"}
	if len(got) != len(want) {
		t.Fatalf("deltas = %q", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("delta %d = %q, want %q", i, got[i], w)
		}
	}
}

func TestContextHintFrom(t *testing.T) {
	if n := contextHintFrom([]byte(`{"id":"m1","max_context_length":65536}`)); n != 65536 {
		t.Fatalf("hint = %d", n)
	}
	if n := contextHintFrom([]byte(`{"id":"m1","meta":{"n_ctx_train":131072}}`)); n != 131072 {
		t.Fatalf("meta hint = %d", n)
	}
	if n := contextHintFrom([]byte(`{"id":"m1"}`)); n != 0 {
		t.Fatalf("no hint = %d", n)
	}
}
