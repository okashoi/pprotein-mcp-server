package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type MCPServer struct {
	config *Config
}

type Config struct {
	DataDir     string
	PproteinURL string
}

type PProteinFile struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Filename string    `json:"filename"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Path     string    `json:"path"`
}

type MCPMessage struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int        `json:"id,omitempty"`
	Method  string      `json:"method,omitempty"`
	Params  interface{} `json:"params,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
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

func NewMCPServer(config *Config) *MCPServer {
	return &MCPServer{
		config: config,
	}
}

func (s *MCPServer) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var msg MCPMessage
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

func (s *MCPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
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

		var msg MCPMessage
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

func (s *MCPServer) handleMessage(msg MCPMessage) *MCPMessage {
	switch msg.Method {
	case "initialize":
		return s.handleInitialize(msg)
	case "resources/list":
		return s.handleResourcesList(msg)
	case "resources/read":
		return s.handleResourcesRead(msg)
	default:
		return &MCPMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &MCPError{
				Code:    -32601,
				Message: "Method not found",
			},
		}
	}
}

func (s *MCPServer) handleInitialize(msg MCPMessage) *MCPMessage {
	return &MCPMessage{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: ServerCaps{
				Resources: map[string]interface{}{},
			},
			ServerInfo: ServerInfo{
				Name:    "pprotein-mcp-server",
				Version: "1.0.0",
			},
		},
	}
}

func (s *MCPServer) handleResourcesList(msg MCPMessage) *MCPMessage {
	resources := []Resource{
		{
			URI:         "pprotein://httplog",
			Name:        "Latest HTTP Log",
			Description: "Latest HTTP access log",
			MimeType:    "application/json",
		},
		{
			URI:         "pprotein://slowlog",
			Name:        "Latest Slow Log",
			Description: "Latest MySQL slow query log",
			MimeType:    "application/json",
		},
	}

	return &MCPMessage{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: map[string]interface{}{
			"resources": resources,
		},
	}
}

func (s *MCPServer) handleResourcesRead(msg MCPMessage) *MCPMessage {
	var params ReadResourceParams
	paramBytes, _ := json.Marshal(msg.Params)
	json.Unmarshal(paramBytes, &params)

	content, err := s.fetchResourceContent(params.URI)
	if err != nil {
		return &MCPMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &MCPError{
				Code:    -32000,
				Message: err.Error(),
			},
		}
	}

	return &MCPMessage{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: ReadResourceResult{
			Contents: []ResourceContent{content},
		},
	}
}

func (s *MCPServer) fetchResourceContent(uri string) (ResourceContent, error) {
	switch uri {
	case "pprotein://httplog":
		return s.fetchLatestHTTPLog()
	case "pprotein://slowlog":
		return s.fetchLatestSlowLog()
	default:
		return ResourceContent{}, fmt.Errorf("unknown resource: %s", uri)
	}
}

func (s *MCPServer) fetchLatestHTTPLog() (ResourceContent, error) {
	// Call pprotein web API to get HTTP log
	data, err := s.fetchHTTPLogFromAPI()
	if err != nil {
		return ResourceContent{}, fmt.Errorf("failed to fetch HTTP log stats from API: %v", err)
	}

	return ResourceContent{
		URI:      "pprotein://httplog",
		MimeType: "application/json",
		Text:     data,
	}, nil
}

func (s *MCPServer) fetchLatestSlowLog() (ResourceContent, error) {
	// Call pprotein web API to get slow log
	data, err := s.fetchSlowLogFromAPI()
	if err != nil {
		return ResourceContent{}, fmt.Errorf("failed to fetch slow log stats from API: %v", err)
	}

	return ResourceContent{
		URI:      "pprotein://slowlog",
		MimeType: "application/json",
		Text:     data,
	}, nil
}

