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
	case "resources/templates/list":
		return handleResourcesTemplatesList(msg)
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
			ProtocolVersion: "2025-11-25",
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
		// ID list resources
		{
			URI:         "pprotein://httplog/list",
			Name:        "HTTP Log ID List",
			Description: "List of available HTTP log IDs",
			MimeType:    "application/json",
		},
		{
			URI:         "pprotein://slowlog/list",
			Name:        "Slow Log ID List",
			Description: "List of available slow log IDs",
			MimeType:    "application/json",
		},
		{
			URI:         "pprotein://pprof/list",
			Name:        "pprof Profile ID List",
			Description: "List of available pprof profile IDs",
			MimeType:    "application/json",
		},
		// Latest resources (convenience)
		{
			URI:         "pprotein://httplog/latest",
			Name:        "Latest HTTP Log",
			Description: "Latest HTTP access log",
			MimeType:    "application/json",
		},
		{
			URI:         "pprotein://slowlog/latest",
			Name:        "Latest Slow Log",
			Description: "Latest MySQL slow query log",
			MimeType:    "application/json",
		},
		{
			URI:         "pprotein://pprof/latest",
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

func handleResourcesTemplatesList(msg mcp.Message) *mcp.Message {
	resourceTemplates := []mcp.ResourceTemplate{
		{
			URITemplate: "pprotein://httplog/{id}",
			Name:        "HTTP Log by ID",
			Description: "HTTP access log for a specific snapshot ID",
			MimeType:    "application/json",
		},
		{
			URITemplate: "pprotein://slowlog/{id}",
			Name:        "Slow Log by ID",
			Description: "MySQL slow query log for a specific snapshot ID",
			MimeType:    "application/json",
		},
		{
			URITemplate: "pprotein://pprof/{id}",
			Name:        "pprof Profile by ID",
			Description: "pprof profile analysis for a specific snapshot ID",
			MimeType:    "application/json",
		},
	}

	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: map[string]interface{}{
			"resourceTemplates": resourceTemplates,
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
	// Parse URI: pprotein://{type}/{id_or_action}
	if !strings.HasPrefix(uri, "pprotein://") {
		return mcp.ResourceContent{}, fmt.Errorf("unknown resource: %s", uri)
	}

	path := strings.TrimPrefix(uri, "pprotein://")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) < 2 {
		return mcp.ResourceContent{}, fmt.Errorf("invalid resource URI format: %s", uri)
	}

	resourceType := parts[0]
	idOrAction := parts[1]

	switch resourceType {
	case "httplog":
		return handleHTTPLogResource(uri, idOrAction)
	case "slowlog":
		return handleSlowLogResource(uri, idOrAction)
	case "pprof":
		return handlePprofResource(uri, idOrAction)
	default:
		return mcp.ResourceContent{}, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

func handleHTTPLogResource(uri, idOrAction string) (mcp.ResourceContent, error) {
	switch idOrAction {
	case "list":
		return listHTTPLogIDs(uri)
	case "latest":
		return fetchHTTPLogByID(uri, "")
	default:
		return fetchHTTPLogByID(uri, idOrAction)
	}
}

func handleSlowLogResource(uri, idOrAction string) (mcp.ResourceContent, error) {
	switch idOrAction {
	case "list":
		return listSlowLogIDs(uri)
	case "latest":
		return fetchSlowLogByID(uri, "")
	default:
		return fetchSlowLogByID(uri, idOrAction)
	}
}

func handlePprofResource(uri, idOrAction string) (mcp.ResourceContent, error) {
	switch idOrAction {
	case "list":
		return listPprofIDs(uri)
	case "latest":
		return pprof.ProvidePprofProfile(config.PproteinURL, "")
	default:
		return pprof.ProvidePprofProfile(config.PproteinURL, idOrAction)
	}
}

func listHTTPLogIDs(uri string) (mcp.ResourceContent, error) {
	files, err := discoverFilesByType("httplog")
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to list httplog files: %v", err)
	}

	type idEntry struct {
		ID      string `json:"id"`
		ModTime string `json:"mod_time"`
	}
	ids := make([]idEntry, 0, len(files))
	for _, f := range files {
		ids = append(ids, idEntry{
			ID:      f.ID,
			ModTime: f.ModTime.Format(time.RFC3339),
		})
	}

	jsonData, _ := json.Marshal(ids)
	return mcp.ResourceContent{
		URI:      uri,
		MimeType: "application/json",
		Text:     string(jsonData),
	}, nil
}

func listSlowLogIDs(uri string) (mcp.ResourceContent, error) {
	files, err := discoverFilesByType("slowlog")
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to list slowlog files: %v", err)
	}

	type idEntry struct {
		ID      string `json:"id"`
		ModTime string `json:"mod_time"`
	}
	ids := make([]idEntry, 0, len(files))
	for _, f := range files {
		ids = append(ids, idEntry{
			ID:      f.ID,
			ModTime: f.ModTime.Format(time.RFC3339),
		})
	}

	jsonData, _ := json.Marshal(ids)
	return mcp.ResourceContent{
		URI:      uri,
		MimeType: "application/json",
		Text:     string(jsonData),
	}, nil
}

func listPprofIDs(uri string) (mcp.ResourceContent, error) {
	ids, err := pprof.ListPprofIDs(config.PproteinURL)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to list pprof profiles: %v", err)
	}

	jsonData, _ := json.Marshal(ids)
	return mcp.ResourceContent{
		URI:      uri,
		MimeType: "application/json",
		Text:     string(jsonData),
	}, nil
}

func fetchHTTPLogByID(uri, id string) (mcp.ResourceContent, error) {
	var filename string
	if id == "" {
		// Get latest
		files, err := discoverFilesByType("httplog")
		if err != nil {
			return mcp.ResourceContent{}, fmt.Errorf("failed to discover httplog files: %v", err)
		}
		if len(files) == 0 {
			return mcp.ResourceContent{}, fmt.Errorf("no httplog files found")
		}
		filename = files[0].Filename
	} else {
		filename = id + "-httplog.log"
	}

	data, err := fetchHTTPLogFromAPIByFilename(filename)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to fetch HTTP log: %v", err)
	}

	return mcp.ResourceContent{
		URI:      uri,
		MimeType: "application/json",
		Text:     data,
	}, nil
}

func fetchSlowLogByID(uri, id string) (mcp.ResourceContent, error) {
	var filename string
	if id == "" {
		// Get latest
		files, err := discoverFilesByType("slowlog")
		if err != nil {
			return mcp.ResourceContent{}, fmt.Errorf("failed to discover slowlog files: %v", err)
		}
		if len(files) == 0 {
			return mcp.ResourceContent{}, fmt.Errorf("no slowlog files found")
		}
		filename = files[0].Filename
	} else {
		filename = id + "-slowlog.log"
	}

	data, err := fetchSlowLogFromAPIByFilename(filename)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to fetch slow log: %v", err)
	}

	return mcp.ResourceContent{
		URI:      uri,
		MimeType: "application/json",
		Text:     data,
	}, nil
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
	return fetchHTTPLogFromAPIByFilename(httplogFiles[0].Filename)
}

func fetchHTTPLogFromAPIByFilename(filename string) (string, error) {
	url := fmt.Sprintf("%s/api/httplog/%s", config.PproteinURL, filename)
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
	return fetchSlowLogFromAPIByFilename(slowlogFiles[0].Filename)
}

func fetchSlowLogFromAPIByFilename(filename string) (string, error) {
	url := fmt.Sprintf("%s/api/slowlog/%s", config.PproteinURL, filename)
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
