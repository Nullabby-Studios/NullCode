// Package upstream — streaming client for OpenAI-compatible backends
// (llama.cpp llama-server, Ollama /v1, vLLM, LM Studio).
package upstream

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nullabby/nullcode/internal/secure"
)

type Upstream struct {
	BaseURL string // e.g. http://127.0.0.1:8081/v1
	APIKey  string // optional bearer
	// TemplateKwargs, when non-nil, is merged into every chat request as
	// chat_template_kwargs (llama.cpp/vLLM jinja template variables) —
	// used to request closed-thinking prompts so instruction-tuned models
	// emit their answer instead of reasoning until the token budget dies.
	// Hub-supplied extras can never override it: the agent sets this.
	TemplateKwargs map[string]any
	// NoPromptCache forces cache_prompt:false on every request: llama.cpp
	// otherwise reuses KV cache slots across consecutive requests, and a
	// rogue operator dumping GPU memory or running crafted prefix-
	// collision prompts could recover fragments of a previous user's
	// prompt from unzeroed VRAM. Disabling prompt caching is the only
	// user-space control against that without patching llama.cpp.
	NoPromptCache bool
	HTTP          *http.Client
}

// SetTLS upgrades the transport to https with server certificate pinning
// (SPKI SHA-256) and an optional client certificate for mutual TLS. A
// localhost TCP backend can be hijacked by any local process binding the
// port — pinning the real server's certificate means a hijacker can
// neither answer nor receive a single prompt byte.
func (u *Upstream) SetTLS(pin, certFile, keyFile string) error {
	cfg := secure.PinnedTLSClientConfig(pin)
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return errors.New("upstream: BACKEND_CERT and BACKEND_KEY must be set together")
		}
		kp, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("upstream: client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{kp}
	}
	t, ok := u.HTTP.Transport.(*http.Transport)
	if !ok {
		return errors.New("upstream: unexpected transport")
	}
	t.TLSClientConfig = cfg
	return nil
}

// New builds a streaming client with a hardened transport: no redirects
// (a hostile backend must never be able to bounce prompts or the bearer
// key to another host), bounded dial/header phases (streaming itself is
// unbounded), and 8 MiB response-header body caps.
func New(base, key string) *Upstream {
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return &Upstream{
		BaseURL: base,
		APIKey:  key,
		HTTP: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return fmt.Errorf("upstream: redirect refused (backend must not redirect)")
			},
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
				MaxIdleConns:          16,
				IdleConnTimeout:       90 * time.Second,
			},
		},
	}
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is one OpenAI-style function call, either streamed from the
// backend or replayed in assistant history.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolDef is one native function-calling definition sent as "tools".
type ToolDef struct {
	Type     string   `json:"type"`
	Function ToolFunc `json:"function"`
}

type ToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type DeltaFn func(kind, text string) // kind: "content" | "reasoning"

type Options struct {
	Model           string
	Messages        []Message
	Temperature     float64
	TopP            float64
	MaxTokens       int
	ReasoningEffort string         // "minimal"|"low"|"medium"|"high"; sent as reasoning_effort when set
	Tools           []ToolDef      // native function-calling definitions ("tools")
	Extras          map[string]any // pass-through OpenAI fields (response_format, ...)
	StreamBody      io.Writer      // if set, raw SSE chunks mirrored (for /v1 passthrough)
}

// Models lists backend models (GET /models). It also extracts a context
// window hint from common provider fields ("max_context_length",
// "context_length", "context_window", "n_ctx", llama.cpp "meta") when the
// backend advertises one; 0 means no hint.
func (u *Upstream) Models(ctx context.Context) ([]string, int, error) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, u.BaseURL+"/models", nil)
	if err != nil {
		return nil, 0, err
	}
	if u.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+u.APIKey)
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("models: %s", resp.Status)
	}
	var out struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, err
	}
	var models []string
	hint := 0
	for _, raw := range out.Data {
		var m struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &m); err == nil && m.ID != "" {
			models = append(models, m.ID)
		}
		if n := contextHintFrom(raw); n > hint {
			hint = n
		}
	}
	return models, hint, nil
}

// contextHintFrom scans a model entry (and its nested "meta") for common
// context window fields.
func contextHintFrom(raw []byte) int {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0
	}
	for _, src := range []map[string]any{m, nestedMap(m, "meta"), nestedMap(m, "info")} {
		for _, key := range []string{"max_context_length", "context_length", "context_window", "n_ctx", "n_ctx_train"} {
			if n := numField(src, key); n > 0 {
				return n
			}
		}
	}
	return 0
}

func nestedMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key]; ok {
		if mm, ok := v.(map[string]any); ok {
			return mm
		}
	}
	return nil
}

func numField(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	if v, ok := m[key]; ok {
		if n, ok := v.(float64); ok && n > 0 && n < 1e9 {
			return int(n)
		}
	}
	return 0
}

func (u *Upstream) body(o Options, stream bool) (map[string]any, error) {
	msgs := make([]map[string]any, len(o.Messages))
	for i, m := range o.Messages {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		if m.Name != "" {
			msg["name"] = m.Name
		}
		msgs[i] = msg
	}
	b := map[string]any{
		"messages":    msgs,
		"temperature": o.Temperature,
		"top_p":       o.TopP,
	}
	if o.Model != "" {
		b["model"] = o.Model
	}
	if o.MaxTokens > 0 {
		b["max_tokens"] = o.MaxTokens
	}
	if o.ReasoningEffort != "" {
		b["reasoning_effort"] = o.ReasoningEffort
	}
	if len(o.Tools) > 0 {
		b["tools"] = o.Tools
	}
	if stream {
		b["stream"] = true
	}
	for k, v := range o.Extras {
		if k == "stream" || k == "stream_options" || k == "model" {
			continue
		}
		b[k] = v
	}
	if u.TemplateKwargs != nil {
		if _, exists := b["chat_template_kwargs"]; !exists {
			b["chat_template_kwargs"] = u.TemplateKwargs
		}
	}
	if u.NoPromptCache {
		b["cache_prompt"] = false // overrides hub-supplied extras
	}
	return b, nil
}

// Stream runs a streaming completion, invoking fn per delta. Returns usage
// and any native tool calls the model emitted.
func (u *Upstream) Stream(ctx context.Context, o Options, fn DeltaFn) (Usage, []ToolCall, error) {
	bodyMap, err := u.body(o, true)
	if err != nil {
		return Usage{}, nil, err
	}
	raw, _ := json.Marshal(bodyMap)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Usage{}, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if u.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+u.APIKey)
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return Usage{}, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Usage{}, nil, fmt.Errorf("upstream %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	var usage Usage
	var acc strings.Builder
	var reasonAcc strings.Builder
	type tcAcc struct {
		call ToolCall
		args strings.Builder
	}
	tcalls := map[int]*tcAcc{}
	var tcOrder []int
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil && chunk.Usage.TotalTokens > 0 {
			usage = *chunk.Usage
		}
		for _, ch := range chunk.Choices {
			// Some gateways mirror the same thinking text into both
			// "reasoning_content" and "reasoning"; prefer the first and
			// skip the duplicate or every delta would be emitted twice.
			if txt := ch.Delta.ReasoningContent; txt != "" {
				reasonAcc.WriteString(txt)
				if fn != nil {
					fn("reasoning", txt)
				}
			} else if txt := ch.Delta.Reasoning; txt != "" {
				reasonAcc.WriteString(txt)
				if fn != nil {
					fn("reasoning", txt)
				}
			}
			if txt := ch.Delta.Content; txt != "" {
				acc.WriteString(txt)
				if fn != nil {
					fn("content", txt)
				}
			}
			for _, tcd := range ch.Delta.ToolCalls {
				a := tcalls[tcd.Index]
				if a == nil {
					a = &tcAcc{}
					tcalls[tcd.Index] = a
					tcOrder = append(tcOrder, tcd.Index)
				}
				if tcd.ID != "" {
					a.call.ID = tcd.ID
				}
				if tcd.Type != "" {
					a.call.Type = tcd.Type
				}
				if tcd.Function.Name != "" {
					a.call.Function.Name = tcd.Function.Name
				}
				a.args.WriteString(tcd.Function.Arguments)
			}
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return usage, nil, err
	}
	var calls []ToolCall
	for _, idx := range tcOrder {
		a := tcalls[idx]
		if a.call.ID == "" {
			a.call.ID = fmt.Sprintf("call_%d", idx)
		}
		if a.call.Type == "" {
			a.call.Type = "function"
		}
		a.call.Function.Arguments = a.args.String()
		calls = append(calls, a.call)
	}
	if usage.TotalTokens == 0 {
		usage.CompletionTokens = int64(len(acc.String()) / 4)
	}
	usage.CompletionTokens += int64(len(reasonAcc.String()) / 4)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage, calls, nil
}

// Probe checks whether the backend responds.
func (u *Upstream) Healthy(ctx context.Context) bool {
	_, _, err := u.Models(ctx)
	return err == nil
}
