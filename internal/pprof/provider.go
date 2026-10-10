package pprof

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
	"github.com/okashoi/pprotein-mcp-server/internal/mcp"
	"github.com/okashoi/pprotein-mcp-server/internal/pprotein"
)

type pprofAnalysis struct {
	ProfileInfo pprofInfo    `json:"profile_info"`
	TopFuncs    []pprofFunc  `json:"top_functions"`
	Summary     pprofSummary `json:"summary"`
}

type pprofInfo struct {
	File     string `json:"file"`
	Type     string `json:"type"`
	Time     string `json:"time"`
	Duration string `json:"duration"`
	Samples  string `json:"samples"`
}

type pprofFunc struct {
	Name        string `json:"name"`
	FlatTime    string `json:"flat_time"`
	FlatPercent string `json:"flat_percent"`
	CumTime     string `json:"cum_time"`
	CumPercent  string `json:"cum_percent"`
}

type pprofSummary struct {
	TotalFunctions int               `json:"total_functions"`
	TopBottleneck  string            `json:"top_bottleneck"`
	MainCategories []categorySummary `json:"main_categories"`
}

type categorySummary struct {
	Category string `json:"category"`
	Percent  string `json:"percent"`
}

// Provide analyzes the profile of the given snapshot, or of the latest one when id is empty.
func Provide(client *pprotein.Client, uri, id string) (mcp.ResourceContent, error) {
	if id == "" {
		latest, err := client.LatestEntry(pprotein.TypePprof)
		if err != nil {
			return mcp.ResourceContent{}, err
		}
		id = latest.Snapshot.ID
	}

	pprofData, err := client.FetchData(pprotein.TypePprof, id)
	if err != nil {
		return mcp.ResourceContent{}, err
	}

	analysis, err := analyzePprofData(pprofData)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to analyze pprof data: %v", err)
	}

	jsonData, err := json.Marshal(analysis)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to marshal analysis result: %v", err)
	}

	return mcp.ResourceContent{
		URI:      uri,
		MimeType: "application/json",
		Text:     string(jsonData),
	}, nil
}

// FunctionShare is the portion of a profile an application function accounts for.
type FunctionShare struct {
	Name string
	// FlatPercent and CumPercent are shares of every sample in the profile, so they stay
	// comparable between profiles of different length.
	FlatPercent float64
	CumPercent  float64
}

// Shares returns the application functions of a snapshot, by descending cumulative share.
func Shares(client *pprotein.Client, id string) ([]FunctionShare, error) {
	data, err := client.FetchData(pprotein.TypePprof, id)
	if err != nil {
		return nil, err
	}

	p, err := profile.ParseData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pprof data: %v", err)
	}

	samples := aggregateSamples(p)
	if samples.total == 0 {
		return nil, fmt.Errorf("profile %s holds no sample", id)
	}

	shares := make([]FunctionShare, 0, len(samples.appCum))
	for name, cum := range samples.appCum {
		shares = append(shares, FunctionShare{
			Name:        name,
			FlatPercent: float64(samples.appFlat[name]) * 100.0 / float64(samples.total),
			CumPercent:  float64(cum) * 100.0 / float64(samples.total),
		})
	}

	sort.Slice(shares, func(i, j int) bool {
		return shares[i].CumPercent > shares[j].CumPercent
	})

	return shares, nil
}

func analyzePprofData(pprofData []byte) (*pprofAnalysis, error) {
	p, err := profile.ParseData(pprofData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pprof data: %v", err)
	}

	analysis := &pprofAnalysis{
		ProfileInfo: pprofInfo{
			File:     "profile",
			Type:     p.SampleType[0].Type,
			Duration: fmt.Sprintf("%.2fs", float64(p.DurationNanos)/1e9),
			Samples:  fmt.Sprintf("%d samples", len(p.Sample)),
		},
		TopFuncs: make([]pprofFunc, 0),
		Summary: pprofSummary{
			MainCategories: make([]categorySummary, 0),
		},
	}

	if p.TimeNanos > 0 {
		analysis.ProfileInfo.Time = fmt.Sprintf("%d", p.TimeNanos/1e9)
	}

	samples := aggregateSamples(p)
	appFlatMap := samples.appFlat
	appCumMap := samples.appCum

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
		analysis.TopFuncs = append(analysis.TopFuncs, pprofFunc{
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

	categorizeApplicationPerformance(analysis, samples.cum)

	return analysis, nil
}

// samples holds the sample values a profile attributes to each function, flat and cumulative,
// and once more for application code alone.
type samples struct {
	cum     map[string]int64
	appFlat map[string]int64
	appCum  map[string]int64
	total   int64
}

func aggregateSamples(p *profile.Profile) samples {
	s := samples{
		cum:     make(map[string]int64),
		appFlat: make(map[string]int64),
		appCum:  make(map[string]int64),
	}

	for _, sample := range p.Sample {
		if len(sample.Value) == 0 {
			continue
		}
		value := sample.Value[0]
		s.total += value

		for i, loc := range sample.Location {
			if len(loc.Line) == 0 {
				continue
			}

			line := loc.Line[0]
			if line.Function == nil {
				continue
			}

			funcName := line.Function.Name

			if i == 0 && isApplicationCode(funcName) {
				s.appFlat[funcName] += value
			}
			s.cum[funcName] += value
			if isApplicationCode(funcName) {
				s.appCum[funcName] += value
			}
		}
	}

	return s
}

func isApplicationCode(funcName string) bool {
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

func categorizeApplicationPerformance(analysis *pprofAnalysis, allCumMap map[string]int64) {
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
			analysis.Summary.MainCategories = append(analysis.Summary.MainCategories, categorySummary{
				Category: category,
				Percent:  fmt.Sprintf("%.1f%%", percent),
			})
		}
	}
}
