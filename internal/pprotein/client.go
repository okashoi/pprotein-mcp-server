// Package pprotein calls the internal Web API of pprotein.
package pprotein

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Snapshot types, which are also the path segments of the pprotein API.
const (
	TypePprof   = "pprof"
	TypeHTTPLog = "httplog"
	TypeSlowLog = "slowlog"
	TypeMemo    = "memo"
)

// Snapshot statuses reported by pprotein.
const (
	StatusOK      = "ok"
	StatusFail    = "fail"
	StatusPending = "pending"
)

// URIScheme is the scheme of the MCP resource URIs this server serves.
const URIScheme = "pprotein"

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: http.DefaultClient,
	}
}

type Entry struct {
	Snapshot Snapshot
	Status   string
	// Message carries the processing result, and the text itself for a memo.
	Message string
}

type Snapshot struct {
	Type     string
	ID       string
	Datetime time.Time
	// Repository is nil for a memo, which pprotein stores without contacting a server.
	Repository *Repository
	GroupID    string `json:"GroupId"`
	Label      string
	URL        string
	Duration   int
}

// Repository is the HEAD the measured server declared via the X-Git-Repository header.
// pprotein leaves every field empty when the header is absent.
type Repository struct {
	Ref     string
	Hash    string
	Author  string
	Message string
	Remote  string
}

// ResourceURI builds an MCP resource URI out of its path segments.
func ResourceURI(segments ...string) string {
	return URIScheme + "://" + strings.Join(segments, "/")
}

// ListEntries returns the snapshots of the given type, newest first.
func (c *Client) ListEntries(snapshotType string) ([]Entry, error) {
	body, err := c.get("/api/" + snapshotType)
	if err != nil {
		return nil, err
	}

	var entries []Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("failed to decode %s list: %w", snapshotType, err)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Snapshot.Datetime.After(entries[j].Snapshot.Datetime)
	})

	return entries, nil
}

// LatestEntry returns the newest snapshot that has been collected and processed successfully.
func (c *Client) LatestEntry(snapshotType string) (Entry, error) {
	entries, err := c.ListEntries(snapshotType)
	if err != nil {
		return Entry{}, err
	}

	for _, entry := range entries {
		if entry.Status == StatusOK {
			return entry, nil
		}
	}

	return Entry{}, fmt.Errorf("no %s snapshot available", snapshotType)
}

// FetchProcessed returns the snapshot after the processor of its type; TSV for httplog and slowlog.
func (c *Client) FetchProcessed(snapshotType, id string) ([]byte, error) {
	return c.get(fmt.Sprintf("/api/%s/%s", snapshotType, id))
}

// FetchData returns the snapshot as collected.
func (c *Client) FetchData(snapshotType, id string) ([]byte, error) {
	return c.get(fmt.Sprintf("/api/%s/data/%s", snapshotType, id))
}

// PostMemo attaches a memo to a group. pprotein accepts it before storing it, so the memo
// is not readable the instant this returns.
func (c *Client) PostMemo(groupID, label, text string) error {
	body, err := json.Marshal(struct {
		GroupId string
		Label   string
		Text    string
	}{
		GroupId: groupID,
		Label:   label,
		Text:    text,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal memo: %w", err)
	}

	resp, err := c.httpClient.Post(c.baseURL+"/api/memo", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to call pprotein API /api/memo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pprotein API /api/memo returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	return nil
}

func (c *Client) get(path string) ([]byte, error) {
	resp, err := c.httpClient.Get(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("failed to call pprotein API %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pprotein API %s returned status %d", path, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response of %s: %w", path, err)
	}

	return body, nil
}
