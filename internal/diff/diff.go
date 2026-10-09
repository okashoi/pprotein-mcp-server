// Package diff compares the snapshots of two benchmark runs.
//
// Snapshots are paired by Label, so a run of several servers is compared server by server.
// Rows are ranked by how much the total metric of their type moved, which is where the
// effect of a change shows up.
package diff

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/okashoi/pprotein-mcp-server/internal/group"
	"github.com/okashoi/pprotein-mcp-server/internal/pprof"
	"github.com/okashoi/pprotein-mcp-server/internal/pprotein"
)

// DefaultLimit caps how many rows each comparison reports.
const DefaultLimit = 20

type Result struct {
	Base   *group.Summary `json:"base"`
	Target *group.Summary `json:"target"`
	// Comparisons are grouped by snapshot type, one entry per Label the runs share.
	HTTPLog []*TableDiff   `json:"httplog"`
	SlowLog []*TableDiff   `json:"slowlog"`
	Pprof   []*ProfileDiff `json:"pprof"`
	// Unpaired lists the snapshots left out of the comparison.
	Unpaired []Unpaired `json:"unpaired"`
}

type TableDiff struct {
	Label    string `json:"label,omitempty"`
	BaseID   string `json:"base_id"`
	TargetID string `json:"target_id"`
	// Metric names the column the rows are ranked by, and that Total sums.
	Metric string     `json:"metric"`
	Total  MetricDiff `json:"total"`
	Rows   []Row      `json:"rows"`
	// Truncated counts the rows the limit left out.
	Truncated int `json:"truncated,omitempty"`
}

type Row struct {
	Key string `json:"key"`
	// Metrics is keyed by the alp or slp column name. A null side means the row is
	// absent from that run.
	Metrics map[string]MetricDiff `json:"metrics"`
}

type MetricDiff struct {
	Base   *float64 `json:"base"`
	Target *float64 `json:"target"`
	Delta  *float64 `json:"delta"`
}

type ProfileDiff struct {
	Label     string         `json:"label,omitempty"`
	BaseID    string         `json:"base_id"`
	TargetID  string         `json:"target_id"`
	Functions []FunctionDiff `json:"functions"`
	Truncated int            `json:"truncated,omitempty"`
}

// FunctionDiff compares the share of the profile a function accounts for. A function
// missing from a profile accounts for 0% of it.
type FunctionDiff struct {
	Name       string  `json:"name"`
	BaseCum    float64 `json:"base_cum_percent"`
	TargetCum  float64 `json:"target_cum_percent"`
	DeltaCum   float64 `json:"delta_cum_percent"`
	BaseFlat   float64 `json:"base_flat_percent"`
	TargetFlat float64 `json:"target_flat_percent"`
	DeltaFlat  float64 `json:"delta_flat_percent"`
}

type Unpaired struct {
	Type   string `json:"type"`
	Label  string `json:"label,omitempty"`
	ID     string `json:"id"`
	Run    string `json:"run"`
	Reason string `json:"reason"`
}

// tableSpec names the columns of the TSV each processor emits: alp for httplog, slp for
// slowlog. Columns the processor was configured not to emit are skipped.
type tableSpec struct {
	keyColumns    []string
	totalColumn   string
	metricColumns []string
}

var tableSpecs = map[string]tableSpec{
	pprotein.TypeHTTPLog: {
		keyColumns:    []string{"Method", "Uri"},
		totalColumn:   "Sum",
		metricColumns: []string{"Count", "Sum", "Avg", "P99", "Max", "5xx"},
	},
	pprotein.TypeSlowLog: {
		keyColumns:    []string{"Query"},
		totalColumn:   "Sum(QueryTime)",
		metricColumns: []string{"Count", "Sum(QueryTime)", "Avg(QueryTime)", "P99(QueryTime)", "Sum(RowsExamined)"},
	},
}

// Groups compares two benchmark runs, each named by its GroupId or by group.LatestGroupID.
func Groups(client *pprotein.Client, baseID, targetID string, limit int) (*Result, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}

	base, err := group.Resolve(client, baseID)
	if err != nil {
		return nil, err
	}

	target, err := group.Resolve(client, targetID)
	if err != nil {
		return nil, err
	}

	if base.GroupID == target.GroupID {
		return nil, fmt.Errorf("cannot compare group %s with itself", base.GroupID)
	}

	result := &Result{
		Base:     base.Summary(),
		Target:   target.Summary(),
		HTTPLog:  []*TableDiff{},
		SlowLog:  []*TableDiff{},
		Pprof:    []*ProfileDiff{},
		Unpaired: []Unpaired{},
	}

	for _, snapshotType := range []string{pprotein.TypeHTTPLog, pprotein.TypeSlowLog} {
		pairs, unpaired := pairEntries(base, target, snapshotType)
		result.Unpaired = append(result.Unpaired, unpaired...)

		for _, p := range pairs {
			tableDiff, err := diffTables(client, snapshotType, p, limit)
			if err != nil {
				return nil, err
			}

			if snapshotType == pprotein.TypeHTTPLog {
				result.HTTPLog = append(result.HTTPLog, tableDiff)
			} else {
				result.SlowLog = append(result.SlowLog, tableDiff)
			}
		}
	}

	pairs, unpaired := pairEntries(base, target, pprotein.TypePprof)
	result.Unpaired = append(result.Unpaired, unpaired...)

	for _, p := range pairs {
		profileDiff, err := diffProfiles(client, p, limit)
		if err != nil {
			return nil, err
		}
		result.Pprof = append(result.Pprof, profileDiff)
	}

	return result, nil
}

