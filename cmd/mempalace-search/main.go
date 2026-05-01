// Plugin: mempalace-search (Tier 1). Native semantic if exposed; substring fallback otherwise.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	plugin "github.com/mrjkilcoyne-lgtm/tardai-plugins/pkg/plugin"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	selfBase     = strings.TrimRight(envOr("SELF_BASE", "http://tool-mempalace-search.tardai.svc.cluster.local:8000"), "/")
	mempalaceURL = strings.TrimRight(envOr("MEMPALACE_URL", "http://mempalace.substrates.svc.cluster.local:8095"), "/")
	searchPaths  = []string{"/api/search", "/search", "/palace/search", "/api/semantic"}
	listPaths    = []string{"/api/memories", "/memories", "/palace/list"}
)

func score(m map[string]interface{}, q string, tags []string) float64 {
	ql := strings.ToLower(q)
	s := 0.0
	for _, v := range m {
		sv := strings.ToLower(fmt.Sprintf("%v", v))
		if strings.Contains(sv, ql) {
			s += 1.0
			s += float64(strings.Count(sv, ql)) * 0.1
		}
	}
	if len(tags) > 0 {
		var memTags []string
		switch t := m["tags"].(type) {
		case []interface{}:
			for _, x := range t {
				if str, ok := x.(string); ok {
					memTags = append(memTags, str)
				}
			}
		case string:
			memTags = []string{t}
		}
		if cat, _ := m["category"].(string); cat != "" {
			memTags = append(memTags, cat)
		}
		for _, want := range tags {
			for _, have := range memTags {
				if want == have {
					s += 0.5
				}
			}
		}
	}
	return s
}

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "mempalace-search",
		Title:       "Semantic search over MemPalace",
		Description: "Ranked search across sovereign's memories. Tries native semantic search endpoints; falls back to substring + tag matching with score estimation. See STATUS.md for the current discovery state.",
		SchemaIn: map[string]interface{}{
			"query": "string (required)",
			"limit": "int (optional, default 10)",
			"tags":  "array (optional)",
		},
		SchemaOut: map[string]interface{}{
			"results":       "array",
			"count":         "int",
			"method":        "semantic|substring-fallback",
			"endpoint_used": "string",
		},
		BlastRadius:     "read-only-external",
		DataSensitivity: "none",
		RateLimitPerMin: 60,
		EstimatedCost:   "zero",
		Endpoint:        selfBase + "/invoke",
		Deprecated:      false,
		Owner:           "claude",
	}
	s.InvokeFn = func(ctx context.Context, body []byte) (interface{}, error) {
		var req struct {
			Query string   `json:"query"`
			Limit int      `json:"limit"`
			Tags  []string `json:"tags"`
		}
		if err := plugin.ParseJSON(body, &req); err != nil {
			return nil, err
		}
		if req.Query == "" {
			return nil, plugin.Errorf(400, "query required")
		}
		if req.Limit == 0 {
			req.Limit = 10
		}
		client := s.HTTPClient()

		// Try native POST semantic
		for _, p := range searchPaths {
			payload, _ := json.Marshal(map[string]interface{}{
				"query": req.Query, "limit": req.Limit, "tags": req.Tags,
			})
			hr, _ := http.NewRequestWithContext(ctx, "POST", mempalaceURL+p, bytes.NewReader(payload))
			hr.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(hr)
			if err != nil {
				continue
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				results := unwrap(raw)
				if len(results) > req.Limit {
					results = results[:req.Limit]
				}
				return map[string]interface{}{
					"results": results, "count": len(results),
					"method": "semantic", "endpoint_used": p,
				}, nil
			}
		}
		// Try GET
		for _, p := range searchPaths {
			params := url.Values{}
			params.Set("q", req.Query)
			params.Set("limit", fmt.Sprintf("%d", req.Limit))
			hr, _ := http.NewRequestWithContext(ctx, "GET", mempalaceURL+p+"?"+params.Encode(), nil)
			resp, err := client.Do(hr)
			if err != nil {
				continue
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				results := unwrap(raw)
				if len(results) > req.Limit {
					results = results[:req.Limit]
				}
				return map[string]interface{}{
					"results": results, "count": len(results),
					"method": "semantic", "endpoint_used": p,
				}, nil
			}
		}
		// Fallback: list + score
		for _, p := range listPaths {
			hr, _ := http.NewRequestWithContext(ctx, "GET", mempalaceURL+p, nil)
			resp, err := client.Do(hr)
			if err != nil {
				continue
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				continue
			}
			memories := unwrap(raw)
			type scored struct {
				Mem   interface{}
				Score float64
			}
			var sc []scored
			for _, m := range memories {
				if mm, ok := m.(map[string]interface{}); ok {
					if v := score(mm, req.Query, req.Tags); v > 0 {
						sc = append(sc, scored{m, v})
					}
				}
			}
			sort.Slice(sc, func(i, j int) bool { return sc[i].Score > sc[j].Score })
			if len(sc) > req.Limit {
				sc = sc[:req.Limit]
			}
			results := make([]map[string]interface{}, 0, len(sc))
			for _, x := range sc {
				results = append(results, map[string]interface{}{"memory": x.Mem, "score": x.Score})
			}
			return map[string]interface{}{
				"results": results, "count": len(results),
				"method": "substring-fallback", "endpoint_used": p,
			}, nil
		}
		return nil, plugin.Errorf(502, "MemPalace API surface not discovered; see STATUS.md")
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}

func unwrap(raw []byte) []interface{} {
	var arr []interface{}
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, k := range []string{"results", "memories", "items"} {
			if v, ok := obj[k].([]interface{}); ok {
				return v
			}
		}
	}
	return nil
}
