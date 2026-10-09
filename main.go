package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/okashoi/pprotein-mcp-server/internal/diff"
	"github.com/okashoi/pprotein-mcp-server/internal/group"
	"github.com/okashoi/pprotein-mcp-server/internal/mcp"
	"github.com/okashoi/pprotein-mcp-server/internal/measure"
	"github.com/okashoi/pprotein-mcp-server/internal/pprof"
	"github.com/okashoi/pprotein-mcp-server/internal/pprotein"
)

var config = &Config{
	PproteinURL: os.Getenv("PPROTEIN_URL"),
	Port:        os.Getenv("PORT"),
}

type Config struct {
	PproteinURL string
	Port        string
}

var client *pprotein.Client

const (
	mimeTypeJSON = "application/json"
	// alp and slp aggregate into TSV, which pprotein passes through as-is.
	mimeTypeTSV = "text/tab-separated-values"
)

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
	case "tools/list":
		return handleToolsList(msg)
	case "tools/call":
		return handleToolsCall(msg)
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
				Tools:     map[string]interface{}{},
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
		// Benchmark run resources
		{
			URI:         "pprotein://group/list",
			Name:        "Benchmark Run List",
			Description: "List of benchmark runs, each with the commit hash the measured servers reported and the memos written for it",
			MimeType:    mimeTypeJSON,
		},
		{
			URI:         "pprotein://group/latest",
			Name:        "Latest Benchmark Run",
			Description: "Snapshots, commit hashes and memos of the most recent benchmark run",
			MimeType:    mimeTypeJSON,
		},
		// ID list resources
		{
			URI:         "pprotein://httplog/list",
			Name:        "HTTP Log ID List",
			Description: "List of available HTTP log IDs",
			MimeType:    mimeTypeJSON,
		},
		{
			URI:         "pprotein://slowlog/list",
			Name:        "Slow Log ID List",
			Description: "List of available slow log IDs",
			MimeType:    mimeTypeJSON,
		},
		{
			URI:         "pprotein://pprof/list",
			Name:        "pprof Profile ID List",
			Description: "List of available pprof profile IDs",
			MimeType:    mimeTypeJSON,
		},
		// Latest resources (convenience)
		{
			URI:         "pprotein://httplog/latest",
			Name:        "Latest HTTP Log",
			Description: "Latest HTTP access log",
			MimeType:    mimeTypeTSV,
		},
		{
			URI:         "pprotein://slowlog/latest",
			Name:        "Latest Slow Log",
			Description: "Latest MySQL slow query log",
			MimeType:    mimeTypeTSV,
		},
		{
			URI:         "pprotein://pprof/latest",
			Name:        "Latest pprof Profile Analysis",
			Description: "Latest pprof profile analysis with performance breakdown",
			MimeType:    mimeTypeJSON,
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
			URITemplate: "pprotein://group/{id}",
			Name:        "Benchmark Run by Group ID",
			Description: "Snapshots, commit hashes and memos of the benchmark run collected under a specific GroupId",
			MimeType:    mimeTypeJSON,
		},
		{
			URITemplate: "pprotein://httplog/{id}",
			Name:        "HTTP Log by ID",
			Description: "HTTP access log for a specific snapshot ID",
			MimeType:    mimeTypeTSV,
		},
		{
			URITemplate: "pprotein://slowlog/{id}",
			Name:        "Slow Log by ID",
			Description: "MySQL slow query log for a specific snapshot ID",
			MimeType:    mimeTypeTSV,
		},
		{
			URITemplate: "pprotein://pprof/{id}",
			Name:        "pprof Profile by ID",
			Description: "pprof profile analysis for a specific snapshot ID",
			MimeType:    mimeTypeJSON,
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

const (
	toolDiffGroups    = "diff_groups"
	toolAnnotateGroup = "annotate_group"
)

func handleToolsList(msg mcp.Message) *mcp.Message {
	tools := []mcp.Tool{
		{
			Name: toolDiffGroups,
			Description: "Compare two benchmark runs: the alp metrics per endpoint, the slp metrics per query " +
				"and the share of the pprof profile per function, each ranked by how much it moved between the " +
				"runs. The commit hashes and the memos of both runs are reported alongside, so the measurements " +
				"can be read against the change they were collected for.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"base": {
						Type:        "string",
						Description: `GroupId of the run to compare against, or "latest" for the most recent run. GroupIds are listed by the pprotein://group/list resource.`,
					},
					"target": {
						Type:        "string",
						Description: `GroupId of the run to compare, or "latest" for the most recent run.`,
					},
					"limit": {
						Type:        "integer",
						Description: "How many rows each comparison reports, by descending movement (default 20).",
					},
				},
				Required: []string{"base", "target"},
			},
		},
		{
			Name: toolAnnotateGroup,
			Description: "Record what a benchmark run measured, as a memo on the run: the change that was " +
				"benchmarked and the score it got. The annotation is what diff_groups and the group resources " +
				"report back, so a later comparison can be read against the change it came from. The commit " +
				"hash needs no recording; pprotein takes it from the measured servers.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"group": {
						Type:        "string",
						Description: `GroupId of the run to annotate. Defaults to "latest", the most recent run, which is the one a benchmark that has just finished collected. Confirm the run in the result, or pick a GroupId from the pprotein://group/list resource.`,
					},
					"comment": {
						Type:        "string",
						Description: "What was changed before this run, in the words the team would use.",
					},
					"score": {
						Type:        "number",
						Description: "Score the benchmarker reported for this run.",
					},
					"pass": {
						Type:        "boolean",
						Description: "Whether the benchmark run passed.",
					},
					"tags": {
						Type:        "array",
						Description: "Short labels to group runs by, such as the subsystem a change touched.",
						Items:       &mcp.Property{Type: "string"},
					},
				},
				Required: []string{"comment"},
			},
		},
	}

	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	}
}

