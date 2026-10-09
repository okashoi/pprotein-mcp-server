package group

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/okashoi/pprotein-mcp-server/internal/pprotein"
)

// Snapshot lists as pprotein serves them: two benchmark runs, plus a pprof snapshot
// collected without a GroupId.
var snapshotLists = map[string]string{
	"/api/pprof": `[
		{
			"Snapshot": {
				"Type": "pprof", "ID": "a-pprof.pb.gz", "Datetime": "2026-10-01T10:00:00Z",
				"Repository": {"Ref": "refs/heads/main", "Hash": "hash1", "Message": "add index\n"},
				"GroupId": "run-1", "Label": "app1"
			},
			"Status": "ok", "Message": "Ready"
		},
		{
			"Snapshot": {
				"Type": "pprof", "ID": "d-pprof.pb.gz", "Datetime": "2026-10-02T10:00:00Z",
				"Repository": {"Hash": "hash3"},
				"GroupId": "run-2", "Label": "app1"
			},
			"Status": "ok", "Message": "Ready"
		},
		{
			"Snapshot": {
				"Type": "pprof", "ID": "e-pprof.pb.gz", "Datetime": "2026-10-03T10:00:00Z",
				"Repository": {"Hash": "hash4"},
				"GroupId": "", "Label": "app1"
			},
			"Status": "ok", "Message": "Ready"
		}
	]`,
	"/api/httplog": `[
		{
			"Snapshot": {
				"Type": "httplog", "ID": "b-httplog.log", "Datetime": "2026-10-01T10:00:01Z",
				"Repository": {"Hash": "hash2"},
				"GroupId": "run-1", "Label": "app2"
			},
			"Status": "ok", "Message": "Ready"
		}
	]`,
	"/api/slowlog": `[
		{
			"Snapshot": {
				"Type": "slowlog", "ID": "f-slowlog.log", "Datetime": "2026-10-02T10:00:01Z",
				"Repository": {"Hash": "hash3"},
				"GroupId": "run-2", "Label": "db1"
			},
			"Status": "fail", "Message": "external process aborted"
		}
	]`,
	"/api/memo": `[
		{
			"Snapshot": {
				"Type": "memo", "ID": "c-memo.log", "Datetime": "2026-10-01T10:05:00Z",
				"Repository": null, "GroupId": "run-1", "Label": "measure"
			},
			"Status": "ok", "Message": "add index on reactions\n"
		},
		{
			"Snapshot": {
				"Type": "memo", "ID": "c2-memo.log", "Datetime": "2026-10-01T10:06:00Z",
				"Repository": null, "GroupId": "run-1", "Label": "measure"
			},
			"Status": "ok",
			"Message": "{\"schema\":\"pprotein-measure/1\",\"comment\":\"reactions に複合インデックスを追加\",\"score\":12345}"
		}
	]`,
}

func newClient(t *testing.T) *pprotein.Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := snapshotLists[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	return pprotein.NewClient(server.URL)
}

func TestListOrdersGroupsNewestFirst(t *testing.T) {
	groups, err := List(newClient(t))
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].GroupID != "run-2" || groups[1].GroupID != "run-1" {
		t.Errorf("got group order [%s %s], want [run-2 run-1]", groups[0].GroupID, groups[1].GroupID)
	}
}

func TestFindCollectsSnapshotsOfTheRun(t *testing.T) {
	g, err := Find(newClient(t), "run-1")
	if err != nil {
		t.Fatalf("Find() returned error: %v", err)
	}

	if want := "2026-10-01T10:00:00Z"; g.Datetime.Format("2006-01-02T15:04:05Z") != want {
		t.Errorf("got datetime %v, want %s", g.Datetime, want)
	}
	if g.URI != "pprotein://group/run-1" {
		t.Errorf("got uri %s, want pprotein://group/run-1", g.URI)
	}

	// The memo is reported as an annotation, not as a readable snapshot.
	if len(g.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(g.Entries))
	}
	if g.Entries[0].ID != "a-pprof.pb.gz" || g.Entries[1].ID != "b-httplog.log" {
		t.Errorf("got entries [%s %s], want them ordered by datetime", g.Entries[0].ID, g.Entries[1].ID)
	}
	if want := "pprotein://pprof/a-pprof.pb.gz"; g.Entries[0].URI != want {
		t.Errorf("got entry uri %s, want %s", g.Entries[0].URI, want)
	}

	if len(g.Memos) != 2 {
		t.Fatalf("got %d memos, want 2", len(g.Memos))
	}
	if want := "add index on reactions"; g.Memos[0].Text != want {
		t.Errorf("got memo text %q, want %q", g.Memos[0].Text, want)
	}
}

func TestFindParsesTheMeasuresAmongTheMemos(t *testing.T) {
	g, err := Find(newClient(t), "run-1")
	if err != nil {
		t.Fatalf("Find() returned error: %v", err)
	}

	if len(g.Measures) != 1 {
		t.Fatalf("got %d measures, want only the memo written in the schema", len(g.Measures))
	}
	if want := "reactions に複合インデックスを追加"; g.Measures[0].Comment != want {
		t.Errorf("got comment %q, want %q", g.Measures[0].Comment, want)
	}
	if g.Measures[0].Score == nil || *g.Measures[0].Score != 12345 {
		t.Errorf("got score %v, want 12345", g.Measures[0].Score)
	}
}

func TestFindReportsEveryDeclaredCommit(t *testing.T) {
	g, err := Find(newClient(t), "run-1")
	if err != nil {
		t.Fatalf("Find() returned error: %v", err)
	}

	if len(g.Commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(g.Commits))
	}
	if g.Commits[0].Hash != "hash1" || g.Commits[0].Message != "add index" {
		t.Errorf("got commit %+v, want hash1 with its message trimmed", g.Commits[0])
	}
	if len(g.Commits[0].Labels) != 1 || g.Commits[0].Labels[0] != "app1" {
		t.Errorf("got labels %v, want [app1]", g.Commits[0].Labels)
	}
}

func TestFindMergesSnapshotsOfTheSameCommit(t *testing.T) {
	g, err := Find(newClient(t), "run-2")
	if err != nil {
		t.Fatalf("Find() returned error: %v", err)
	}

	if len(g.Commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(g.Commits))
	}
	if len(g.Commits[0].Labels) != 2 {
		t.Errorf("got labels %v, want both app1 and db1", g.Commits[0].Labels)
	}
}

func TestFindRejectsUnknownGroup(t *testing.T) {
	if _, err := Find(newClient(t), "run-9"); err == nil {
		t.Error("Find() returned no error for an unknown group")
	}
}

func TestSummaryKeepsWhatIdentifiesTheRun(t *testing.T) {
	g, err := Find(newClient(t), "run-1")
	if err != nil {
		t.Fatalf("Find() returned error: %v", err)
	}

	s := g.Summary()

	if len(s.Commits) != 2 || s.Commits[0].Hash != "hash1" {
		t.Errorf("got commits %+v, want both hashes", s.Commits)
	}
	if len(s.Measures) != 1 {
		t.Errorf("got measures %+v, want the one written in the schema", s.Measures)
	}
	// A memo carrying a measure is reported as one, not twice.
	if len(s.Memos) != 1 || s.Memos[0] != "add index on reactions" {
		t.Errorf("got memos %v, want only the hand-written one", s.Memos)
	}
	if len(s.EntryTypes) != 2 || s.EntryTypes[0] != "pprof" || s.EntryTypes[1] != "httplog" {
		t.Errorf("got entry types %v, want [pprof httplog]", s.EntryTypes)
	}
}
