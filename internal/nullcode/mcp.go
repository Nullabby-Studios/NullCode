package nullcode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// mcpServer is one entry of the nullcode MCP config file
// (nullcode/mcp.json): {"servers":{"name":{"command":"…","args":[…]}}}.
type mcpServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
}

func mcpConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "nullcode", "mcp.json")
	}
	return ""
}

func loadMCPServers() (map[string]mcpServer, string) {
	p := mcpConfigPath()
	if p == "" {
		return nil, ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return map[string]mcpServer{}, p
	}
	var cfg struct {
		Servers map[string]mcpServer `json:"servers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return map[string]mcpServer{}, p
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]mcpServer{}
	}
	return cfg.Servers, p
}

type mcpRPC struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// mcpRequest runs one JSON-RPC request against a stdio MCP server: it
// spawns the process, performs the initialize handshake, sends the request,
// reads the matching response, and shuts the server down.
func mcpRequest(srv mcpServer, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	cmd := exec.Command(srv.Command, srv.Args...)
	if len(srv.Env) > 0 {
		cmd.Env = append(os.Environ(), srv.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil
	prepareCmd(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp server did not start: %s", err.Error())
	}
	defer func() {
		_ = killCmd(cmd)
		_ = cmd.Wait()
	}()

	send := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		_, err = stdin.Write(b)
		return err
	}
	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 64*1024), 4*1024*1024)
	recv := func(id int) (json.RawMessage, error) {
		deadline := time.After(timeout)
		for {
			line := make(chan string, 1)
			go func() {
				if reader.Scan() {
					line <- reader.Text()
				} else {
					line <- ""
				}
			}()
			var text string
			select {
			case text = <-line:
			case <-deadline:
				return nil, fmt.Errorf("mcp %s timed out", method)
			}
			if text == "" {
				return nil, fmt.Errorf("mcp %s: server closed output", method)
			}
			var msg mcpRPC
			if err := json.Unmarshal([]byte(text), &msg); err != nil {
				continue
			}
			if msg.ID != id {
				continue
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("mcp %s: %s", method, msg.Error.Message)
			}
			return msg.Result, nil
		}
	}

	if err := send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "nullcode", "version": "0.1.0"},
		},
	}); err != nil {
		return nil, err
	}
	if _, err := recv(1); err != nil {
		return nil, err
	}
	_ = send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if err := send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": method, "params": params,
	}); err != nil {
		return nil, err
	}
	return recv(2)
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func mcpListTools(srv mcpServer, timeout time.Duration) ([]mcpTool, error) {
	result, err := mcpRequest(srv, "tools/list", map[string]any{}, timeout)
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

type mcpCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

func (a *Agent) mcp(arg string) ActionResult {
	servers, cfgPath := loadMCPServers()
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ActionResult{Tool: "mcp", Status: "error",
			Body: "no MCP servers configured; add them to " + cfgPath + " as {\"servers\":{\"name\":{\"command\":\"…\",\"args\":[…]}}}"}
	}
	fields := strings.Fields(arg)
	switch {
	case len(fields) == 0:
		var b strings.Builder
		for _, name := range names {
			tools, err := mcpListTools(servers[name], 15*time.Second)
			if err != nil {
				fmt.Fprintf(&b, "%s: error: %s\n", name, err.Error())
				continue
			}
			fmt.Fprintf(&b, "%s:\n", name)
			for _, t := range tools {
				if t.Description != "" {
					fmt.Fprintf(&b, "  %s — %s\n", t.Name, t.Description)
				} else {
					fmt.Fprintf(&b, "  %s\n", t.Name)
				}
			}
		}
		if b.Len() == 0 {
			return ActionResult{Tool: "mcp", Status: "error", Body: "no MCP tools available"}
		}
		return ActionResult{Tool: "mcp", Status: "ok", Body: strings.TrimRight(b.String(), "\n"), Emitted: true}
	case len(fields) == 1:
		name := fields[0]
		srv, ok := servers[name]
		if !ok {
			return ActionResult{Tool: "mcp", Arg: arg, Status: "error",
				Body: "unknown MCP server " + name + "; configured: " + strings.Join(names, ", ")}
		}
		tools, err := mcpListTools(srv, 15*time.Second)
		if err != nil {
			return ActionResult{Tool: "mcp", Arg: arg, Status: "error", Body: err.Error()}
		}
		var b strings.Builder
		for _, t := range tools {
			if t.Description != "" {
				fmt.Fprintf(&b, "%s — %s\n", t.Name, t.Description)
			} else {
				fmt.Fprintf(&b, "%s\n", t.Name)
			}
		}
		if b.Len() == 0 {
			return ActionResult{Tool: "mcp", Arg: arg, Status: "ok", Body: "(no tools)", Emitted: true}
		}
		return ActionResult{Tool: "mcp", Arg: arg, Status: "ok", Body: strings.TrimRight(b.String(), "\n"), Emitted: true}
	default:
		name, tool := fields[0], fields[1]
		srv, ok := servers[name]
		if !ok {
			return ActionResult{Tool: "mcp", Arg: arg, Status: "error",
				Body: "unknown MCP server " + name + "; configured: " + strings.Join(names, ", ")}
		}
		argsJSON := strings.Join(fields[2:], " ")
		params := map[string]any{"name": tool, "arguments": map[string]any{}}
		if argsJSON != "" {
			var callArgs map[string]any
			if err := json.Unmarshal([]byte(argsJSON), &callArgs); err != nil {
				return ActionResult{Tool: "mcp", Arg: arg, Status: "error",
					Body: "tool arguments must be a JSON object: " + err.Error()}
			}
			params["arguments"] = callArgs
		}
		result, err := mcpRequest(srv, "tools/call", params, 30*time.Second)
		if err != nil {
			return ActionResult{Tool: "mcp", Arg: arg, Status: "error", Body: err.Error()}
		}
		var out mcpCallResult
		if err := json.Unmarshal(result, &out); err != nil {
			return ActionResult{Tool: "mcp", Arg: arg, Status: "error", Body: "bad tool result: " + err.Error()}
		}
		var b strings.Builder
		for _, c := range out.Content {
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
		body := strings.TrimRight(b.String(), "\n")
		if body == "" {
			body = "(empty result)"
		}
		status := "ok"
		if out.IsError {
			status = "error"
		}
		return ActionResult{Tool: "mcp", Arg: arg, Status: status, Body: body, Emitted: true}
	}
}