func (s *MCPServer) discoverFilesByType(fileType string) ([]PProteinFile, error) {
	if s.config.DataDir == "" {
		return []PProteinFile{}, nil
	}

	var files []PProteinFile
	var pattern string

	// Define file patterns for each type
	switch fileType {
	case "httplog":
		pattern = "-httplog.log"
	case "slowlog":
		pattern = "-slowlog.log"
	default:
		return []PProteinFile{}, fmt.Errorf("unknown file type: %s", fileType)
	}

	err := filepath.WalkDir(s.config.DataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(path, pattern) {
			info, err := d.Info()
			if err != nil {
				return err
			}

			filename := filepath.Base(path)
			// Extract ID by removing the suffix pattern
			id := strings.TrimSuffix(filename, pattern)

			file := PProteinFile{
				ID:       id,
				Type:     fileType,
				Filename: filename,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Path:     path,
			}
			files = append(files, file)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to scan data directory: %v", err)
	}

	// Sort by base36 timestamp (newest first)
	for i := 0; i < len(files)-1; i++ {
		for j := i + 1; j < len(files); j++ {
			if s.compareBase36Timestamps(files[i].ID, files[j].ID) < 0 {
				files[i], files[j] = files[j], files[i]
			}
		}
	}

	return files, nil
}

func (s *MCPServer) compareBase36Timestamps(id1, id2 string) int {
	// Parse base36 timestamps and compare
	timestamp1, err1 := strconv.ParseInt(id1, 36, 64)
	timestamp2, err2 := strconv.ParseInt(id2, 36, 64)

	if err1 != nil || err2 != nil {
		// Fallback to string comparison if parsing fails
		return strings.Compare(id1, id2)
	}

	if timestamp1 > timestamp2 {
		return 1
	} else if timestamp1 < timestamp2 {
		return -1
	}
	return 0
}

func (s *MCPServer) fetchHTTPLogFromAPI() (string, error) {
	// Get the latest httplog file
	httplogFiles, err := s.discoverFilesByType("httplog")
	if err != nil {
		return "", fmt.Errorf("failed to discover httplog files: %v", err)
	}
	if len(httplogFiles) == 0 {
		return "", fmt.Errorf("no httplog files found")
	}

	// Use the latest file (first in sorted list)
	latestFile := httplogFiles[0]
	url := fmt.Sprintf("%s/api/httplog/%s", s.config.PproteinURL, latestFile.Filename)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to call pprotein API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pprotein API returned status %d", resp.StatusCode)
	}

	data := ""
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		data = data + "\n" + scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading HTTP log: %v", err)
	}

	return data, nil
}

func (s *MCPServer) fetchSlowLogFromAPI() (string, error) {
	// Get the latest slowlog file
	slowlogFiles, err := s.discoverFilesByType("slowlog")
	if err != nil {
		return "", fmt.Errorf("failed to discover slowlog files: %v", err)
	}
	if len(slowlogFiles) == 0 {
		return "", fmt.Errorf("no slowlog files found")
	}

	// Use the latest file (first in sorted list)
	latestFile := slowlogFiles[0]
	url := fmt.Sprintf("%s/api/slowlog/%s", s.config.PproteinURL, latestFile.Filename)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to call pprotein API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pprotein API returned status %d", resp.StatusCode)
	}

	data := ""
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		data = data + "\n" + scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading slow log: %v", err)
	}

	return data, nil
}

func main() {
	config := &Config{
		DataDir:     os.Getenv("DATA_DIR"),
		PproteinURL: os.Getenv("PPROTEIN_URL"),
	}

	if config.DataDir == "" {
		config.DataDir = "./data"
	}

	if config.PproteinURL == "" {
		config.PproteinURL = "http://localhost:9000"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "9999"
	}

	server := NewMCPServer(config)

	http.HandleFunc("/mcp", server.handleHTTP)

	fmt.Printf("MCP Server started on port %s\n", port)
	fmt.Printf("Configuration:\n")
	fmt.Printf("  Data Directory: %s\n", config.DataDir)
	fmt.Printf("  PProtein URL: %s\n", config.PproteinURL)
	fmt.Printf("HTTP endpoint: http://localhost:%s/mcp\n", port)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Printf("Server failed: %v\n", err)
		os.Exit(1)
	}
}