type pair struct {
	label  string
	base   group.Entry
	target group.Entry
}

func pairEntries(base, target *group.Group, snapshotType string) ([]pair, []Unpaired) {
	baseEntries, baseUnpaired := entriesByLabel(base, snapshotType, "base")
	targetEntries, targetUnpaired := entriesByLabel(target, snapshotType, "target")

	unpaired := append(baseUnpaired, targetUnpaired...)

	labels := make([]string, 0, len(baseEntries))
	for label := range baseEntries {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	pairs := make([]pair, 0, len(labels))
	for _, label := range labels {
		targetEntry, ok := targetEntries[label]
		if !ok {
			unpaired = append(unpaired, Unpaired{
				Type:   snapshotType,
				Label:  label,
				ID:     baseEntries[label].ID,
				Run:    "base",
				Reason: "the target run has no snapshot of this type under the same label",
			})
			continue
		}

		pairs = append(pairs, pair{label: label, base: baseEntries[label], target: targetEntry})
		delete(targetEntries, label)
	}

	for label, entry := range targetEntries {
		unpaired = append(unpaired, Unpaired{
			Type:   snapshotType,
			Label:  label,
			ID:     entry.ID,
			Run:    "target",
			Reason: "the base run has no snapshot of this type under the same label",
		})
	}

	return pairs, unpaired
}

func entriesByLabel(g *group.Group, snapshotType, run string) (map[string]group.Entry, []Unpaired) {
	entries := map[string]group.Entry{}
	unpaired := []Unpaired{}

	for _, entry := range g.Entries {
		if entry.Type != snapshotType {
			continue
		}

		if entry.Status != pprotein.StatusOK {
			unpaired = append(unpaired, Unpaired{
				Type:   snapshotType,
				Label:  entry.Label,
				ID:     entry.ID,
				Run:    run,
				Reason: fmt.Sprintf("the snapshot status is %s", entry.Status),
			})
			continue
		}

		// Entries are ordered by collection time, so a label collected twice into one
		// group is represented by its newest snapshot.
		entries[entry.Label] = entry
	}

	return entries, unpaired
}

func diffTables(client *pprotein.Client, snapshotType string, p pair, limit int) (*TableDiff, error) {
	spec := tableSpecs[snapshotType]

	baseTable, err := fetchTable(client, snapshotType, p.base.ID)
	if err != nil {
		return nil, err
	}

	targetTable, err := fetchTable(client, snapshotType, p.target.ID)
	if err != nil {
		return nil, err
	}

	baseRows, keys := baseTable.rowsByKey(spec)
	targetRows, targetKeys := targetTable.rowsByKey(spec)
	for _, key := range targetKeys {
		if _, ok := baseRows[key]; !ok {
			keys = append(keys, key)
		}
	}

	columns := make([]string, 0, len(spec.metricColumns))
	for _, column := range spec.metricColumns {
		if baseTable.has(column) || targetTable.has(column) {
			columns = append(columns, column)
		}
	}

	rows := make([]Row, 0, len(keys))
	for _, key := range keys {
		row := Row{Key: key, Metrics: map[string]MetricDiff{}}
		for _, column := range columns {
			row.Metrics[column] = metricDiff(baseRows[key], targetRows[key], column)
		}
		rows = append(rows, row)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		return math.Abs(delta(rows[i], spec.totalColumn)) > math.Abs(delta(rows[j], spec.totalColumn))
	})

	diff := &TableDiff{
		Label:    p.label,
		BaseID:   p.base.ID,
		TargetID: p.target.ID,
		Metric:   spec.totalColumn,
		Total:    totalDiff(baseRows, targetRows, spec.totalColumn),
	}

	if len(rows) > limit {
		diff.Truncated = len(rows) - limit
		rows = rows[:limit]
	}
	diff.Rows = rows

	return diff, nil
}

