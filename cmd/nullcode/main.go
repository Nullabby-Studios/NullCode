// Command nullcode is a light-theme terminal coding agent for the
// Nullabby 1.5 model family. It speaks the OpenAI-compatible chat API
// (llama.cpp llama-server, vLLM, LM Studio, Ollama /v1) and uses a
// one-line directive protocol instead of verbose tool JSON.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/nullabby/nullcode/internal/nullcode"
)

const version = "0.1.0"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func main() {
	var (
		base    = flag.String("base", env("NULLCODE_BASE_URL", ""), "OpenAI-compatible base URL (saved on first login)")
		apiKey  = flag.String("key", env("NULLCODE_API_KEY", ""), "bearer token (saved on first login)")
		model   = flag.String("model", env("NULLCODE_MODEL", ""), "model id (picked during first connect, or NULLCODE_MODEL)")
		root    = flag.String("root", env("NULLCODE_ROOT", ""), "project root (default: current directory)")
		temp    = flag.Float64("temp", 0.6, "sampling temperature")
		topP    = flag.Float64("top-p", 0.95, "nucleus sampling")
		maxTok  = flag.Int("max-tokens", 8192, "max tokens per model turn")
		steps   = flag.Int("steps", 32, "max tool steps per user turn")
		timeout = flag.Duration("timeout", 120*time.Second, "per-command timeout for exec")
		think   = flag.Bool("think", envBool("NULLCODE_THINK", true), "enable model reasoning (Qwen thinking)")
		yolo    = flag.Bool("yolo", envBool("NULLCODE_YOLO", false), "skip exec/write/patch approval prompts")
		ctxTok  = flag.Int("ctx", envInt("NULLCODE_CONTEXT", 0), "context limit in tokens (0 = use provider info, none otherwise)")
		showVer = flag.Bool("version", false, "print version")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVer {
		fmt.Println("nullcode " + version)
		return
	}
	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "nullcode: unexpected arguments:", flag.Args())
		flag.Usage()
		os.Exit(2)
	}

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nullcode:", err)
		os.Exit(1)
	}
	if *root == "" {
		*root = wd
	}

	cfg := nullcode.Config{
		BaseURL:        *base,
		APIKey:         *apiKey,
		Model:          *model,
		Temperature:    *temp,
		TopP:           *topP,
		MaxTokens:      *maxTok,
		MaxSteps:       *steps,
		Timeout:        *timeout,
		Think:          *think,
		AutoApprove:    *yolo,
		Root:           *root,
		ContextTokens:  *ctxTok,
		MaxResultChars: 24000,
		Version:        version,
	}
	if err := nullcode.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "nullcode:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stdout, `NullCode %s — a minimal coding agent for nullabby1.5.

Usage:
  nullcode [flags]

Flags:
  -base URL        OpenAI-compatible endpoint (default: saved connection, else https://nullaby.org/v1)
  -key TOKEN       bearer token (or NULLCODE_API_KEY; saved on first connect)
  -model ID        model id (or NULLCODE_MODEL; picked during first connect)
  -root DIR        project root (default: current directory)
  -temp F          sampling temperature (default 0.6)
  -top-p F         nucleus sampling (default 0.95)
  -max-tokens N    max tokens per model turn (default 8192)
  -steps N         max tool steps per user turn (default 32)
  -timeout D       per-command timeout (default 120s)
  -think=BOOL      enable Qwen thinking (default true)
  -yolo=BOOL       skip exec/write/patch approvals (default false)
  -ctx N           context budget in tokens (default 32768)
  -version         print version

Environment:
  NULLCODE_BASE_URL, NULLCODE_API_KEY, NULLCODE_MODEL, NULLCODE_ROOT,
  NULLCODE_THINK, NULLCODE_YOLO

First launch opens a connect window inside the app: paste your API token,
optionally set a URL (default https://nullaby.org/v1), then pick a model
from the ones your provider advertises. Everything is saved to your user
config (nullcode/config.json); /connect re-opens the window and /theme
switches light/dark. Flags and environment variables override the saved
connection for one session.

Action syntax the model emits (only assistant output is parsed):
  ⟦exec⟧ command
  ⟦read⟧ path
  ⟦write⟧ path
  <complete file>
  ⟦end⟧
  ⟦patch⟧ path
  <<<
  old text
  >>>
  new text
  ⟦end⟧
  ⟦done⟧

The TUI uses the Nullabby light palette: blue #203bb4, deep #12236f,
soft #dce7ff, sky #c9ecff, coral #ff7567, lime #dced89, yellow #ffd96e,
ink #171a24, cloud #f4f6fb.
`, version)
}
