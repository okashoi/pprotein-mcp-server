// Package group assembles pprotein snapshots into the benchmark runs they were collected for.
//
// pprotein offers no API listing the runs: a run is identifiable only through the GroupId
// that every snapshot collected by /api/group/collect shares. The commit hash of the
// measured server and the memos written for the run hang off those same snapshots, which is
// what ties a profile to the change that was benchmarked.
package group

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/okashoi/pprotein-mcp-server/internal/pprotein"
)

// ResourceType is the path segment of the MCP resource URIs this package serves.
const ResourceType = "group"

// Group is the set of snapshots collected for one benchmark run.
type Group struct {
	GroupID string `json:"group_id"`
	// Datetime is when the oldest snapshot of the group was collected.
	Datetime time.Time `json:"datetime"`
	URI      string    `json:"uri"`
	// Commits has one element per distinct HEAD the measured servers declared.
	Commits []Commit `json:"commits"`
	Memos   []Memo   `json:"memos"`
	Entries []Entry  `json:"entries"`
}

type Commit struct {
	Hash    string `json:"hash"`
	Ref     string `json:"ref,omitempty"`
	Author  string `json:"author,omitempty"`
	Message string `json:"message,omitempty"`
	Remote  string `json:"remote,omitempty"`
	// Labels names the snapshots that declared this commit.
	Labels []string `json:"labels,omitempty"`
}

type Memo struct {
	ID       string    `json:"id"`
	Datetime time.Time `json:"datetime"`
	Label    string    `json:"label,omitempty"`
	Text     string    `json:"text"`
}

type Entry struct {
	Type     string    `json:"type"`
	ID       string    `json:"id"`
	Datetime time.Time `json:"datetime"`
	Label    string    `json:"label,omitempty"`
	Status   string    `json:"status"`
	URI      string    `json:"uri"`
}

// Summary keeps only what identifies a run, for listing many groups at once.
type Summary struct {
	GroupID    string          `json:"group_id"`
	Datetime   time.Time       `json:"datetime"`
	URI        string          `json:"uri"`
	Commits    []CommitSummary `json:"commits"`
	Memos      []string        `json:"memos"`
	EntryTypes []string        `json:"entry_types"`
}

type CommitSummary struct {
	Hash    string `json:"hash"`
	Message string `json:"message,omitempty"`
}

// List returns the groups, newest first.
func List(client *pprotein.Client) ([]*Group, error) {
	byID := map[string]*Group{}

	for _, snapshotType := range []string{pprotein.TypePprof, pprotein.TypeHTTPLog, pprotein.TypeSlowLog, pprotein.TypeMemo} {
		entries, err := client.ListEntries(snapshotType)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			groupID := entry.Snapshot.GroupID
			// A snapshot collected outside /api/group/collect may carry no GroupId,
			// and is then reachable only through the resources of its own type.
			if groupID == "" {
				continue
			}

			g, ok := byID[groupID]
			if !ok {
				g = &Group{
					GroupID: groupID,
					URI:     pprotein.ResourceURI(ResourceType, groupID),
					Commits: []Commit{},
					Memos:   []Memo{},
					Entries: []Entry{},
				}
				byID[groupID] = g
			}
			g.add(entry)
		}
	}

	groups := make([]*Group, 0, len(byID))
	for _, g := range byID {
		g.sortContents()
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Datetime.After(groups[j].Datetime)
	})

	return groups, nil
}

// Find returns the group of the given GroupId.
func Find(client *pprotein.Client, groupID string) (*Group, error) {
	groups, err := List(client)
	if err != nil {
		return nil, err
	}

	for _, g := range groups {
		if g.GroupID == groupID {
			return g, nil
		}
	}

	return nil, fmt.Errorf("no such group: %s", groupID)
}

// Latest returns the group of the most recent benchmark run.
func Latest(client *pprotein.Client) (*Group, error) {
	groups, err := List(client)
	if err != nil {
		return nil, err
	}

	if len(groups) == 0 {
		return nil, fmt.Errorf("no group available")
	}

	return groups[0], nil
}

func (g *Group) Summary() *Summary {
	s := &Summary{
		GroupID:    g.GroupID,
		Datetime:   g.Datetime,
		URI:        g.URI,
		Commits:    make([]CommitSummary, 0, len(g.Commits)),
		Memos:      make([]string, 0, len(g.Memos)),
		EntryTypes: make([]string, 0, len(g.Entries)),
	}

	for _, commit := range g.Commits {
		s.Commits = append(s.Commits, CommitSummary{
			Hash:    commit.Hash,
			Message: commit.Message,
		})
	}
	for _, memo := range g.Memos {
		s.Memos = append(s.Memos, memo.Text)
	}
	for _, entry := range g.Entries {
		s.EntryTypes = appendUnique(s.EntryTypes, entry.Type)
	}

	return s
}

func (g *Group) add(entry pprotein.Entry) {
	snapshot := entry.Snapshot

	if snapshot.Type == pprotein.TypeMemo {
		g.Memos = append(g.Memos, Memo{
			ID:       snapshot.ID,
			Datetime: snapshot.Datetime,
			Label:    snapshot.Label,
			Text:     strings.TrimSpace(entry.Message),
		})
	} else {
		g.Entries = append(g.Entries, Entry{
			Type:     snapshot.Type,
			ID:       snapshot.ID,
			Datetime: snapshot.Datetime,
			Label:    snapshot.Label,
			Status:   entry.Status,
			URI:      pprotein.ResourceURI(snapshot.Type, snapshot.ID),
		})
	}

	g.addCommit(snapshot)

	if g.Datetime.IsZero() || snapshot.Datetime.Before(g.Datetime) {
		g.Datetime = snapshot.Datetime
	}
}

func (g *Group) addCommit(snapshot pprotein.Snapshot) {
	if snapshot.Repository == nil || snapshot.Repository.Hash == "" {
		return
	}

	for i, commit := range g.Commits {
		if commit.Hash == snapshot.Repository.Hash {
			g.Commits[i].Labels = appendUnique(commit.Labels, snapshot.Label)
			return
		}
	}

	g.Commits = append(g.Commits, Commit{
		Hash:    snapshot.Repository.Hash,
		Ref:     snapshot.Repository.Ref,
		Author:  snapshot.Repository.Author,
		Message: strings.TrimSpace(snapshot.Repository.Message),
		Remote:  snapshot.Repository.Remote,
		Labels:  appendUnique(nil, snapshot.Label),
	})
}

func (g *Group) sortContents() {
	sort.SliceStable(g.Entries, func(i, j int) bool {
		return g.Entries[i].Datetime.Before(g.Entries[j].Datetime)
	})
	sort.SliceStable(g.Memos, func(i, j int) bool {
		return g.Memos[i].Datetime.Before(g.Memos[j].Datetime)
	})
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}

	for _, v := range values {
		if v == value {
			return values
		}
	}

	return append(values, value)
}