func diffProfiles(client *pprotein.Client, p pair, limit int) (*ProfileDiff, error) {
	baseShares, err := pprof.Shares(client, p.base.ID)
	if err != nil {
		return nil, err
	}

	targetShares, err := pprof.Shares(client, p.target.ID)
	if err != nil {
		return nil, err
	}

	byName := map[string]*FunctionDiff{}
	names := make([]string, 0, len(baseShares)+len(targetShares))

	for _, share := range baseShares {
		byName[share.Name] = &FunctionDiff{
			Name:     share.Name,
			BaseCum:  share.CumPercent,
			BaseFlat: share.FlatPercent,
		}
		names = append(names, share.Name)
	}

	for _, share := range targetShares {
		fn, ok := byName[share.Name]
		if !ok {
			fn = &FunctionDiff{Name: share.Name}
			byName[share.Name] = fn
			names = append(names, share.Name)
		}
		fn.TargetCum = share.CumPercent
		fn.TargetFlat = share.FlatPercent
	}

	functions := make([]FunctionDiff, 0, len(names))
	for _, name := range names {
		fn := byName[name]
		fn.DeltaCum = fn.TargetCum - fn.BaseCum
		fn.DeltaFlat = fn.TargetFlat - fn.BaseFlat
		functions = append(functions, *fn)
	}

	sort.SliceStable(functions, func(i, j int) bool {
		return math.Abs(functions[i].DeltaCum) > math.Abs(functions[j].DeltaCum)
	})

	diff := &ProfileDiff{
		Label:    p.label,
		BaseID:   p.base.ID,
		TargetID: p.target.ID,
	}

	if len(functions) > limit {
		diff.Truncated = len(functions) - limit
		functions = functions[:limit]
	}
	diff.Functions = functions

	return diff, nil
}

func metricDiff(baseRow, targetRow map[string]float64, column string) MetricDiff {
	var diff MetricDiff

	baseValue, baseOK := baseRow[normalize(column)]
	if baseOK {
		diff.Base = &baseValue
	}

	targetValue, targetOK := targetRow[normalize(column)]
	if targetOK {
		diff.Target = &targetValue
	}

	if baseOK && targetOK {
		change := targetValue - baseValue
		diff.Delta = &change
	}

	return diff
}

func totalDiff(baseRows, targetRows map[string]map[string]float64, column string) MetricDiff {
	baseTotal := 0.0
	for _, row := range baseRows {
		baseTotal += row[normalize(column)]
	}

	targetTotal := 0.0
	for _, row := range targetRows {
		targetTotal += row[normalize(column)]
	}

	change := targetTotal - baseTotal

	return MetricDiff{Base: &baseTotal, Target: &targetTotal, Delta: &change}
}

func delta(row Row, column string) float64 {
	metric, ok := row.Metrics[column]
	if !ok {
		return 0
	}

	target := 0.0
	if metric.Target != nil {
		target = *metric.Target
	}

	base := 0.0
	if metric.Base != nil {
		base = *metric.Base
	}

	return target - base
}

type table struct {
	columns map[string]int
	rows    [][]string
}

func fetchTable(client *pprotein.Client, snapshotType, id string) (*table, error) {
	data, err := client.FetchProcessed(snapshotType, id)
	if err != nil {
		return nil, err
	}

	t, err := parseTable(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s snapshot %s: %w", snapshotType, id, err)
	}

	return t, nil
}

func parseTable(data []byte) (*table, error) {
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	header := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			header = i
			break
		}
	}
	if header < 0 {
		return nil, fmt.Errorf("no header row")
	}

	t := &table{columns: map[string]int{}}
	for i, name := range strings.Split(lines[header], "\t") {
		t.columns[normalize(name)] = i
	}

	for _, line := range lines[header+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		t.rows = append(t.rows, strings.Split(line, "\t"))
	}

	return t, nil
}

func (t *table) has(column string) bool {
	_, ok := t.columns[normalize(column)]
	return ok
}

func (t *table) field(row []string, column string) (string, bool) {
	i, ok := t.columns[normalize(column)]
	if !ok || i >= len(row) {
		return "", false
	}

	return strings.TrimSpace(row[i]), true
}

// rowsByKey indexes the rows by their key columns, keeping the order of the table.
func (t *table) rowsByKey(spec tableSpec) (map[string]map[string]float64, []string) {
	byKey := map[string]map[string]float64{}
	keys := make([]string, 0, len(t.rows))

	for _, row := range t.rows {
		parts := make([]string, 0, len(spec.keyColumns))
		for _, column := range spec.keyColumns {
			if value, ok := t.field(row, column); ok {
				parts = append(parts, value)
			}
		}

		key := strings.Join(parts, " ")
		if key == "" {
			continue
		}

		values := map[string]float64{}
		for _, column := range spec.metricColumns {
			text, ok := t.field(row, column)
			if !ok {
				continue
			}
			if value, err := strconv.ParseFloat(text, 64); err == nil {
				values[normalize(column)] = value
			}
		}

		if _, ok := byKey[key]; !ok {
			keys = append(keys, key)
		}
		byKey[key] = values
	}

	return byKey, keys
}

func normalize(column string) string {
	return strings.ToLower(strings.TrimSpace(column))
}