func handleToolsCall(msg mcp.Message) *mcp.Message {
	var params mcp.CallToolParams
	paramBytes, _ := json.Marshal(msg.Params)
	if err := json.Unmarshal(paramBytes, &params); err != nil {
		return invalidParams(msg, "failed to parse call: %v", err)
	}

	switch params.Name {
	case toolDiffGroups:
		return callDiffGroups(msg, params)
	case toolAnnotateGroup:
		return callAnnotateGroup(msg, params)
	default:
		return invalidParams(msg, "unknown tool: %s", params.Name)
	}
}

func callDiffGroups(msg mcp.Message, params mcp.CallToolParams) *mcp.Message {
	var args struct {
		Base   string `json:"base"`
		Target string `json:"target"`
		Limit  int    `json:"limit"`
	}
	if err := parseArguments(params, &args); err != nil {
		return invalidParams(msg, "failed to parse arguments: %v", err)
	}
	if args.Base == "" || args.Target == "" {
		return invalidParams(msg, "both base and target are required")
	}

	result, err := diff.Groups(client, args.Base, args.Target, args.Limit)
	if err != nil {
		return toolError(msg, err)
	}

	return toolResult(msg, result)
}

// annotateResult reports the run a measure was attached to, so that a measure recorded
// against the wrong run can be noticed right away.
type annotateResult struct {
	GroupID  string          `json:"group_id"`
	Datetime time.Time       `json:"datetime"`
	URI      string          `json:"uri"`
	Commits  []group.Commit  `json:"commits"`
	Measure  measure.Measure `json:"measure"`
}

func callAnnotateGroup(msg mcp.Message, params mcp.CallToolParams) *mcp.Message {
	var args struct {
		Group   string   `json:"group"`
		Comment string   `json:"comment"`
		Score   *float64 `json:"score"`
		Pass    *bool    `json:"pass"`
		Tags    []string `json:"tags"`
	}
	if err := parseArguments(params, &args); err != nil {
		return invalidParams(msg, "failed to parse arguments: %v", err)
	}
	if strings.TrimSpace(args.Comment) == "" {
		return invalidParams(msg, "comment is required")
	}
	if args.Group == "" {
		args.Group = group.LatestGroupID
	}

	recorded := measure.Measure{
		Comment: strings.TrimSpace(args.Comment),
		Score:   args.Score,
		Pass:    args.Pass,
		Tags:    args.Tags,
	}

	annotated, err := group.Annotate(client, args.Group, recorded)
	if err != nil {
		return toolError(msg, err)
	}

	recorded.Schema = measure.Schema

	return toolResult(msg, annotateResult{
		GroupID:  annotated.GroupID,
		Datetime: annotated.Datetime,
		URI:      annotated.URI,
		Commits:  annotated.Commits,
		Measure:  recorded,
	})
}

func parseArguments(params mcp.CallToolParams, args interface{}) error {
	if len(params.Arguments) == 0 {
		return nil
	}

	return json.Unmarshal(params.Arguments, args)
}

func toolResult(msg mcp.Message, value interface{}) *mcp.Message {
	data, err := json.Marshal(value)
	if err != nil {
		return toolError(msg, err)
	}

	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  mcp.TextResult(string(data)),
	}
}

func toolError(msg mcp.Message, err error) *mcp.Message {
	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  mcp.ErrorResult(err),
	}
}

func invalidParams(msg mcp.Message, format string, args ...interface{}) *mcp.Message {
	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Error: &mcp.Error{
			Code:    -32602,
			Message: fmt.Sprintf(format, args...),
		},
	}
}

