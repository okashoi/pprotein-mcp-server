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

	"github.com/okashoi/pprotein-mcp-server/internal/mcp"
	"github.com/okashoi/pprotein-mcp-server/internal/pprof"
)

var config = &Config{
	DataDir:     os.Getenv("DATA_DIR"),
	PproteinURL: os.Getenv("PPROTEIN_URL"),
	Port:        os.Getenv("PORT"),
}

type Config struct {
	DataDir     string
	PproteinURL string
	Port        string
}

type PProteinFile struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Filename string    `json:"filename"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Path     string    `json:"path"`
}

func handleMessage(msg mcp.Message) *mcp.Message {
	switch msg.Method {
	case "initialize":
		return handleInitialize(msg)
	case "resources/list":
		return handleResourcesList(msg)
	case "resources/read":
		return handleResourcesRead(msg)
	default:
		return &mcp.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &mcp.Error{
				Code:    -32601,
				Message: "Method not found",
			},
		}
	}
}

func handleInitialize(msg mcp.Message) *mcp.Message {
	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: mcp.InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: mcp.ServerCaps{
				Resources: map[string]interface{}{},
			},
			ServerInfo: mcp.ServerInfo{
				Name:    "pprotein-mcp-server",
				Version: "1.0.0",
			},
		},
	}
}

func handleResourcesList(msg mcp.Message) *mcp.Message {
	resources := []mcp.Resource{
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

	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: map[string]interface{}{
			"resources": resources,
		},
	}
}

func handleResourcesRead(msg mcp.Message) *mcp.Message {
	var params mcp.ReadResourceParams
	paramBytes, _ := json.Marshal(msg.Params)
	json.Unmarshal(paramBytes, &params)

	content, err := fetchResourceContent(params.URI)
	if err != nil {
		return &mcp.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &mcp.Error{
				Code:    -32000,
				Message: err.Error(),
			},
		}
	}

	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: mcp.ReadResourceResult{
			Contents: []mcp.ResourceContent{content},
		},
	}
}

func fetchResourceContent(uri string) (mcp.ResourceContent, error) {
	switch uri {
	case "pprotein://httplog":
		return fetchLatestHTTPLog()
	case "pprotein://slowlog":
		return fetchLatestSlowLog()
	case "pprotein://pprof":
		return pprof.ProvideLatestPprofProfile(config.PproteinURL)
	default:
		return mcp.ResourceContent{}, fmt.Errorf("unknown resource: %s", uri)
	}
}

func fetchLatestHTTPLog() (mcp.ResourceContent, error) {
	// Call pprotein web API to get HTTP log
	data, err := fetchHTTPLogFromAPI()
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to fetch HTTP log stats from API: %v", err)
	}

	return mcp.ResourceContent{
		URI:      "pprotein://httplog",
		MimeType: "application/json",
		Text:     data,
	}, nil
}

func fetchLatestSlowLog() (mcp.ResourceContent, error) {
	// Call pprotein web API to get slow log
	data, err := fetchSlowLogFromAPI()
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to fetch slow log stats from API: %v", err)
	}

	return mcp.ResourceContent{
		URI:      "pprotein://slowlog",
		MimeType: "application/json",
		Text:     data,
	}, nil
}

func discoverFilesByType(fileType string) ([]PProteinFile, error) {
	if config.DataDir == "" {
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

	err := filepath.WalkDir(config.DataDir, func(path string, d fs.DirEntry, err error) error {
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
			if compareBase36Timestamps(files[i].ID, files[j].ID) < 0 {
				files[i], files[j] = files[j], files[i]
			}
		}
	}

	return files, nil
}

func compareBase36Timestamps(id1, id2 string) int {
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

func fetchHTTPLogFromAPI() (string, error) {
	// Get the latest httplog file
	httplogFiles, err := discoverFilesByType("httplog")
	if err != nil {
		return "", fmt.Errorf("failed to discover httplog files: %v", err)
	}
	if len(httplogFiles) == 0 {
		return "", fmt.Errorf("no httplog files found")
	}

	// Use the latest file (first in sorted list)
	latestFile := httplogFiles[0]
	url := fmt.Sprintf("%s/api/httplog/%s", config.PproteinURL, latestFile.Filename)
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

func fetchSlowLogFromAPI() (string, error) {
	// Get the latest slowlog file
	slowlogFiles, err := discoverFilesByType("slowlog")
	if err != nil {
		return "", fmt.Errorf("failed to discover slowlog files: %v", err)
	}
	if len(slowlogFiles) == 0 {
		return "", fmt.Errorf("no slowlog files found")
	}

	// Use the latest file (first in sorted list)
	latestFile := slowlogFiles[0]
	url := fmt.Sprintf("%s/api/slowlog/%s", config.PproteinURL, latestFile.Filename)
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
	if config.DataDir == "" {
		config.DataDir = "./data"
	}

	if config.PproteinURL == "" {
		config.PproteinURL = "http://localhost:9000"
	}

	if config.Port == "" {
		config.Port = "9999"
	}

	fmt.Printf("MCP Server started on port %s\n", config.Port)
	fmt.Printf("Configuration:\n")
	fmt.Printf("  Data Directory: %s\n", config.DataDir)
	fmt.Printf("  PProtein URL: %s\n", config.PproteinURL)
	fmt.Printf("HTTP endpoint: http://localhost:%s/mcp\n", config.Port)

	if err := mcp.ListenAndServe(":"+config.Port, handleMessage); err != nil {
		fmt.Printf("Server failed: %v\n", err)
		os.Exit(1)
	}
}
