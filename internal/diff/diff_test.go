package diff

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/okashoi/pprotein-mcp-server/internal/pprotein"
)

var snapshotLists = map[string]string{
	"/api/pprof": `[
		{"Snapshot": {"Type": "pprof", "ID": "ma-pprof.pb.gz", "Datetime": "2026-10-01T10:00:00Z",
			"Repository": {"Hash": "hash1"}, "GroupId": "run-1", "Label": "app1"}, "Status": "ok", "Message": "Ready"},
		{"Snapshot": {"Type": "pprof", "ID": "mb-pprof.pb.gz", "Datetime": "2026-10-02T10:00:00Z",
			"Repository": {"Hash": "hash2"}, "GroupId": "run-2", "Label": "app1"}, "Status": "ok", "Message": "Ready"},
		{"Snapshot": {"Type": "pprof", "ID": "mz-pprof.pb.gz", "Datetime": "2026-10-02T10:00:03Z",
			"Repository": {"Hash": "hash2"}, "GroupId": "run-2", "Label": "app9"}, "Status": "fail", "Message": "http error"}
	]`,
	"/api/httplog": `[
		{"Snapshot": {"Type": "httplog", "ID": "ma-httplog.log", "Datetime": "2026-10-01T10:00:01Z",
			"Repository": {"Hash": "hash1"}, "GroupId": "run-1", "Label": "app1"}, "Status": "ok", "Message": "Ready"},
		{"Snapshot": {"Type": "httplog", "ID": "ma2-httplog.log", "Datetime": "2026-10-01T10:00:02Z",
			"Repository": {"Hash": "hash1"}, "GroupId": "run-1", "Label": "app2"}, "Status": "ok", "Message": "Ready"},
		{"Snapshot": {"Type": "httplog", "ID": "mb-httplog.log", "Datetime": "2026-10-02T10:00:01Z",
			"Repository": {"Hash": "hash2"}, "GroupId": "run-2", "Label": "app1"}, "Status": "ok", "Message": "Ready"}
	]`,
	"/api/slowlog": `[
		{"Snapshot": {"Type": "slowlog", "ID": "ma-slowlog.log", "Datetime": "2026-10-01T10:00:03Z",
			"Repository": {"Hash": "hash1"}, "GroupId": "run-1", "Label": "db1"}, "Status": "ok", "Message": "Ready"},
		{"Snapshot": {"Type": "slowlog", "ID": "mb-slowlog.log", "Datetime": "2026-10-02T10:00:02Z",
			"Repository": {"Hash": "hash2"}, "GroupId": "run-2", "Label": "db1"}, "Status": "ok", "Message": "Ready"}
	]`,
	"/api/memo": `[
		{"Snapshot": {"Type": "memo", "ID": "ma-memo.log", "Datetime": "2026-10-01T10:10:00Z",
			"Repository": null, "GroupId": "run-1", "Label": "measure"}, "Status": "ok", "Message": "baseline"}
	]`,
}

// alp emits mixed-case headers, slp wraps the metric in the name of the value it measures.
var tsvSnapshots = map[string]string{
	"ma-httplog.log": "Count\tMethod\tUri\tSum\tAvg\tP99\n" +
		"120\tGET\t/api/posts\t36.0\t0.300\t0.800\n" +
		"60\tPOST\t/api/posts\t18.0\t0.300\t1.100\n",
	"mb-httplog.log": "Count\tMethod\tUri\tSum\tAvg\tP99\n" +
		"150\tGET\t/api/posts\t12.0\t0.080\t0.250\n" +
		"30\tGET\t/api/comments\t0.9\t0.030\t0.090\n",
	"ma-slowlog.log": "Count\tQuery\tSum(QueryTime)\tAvg(QueryTime)\tSum(RowsExamined)\n" +
		"120\tSELECT * FROM posts WHERE id = N\t30.0\t0.250\t120000\n",
	"mb-slowlog.log": "Count\tQuery\tSum(QueryTime)\tAvg(QueryTime)\tSum(RowsExamined)\n" +
		"120\tSELECT * FROM posts WHERE id = N\t3.0\t0.025\t120\n",
}