func fetchResourceContent(uri string) (mcp.ResourceContent, error) {
	// Parse URI: pprotein://{type}/{id_or_action}
	prefix := pprotein.URIScheme + "://"
	if !strings.HasPrefix(uri, prefix) {
		return mcp.ResourceContent{}, fmt.Errorf("unknown resource: %s", uri)
	}

	path := strings.TrimPrefix(uri, prefix)
	parts := strings.SplitN(path, "/", 2)

	if len(parts) < 2 {
		return mcp.ResourceContent{}, fmt.Errorf("invalid resource URI format: %s", uri)
	}

	resourceType := parts[0]
	idOrAction := parts[1]

	switch resourceType {
	case pprotein.TypeHTTPLog, pprotein.TypeSlowLog:
		return handleSnapshotResource(uri, resourceType, idOrAction)
	case pprotein.TypePprof:
		return handlePprofResource(uri, idOrAction)
	case group.ResourceType:
		return handleGroupResource(uri, idOrAction)
	default:
		return mcp.ResourceContent{}, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

func handleGroupResource(uri, idOrAction string) (mcp.ResourceContent, error) {
	switch idOrAction {
	case "list":
		return listGroups(uri)
	case "latest":
		latest, err := group.Latest(client)
		if err != nil {
			return mcp.ResourceContent{}, err
		}

		return jsonContent(uri, latest)
	default:
		found, err := group.Find(client, idOrAction)
		if err != nil {
			return mcp.ResourceContent{}, err
		}

		return jsonContent(uri, found)
	}
}

func listGroups(uri string) (mcp.ResourceContent, error) {
	groups, err := group.List(client)
	if err != nil {
		return mcp.ResourceContent{}, err
	}

	summaries := make([]*group.Summary, 0, len(groups))
	for _, g := range groups {
		summaries = append(summaries, g.Summary())
	}

	return jsonContent(uri, summaries)
}

func handleSnapshotResource(uri, snapshotType, idOrAction string) (mcp.ResourceContent, error) {
	switch idOrAction {
	case "list":
		return listSnapshots(uri, snapshotType)
	case "latest":
		return fetchProcessedSnapshot(uri, snapshotType, "")
	default:
		return fetchProcessedSnapshot(uri, snapshotType, idOrAction)
	}
}

func handlePprofResource(uri, idOrAction string) (mcp.ResourceContent, error) {
	switch idOrAction {
	case "list":
		return listSnapshots(uri, pprotein.TypePprof)
	case "latest":
		return pprof.Provide(client, uri, "")
	default:
		return pprof.Provide(client, uri, idOrAction)
	}
}

func listSnapshots(uri, snapshotType string) (mcp.ResourceContent, error) {
	entries, err := client.ListEntries(snapshotType)
	if err != nil {
		return mcp.ResourceContent{}, err
	}

	type listEntry struct {
		ID       string    `json:"id"`
		Datetime time.Time `json:"datetime"`
		Status   string    `json:"status"`
		Label    string    `json:"label,omitempty"`
		GroupID  string    `json:"group_id,omitempty"`
		URI      string    `json:"uri"`
	}

	list := make([]listEntry, 0, len(entries))
	for _, entry := range entries {
		list = append(list, listEntry{
			ID:       entry.Snapshot.ID,
			Datetime: entry.Snapshot.Datetime,
			Status:   entry.Status,
			Label:    entry.Snapshot.Label,
			GroupID:  entry.Snapshot.GroupID,
			URI:      pprotein.ResourceURI(snapshotType, entry.Snapshot.ID),
		})
	}

	return jsonContent(uri, list)
}

func fetchProcessedSnapshot(uri, snapshotType, id string) (mcp.ResourceContent, error) {
	if id == "" {
		latest, err := client.LatestEntry(snapshotType)
		if err != nil {
			return mcp.ResourceContent{}, err
		}
		id = latest.Snapshot.ID
	}

	data, err := client.FetchProcessed(snapshotType, id)
	if err != nil {
		return mcp.ResourceContent{}, err
	}

	return mcp.ResourceContent{
		URI:      uri,
		MimeType: mimeTypeTSV,
		Text:     string(data),
	}, nil
}

func jsonContent(uri string, value any) (mcp.ResourceContent, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to marshal resource content: %v", err)
	}

	return mcp.ResourceContent{
		URI:      uri,
		MimeType: mimeTypeJSON,
		Text:     string(data),
	}, nil
}

func main() {
	if config.PproteinURL == "" {
		config.PproteinURL = "http://localhost:9000"
	}

	if config.Port == "" {
		config.Port = "9999"
	}

	client = pprotein.NewClient(config.PproteinURL)

	fmt.Printf("MCP Server started on port %s\n", config.Port)
	fmt.Printf("Configuration:\n")
	fmt.Printf("  PProtein URL: %s\n", config.PproteinURL)
	fmt.Printf("HTTP endpoint: http://localhost:%s/mcp\n", config.Port)

	if err := mcp.ListenAndServe(":"+config.Port, handleMessage); err != nil {
		fmt.Printf("Server failed: %v\n", err)
		os.Exit(1)
	}
}
