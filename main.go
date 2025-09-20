package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/pprof/profile"
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

type PprofAnalysis struct {
	ProfileInfo PprofInfo    `json:"profile_info"`
	TopFuncs    []PprofFunc  `json:"top_functions"`
	Summary     PprofSummary `json:"summary"`
}

type PprofInfo struct {
	File     string `json:"file"`
	Type     string `json:"type"`
	Time     string `json:"time"`
	Duration string `json:"duration"`
	Samples  string `json:"samples"`
}

type PprofFunc struct {
	Name        string `json:"name"`
	FlatTime    string `json:"flat_time"`
	FlatPercent string `json:"flat_percent"`
	CumTime     string `json:"cum_time"`
	CumPercent  string `json:"cum_percent"`
}

type PprofSummary struct {
	TotalFunctions int               `json:"total_functions"`
	TopBottleneck  string            `json:"top_bottleneck"`
	MainCategories []CategorySummary `json:"main_categories"`
}

type CategorySummary struct {
	Category string `json:"category"`
	Percent  string `json:"percent"`
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
		{
			URI:         "pprotein://pprof",
			Name:        "Latest pprof Profile Analysis",
			Description: "Latest pprof profile analysis with performance breakdown",
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
	case "pprotein://pprof":
		return s.fetchLatestPprofProfile()
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

func (s *MCPServer) fetchLatestPprofProfile() (ResourceContent, error) {
	url := fmt.Sprintf("%s/api/pprof/data/latest", s.config.PproteinURL)
	resp, err := http.Get(url)
	if err != nil {
		return ResourceContent{}, fmt.Errorf("failed to call pprof API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ResourceContent{}, fmt.Errorf("pprof API returned status %d", resp.StatusCode)
	}

	pprofData, err := io.ReadAll(resp.Body)
	if err != nil {
		return ResourceContent{}, fmt.Errorf("error reading pprof data: %v", err)
	}

	analysis, err := s.analyzePprofData(pprofData)
	if err != nil {
		return ResourceContent{}, fmt.Errorf("failed to analyze pprof data: %v", err)
	}

	jsonData, err := json.Marshal(analysis)
	if err != nil {
		return ResourceContent{}, fmt.Errorf("failed to marshal analysis result: %v", err)
	}

	return ResourceContent{
		URI:      "pprotein://pprof",
		MimeType: "application/json",
		Text:     string(jsonData),
	}, nil
}

func (s *MCPServer) analyzePprofData(pprofData []byte) (*PprofAnalysis, error) {
	p, err := profile.ParseData(pprofData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pprof data: %v", err)
	}

	analysis := &PprofAnalysis{
		ProfileInfo: PprofInfo{
			File:     "profile",
			Type:     p.SampleType[0].Type,
			Duration: fmt.Sprintf("%.2fs", float64(p.DurationNanos)/1e9),
			Samples:  fmt.Sprintf("%d samples", len(p.Sample)),
		},
		TopFuncs: make([]PprofFunc, 0),
		Summary: PprofSummary{
			MainCategories: make([]CategorySummary, 0),
		},
	}

	if p.TimeNanos > 0 {
		analysis.ProfileInfo.Time = fmt.Sprintf("%d", p.TimeNanos/1e9)
	}

	flatMap := make(map[string]int64)
	cumMap := make(map[string]int64)
	appFlatMap := make(map[string]int64)
	appCumMap := make(map[string]int64)

	for _, sample := range p.Sample {
		if len(sample.Value) == 0 {
			continue
		}
		value := sample.Value[0]

		for i, loc := range sample.Location {
			if len(loc.Line) == 0 {
				continue
			}

			line := loc.Line[0]
			if line.Function == nil {
				continue
			}

			funcName := line.Function.Name

			if i == 0 {
				flatMap[funcName] += value
				if s.isApplicationCode(funcName) {
					appFlatMap[funcName] += value
				}
			}
			cumMap[funcName] += value
			if s.isApplicationCode(funcName) {
				appCumMap[funcName] += value
			}
		}
	}

	appTotalSamples := int64(0)
	for _, value := range appCumMap {
		appTotalSamples += value
	}

	if appTotalSamples == 0 {
		appTotalSamples = 1
	}

	type funcStat struct {
		name    string
		flat    int64
		cum     int64
		flatPct float64
		cumPct  float64
	}

	var appFuncs []funcStat
	for name, cum := range appCumMap {
		flat := appFlatMap[name]
		appFuncs = append(appFuncs, funcStat{
			name:    name,
			flat:    flat,
			cum:     cum,
			flatPct: float64(flat) * 100.0 / float64(appTotalSamples),
			cumPct:  float64(cum) * 100.0 / float64(appTotalSamples),
		})
	}

	for i := 0; i < len(appFuncs)-1; i++ {
		for j := i + 1; j < len(appFuncs); j++ {
			if appFuncs[i].cum < appFuncs[j].cum {
				appFuncs[i], appFuncs[j] = appFuncs[j], appFuncs[i]
			}
		}
	}

	maxFuncs := 10
	if len(appFuncs) < maxFuncs {
		maxFuncs = len(appFuncs)
	}

	for i := 0; i < maxFuncs; i++ {
		fn := appFuncs[i]
		analysis.TopFuncs = append(analysis.TopFuncs, PprofFunc{
			Name:        fn.name,
			FlatTime:    fmt.Sprintf("%d", fn.flat),
			FlatPercent: fmt.Sprintf("%.2f%%", fn.flatPct),
			CumTime:     fmt.Sprintf("%d", fn.cum),
			CumPercent:  fmt.Sprintf("%.2f%%", fn.cumPct),
		})
	}

	if len(analysis.TopFuncs) > 0 {
		analysis.Summary.TopBottleneck = analysis.TopFuncs[0].Name
	}
	analysis.Summary.TotalFunctions = len(appFuncs)

	s.categorizeApplicationPerformance(analysis, cumMap)

	return analysis, nil
}

func (s *MCPServer) isApplicationCode(funcName string) bool {
	if strings.HasPrefix(funcName, "main.") {
		return true
	}

	excludePatterns := []string{
		"runtime.",
		"syscall.",
		"internal/",
		"net/http.",
		"net/textproto.",
		"bufio.",
		"encoding/",
		"io.",
		"os.",
		"reflect.",
		"sync.",
		"time.",
		"context.",
		"crypto/",
		"github.com/go-chi/chi",
		"github.com/gin-gonic/gin",
		"github.com/labstack/echo",
		"github.com/gorilla/mux",
		"github.com/julienschmidt/httprouter",
		"github.com/gofiber/fiber",
		"github.com/valyala/fasthttp",
		"github.com/go-sql-driver/mysql",
		"github.com/jmoiron/sqlx",
		"database/sql",
		"internal/poll",
		"internal/runtime",
		"log.",
		"fmt.",
		"strings.",
		"strconv.",
		"sort.",
		"unicode/",
		"compress/",
		"hash/",
		"math/",
		"path/",
		"regexp.",
		"mime/",
		"net.",
		"github.com/golang/",
		"google.golang.org/",
		"gopkg.in/",
	}

	for _, pattern := range excludePatterns {
		if strings.HasPrefix(funcName, pattern) {
			return false
		}
	}

	if strings.Contains(funcName, "/vendor/") {
		return false
	}

	if !strings.Contains(funcName, ".") {
		return false
	}

	return true
}

func (s *MCPServer) categorizeApplicationPerformance(analysis *PprofAnalysis, allCumMap map[string]int64) {
	totalAllSamples := int64(0)
	for _, value := range allCumMap {
		totalAllSamples += value
	}

	if totalAllSamples == 0 {
		return
	}

	categories := make(map[string]float64)

	dbPatterns := []string{
		"database/sql",
		"github.com/go-sql-driver/mysql",
		"github.com/jmoiron/sqlx",
	}

	httpPatterns := []string{
		"net/http",
		"github.com/go-chi/chi",
	}

	systemPatterns := []string{
		"runtime.",
		"syscall.",
		"internal/",
	}

	for funcName, cum := range allCumMap {
		percent := float64(cum) * 100.0 / float64(totalAllSamples)

		matched := false
		for _, pattern := range dbPatterns {
			if strings.Contains(funcName, pattern) {
				categories["Database Operations"] += percent
				matched = true
				break
			}
		}

		if !matched {
			for _, pattern := range httpPatterns {
				if strings.Contains(funcName, pattern) {
					categories["HTTP Framework"] += percent
					matched = true
					break
				}
			}
		}

		if !matched {
			for _, pattern := range systemPatterns {
				if strings.Contains(funcName, pattern) {
					categories["System/Runtime"] += percent
					matched = true
					break
				}
			}
		}

		if !matched && strings.HasPrefix(funcName, "main.") {
			categories["Application Logic"] += percent
		}
	}

	for category, percent := range categories {
		if percent > 1.0 {
			analysis.Summary.MainCategories = append(analysis.Summary.MainCategories, CategorySummary{
				Category: category,
				Percent:  fmt.Sprintf("%.1f%%", percent),
			})
		}
	}
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