var profileSamples = map[string]map[string]int64{
	"ma-pprof.pb.gz": {"main.handlerA": 80, "main.handlerB": 20},
	"mb-pprof.pb.gz": {"main.handlerA": 20, "main.handlerB": 20, "main.handlerC": 60},
}

func profileData(t *testing.T, samples map[string]int64) []byte {
	t.Helper()

	names := make([]string, 0, len(samples))
	for name := range samples {
		names = append(names, name)
	}
	sort.Strings(names)

	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}},
		PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     1,
	}

	for i, name := range names {
		id := uint64(i + 1)
		fn := &profile.Function{ID: id, Name: name}
		loc := &profile.Location{ID: id, Line: []profile.Line{{Function: fn, Line: 1}}}

		p.Function = append(p.Function, fn)
		p.Location = append(p.Location, loc)
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{samples[name]},
		})
	}

	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	return buf.Bytes()
}

func newClient(t *testing.T) *pprotein.Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)

		if body, ok := snapshotLists[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, body)
			return
		}
		if body, ok := tsvSnapshots[name]; ok {
			io.WriteString(w, body)
			return
		}
		if samples, ok := profileSamples[name]; ok {
			w.Write(profileData(t, samples))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	return pprotein.NewClient(server.URL)
}

func TestGroupsRanksRowsByTheTotalMetric(t *testing.T) {
	result, err := Groups(newClient(t), "run-1", "run-2", DefaultLimit)
	if err != nil {
		t.Fatalf("Groups() returned error: %v", err)
	}

	if len(result.HTTPLog) != 1 {
		t.Fatalf("got %d httplog comparisons, want 1", len(result.HTTPLog))
	}

	httplog := result.HTTPLog[0]
	if httplog.Label != "app1" || httplog.BaseID != "ma-httplog.log" || httplog.TargetID != "mb-httplog.log" {
		t.Errorf("got comparison %+v, want the app1 snapshots paired", httplog)
	}
	if httplog.Metric != "Sum" {
		t.Errorf("got metric %s, want Sum", httplog.Metric)
	}
	if got, want := *httplog.Total.Delta, -41.1; !closeTo(got, want) {
		t.Errorf("got total delta %v, want %v", got, want)
	}

	wantKeys := []string{"GET /api/posts", "POST /api/posts", "GET /api/comments"}
	if len(httplog.Rows) != len(wantKeys) {
		t.Fatalf("got %d rows, want %d", len(httplog.Rows), len(wantKeys))
	}
	for i, want := range wantKeys {
		if httplog.Rows[i].Key != want {
			t.Errorf("row %d is %q, want %q", i, httplog.Rows[i].Key, want)
		}
	}

	if got, want := *httplog.Rows[0].Metrics["Sum"].Delta, -24.0; !closeTo(got, want) {
		t.Errorf("got Sum delta %v, want %v", got, want)
	}
	if _, ok := httplog.Rows[0].Metrics["5xx"]; ok {
		t.Error("a column absent from both snapshots is reported")
	}
}

func TestGroupsMarksRowsMissingFromARun(t *testing.T) {
	result, err := Groups(newClient(t), "run-1", "run-2", DefaultLimit)
	if err != nil {
		t.Fatalf("Groups() returned error: %v", err)
	}

	baseOnly := result.HTTPLog[0].Rows[1].Metrics["Sum"]
	if baseOnly.Target != nil || baseOnly.Delta != nil || baseOnly.Base == nil {
		t.Errorf("got %+v for a row only the base run has, want the target side null", baseOnly)
	}

	targetOnly := result.HTTPLog[0].Rows[2].Metrics["Sum"]
	if targetOnly.Base != nil || targetOnly.Delta != nil || targetOnly.Target == nil {
		t.Errorf("got %+v for a row only the target run has, want the base side null", targetOnly)
	}
}

