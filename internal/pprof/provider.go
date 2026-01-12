package pprof

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/pprof/profile"
	"github.com/okashoi/pprotein-mcp-server/internal/mcp"
)

type PprofIDEntry struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

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

func ListPprofIDs(pproteinURL string) ([]PprofIDEntry, error) {
	url := fmt.Sprintf("%s/api/pprof/list", pproteinURL)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to call pprof list API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pprof list API returned status %d", resp.StatusCode)
	}

	var entries []PprofIDEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to decode pprof list: %v", err)
	}

	return entries, nil
}

func ProvidePprofProfile(pproteinURL string, id string) (mcp.ResourceContent, error) {
	pprofData, err := fetchPprofProfileFromAPI(pproteinURL, id)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to fetch pprof profile: %v", err)
	}

	analysis, err := analyzePprofData(pprofData)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to analyze pprof data: %v", err)
	}

	jsonData, err := json.Marshal(analysis)
	if err != nil {
		return mcp.ResourceContent{}, fmt.Errorf("failed to marshal analysis result: %v", err)
	}

	uri := "pprotein://pprof/latest"
	if id != "" {
		uri = fmt.Sprintf("pprotein://pprof/%s", id)
	}

	return mcp.ResourceContent{
		URI:      uri,
		MimeType: "application/json",
		Text:     string(jsonData),
	}, nil
}

func ProvideLatestPprofProfile(pproteinURL string) (mcp.ResourceContent, error) {
	return ProvidePprofProfile(pproteinURL, "")
}

func fetchPprofProfileFromAPI(pproteinURL string, id string) ([]byte, error) {
	var url string
	if id == "" {
		url = fmt.Sprintf("%s/api/pprof/data/latest", pproteinURL)
	} else {
		url = fmt.Sprintf("%s/api/pprof/data/%s", pproteinURL, id)
	}

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to call pprof API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pprof API returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading pprof data: %v", err)
	}

	return data, nil
}

func fetchLatestPprofProfileFromAPI(pproteinURL string) ([]byte, error) {
	return fetchPprofProfileFromAPI(pproteinURL, "")
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
				if isApplicationCode(funcName) {
					appFlatMap[funcName] += value
				}
			}
			cumMap[funcName] += value
			if isApplicationCode(funcName) {
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

	categorizeApplicationPerformance(analysis, cumMap)

	return analysis, nil
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
