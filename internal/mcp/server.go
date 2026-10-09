package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type HandleMessageFunc = func(msg Message) *Message

type server struct {
	handleMessage HandleMessageFunc
}

type Message struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int        `json:"id,omitempty"`
	Method  string      `json:"method,omitempty"`
	Params  interface{} `json:"params,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type InitializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      ClientInfo             `json:"clientInfo"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    ServerCaps `json:"capabilities"`
	ServerInfo      ServerInfo `json:"serverInfo"`
}

type ServerCaps struct {
	Resources map[string]interface{} `json:"resources"`
	Tools     map[string]interface{} `json:"tools,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ReadResourceParams struct {
	URI string `json:"uri"`
}

type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
}

type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type CallToolResult struct {
	Content []Content `json:"content"`
	// IsError reports a failure of the tool itself, as opposed to a malformed call.
	IsError bool `json:"isError,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TextResult carries a successful tool result.
func TextResult(text string) CallToolResult {
	return CallToolResult{Content: []Content{{Type: "text", Text: text}}}
}

// ErrorResult carries a tool failure back to the client, which shows it to the model.
func ErrorResult(err error) CallToolResult {
	return CallToolResult{
		Content: []Content{{Type: "text", Text: err.Error()}},
		IsError: true,
	}
}

func ListenAndServe(port string, handler HandleMessageFunc) error {
	mcpServer := &server{
		handleMessage: handler,
	}

	http.HandleFunc("/mcp", mcpServer.handleHTTP)

	return http.ListenAndServe(port, nil)
}

func (s *server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var msg Message
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&msg)
		if err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		response := s.handleMessage(msg)
		if response != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
		return
	}

	if r.Header.Get("Accept") == "text/event-stream" {
		s.handleSSE(w, r)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	scanner := bufio.NewScanner(r.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var msg Message
		err := json.Unmarshal([]byte(line), &msg)
		if err != nil {
			continue
		}

		response := s.handleMessage(msg)
		if response != nil {
			data, _ := json.Marshal(response)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