func TestGroupsReadsTheSlowLogColumns(t *testing.T) {
	result, err := Groups(newClient(t), "run-1", "run-2", DefaultLimit)
	if err != nil {
		t.Fatalf("Groups() returned error: %v", err)
	}

	if len(result.SlowLog) != 1 {
		t.Fatalf("got %d slowlog comparisons, want 1", len(result.SlowLog))
	}

	slowlog := result.SlowLog[0]
	if slowlog.Metric != "Sum(QueryTime)" {
		t.Errorf("got metric %s, want Sum(QueryTime)", slowlog.Metric)
	}
	if got, want := *slowlog.Rows[0].Metrics["Sum(QueryTime)"].Delta, -27.0; !closeTo(got, want) {
		t.Errorf("got query time delta %v, want %v", got, want)
	}
	if got, want := *slowlog.Rows[0].Metrics["Sum(RowsExamined)"].Delta, -119880.0; !closeTo(got, want) {
		t.Errorf("got rows examined delta %v, want %v", got, want)
	}
}

func TestGroupsComparesFunctionShares(t *testing.T) {
	result, err := Groups(newClient(t), "run-1", "run-2", DefaultLimit)
	if err != nil {
		t.Fatalf("Groups() returned error: %v", err)
	}

	if len(result.Pprof) != 1 {
		t.Fatalf("got %d pprof comparisons, want 1", len(result.Pprof))
	}

	functions := map[string]FunctionDiff{}
	for _, fn := range result.Pprof[0].Functions {
		functions[fn.Name] = fn
	}

	if got, want := functions["main.handlerA"].DeltaCum, -60.0; !closeTo(got, want) {
		t.Errorf("got handlerA delta %v, want %v", got, want)
	}
	if got, want := functions["main.handlerC"].DeltaCum, 60.0; !closeTo(got, want) {
		t.Errorf("got handlerC delta %v, want %v", got, want)
	}
	if got := functions["main.handlerC"].BaseCum; got != 0 {
		t.Errorf("got %v for a function missing from the base profile, want 0", got)
	}
	if result.Pprof[0].Functions[0].Name != "main.handlerA" {
		t.Errorf("got %s first, want the function that moved most", result.Pprof[0].Functions[0].Name)
	}
}

func TestGroupsReportsWhatItLeftOut(t *testing.T) {
	result, err := Groups(newClient(t), "run-1", "run-2", DefaultLimit)
	if err != nil {
		t.Fatalf("Groups() returned error: %v", err)
	}

	reasons := map[string]string{}
	for _, unpaired := range result.Unpaired {
		reasons[unpaired.ID] = unpaired.Reason
	}

	if len(reasons) != 2 {
		t.Fatalf("got %d unpaired snapshots, want 2: %+v", len(reasons), result.Unpaired)
	}
	if reason, ok := reasons["ma2-httplog.log"]; !ok || reason == "" {
		t.Errorf("the base-only snapshot is not reported: %+v", result.Unpaired)
	}
	if reason := reasons["mz-pprof.pb.gz"]; reason != "the snapshot status is fail" {
		t.Errorf("got reason %q for a failed snapshot, want the status", reason)
	}
}

func TestGroupsLimitsRows(t *testing.T) {
	result, err := Groups(newClient(t), "run-1", "run-2", 1)
	if err != nil {
		t.Fatalf("Groups() returned error: %v", err)
	}

	httplog := result.HTTPLog[0]
	if len(httplog.Rows) != 1 || httplog.Truncated != 2 {
		t.Errorf("got %d rows and %d truncated, want 1 and 2", len(httplog.Rows), httplog.Truncated)
	}
	if got, want := *httplog.Total.Delta, -41.1; !closeTo(got, want) {
		t.Errorf("got total delta %v, want it summed over every row", got)
	}
}

func TestGroupsResolvesLatest(t *testing.T) {
	result, err := Groups(newClient(t), "run-1", LatestGroupID, DefaultLimit)
	if err != nil {
		t.Fatalf("Groups() returned error: %v", err)
	}

	if result.Target.GroupID != "run-2" {
		t.Errorf("got target %s, want the most recent run", result.Target.GroupID)
	}
}

func TestGroupsRejectsComparingARunWithItself(t *testing.T) {
	if _, err := Groups(newClient(t), "run-2", LatestGroupID, DefaultLimit); err == nil {
		t.Error("Groups() returned no error for a run compared with itself")
	}
}

func closeTo(got, want float64) bool {
	diff := got - want
	return diff < 0.0001 && diff > -0.0001
}
