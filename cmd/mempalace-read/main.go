// Plugin: mempalace-read (Tier 1). Probes plausible MemPalace endpoints.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	selfBase     = strings.TrimRight(envOr("SELF_BASE", "http://tool-mempalace-read.tardai.svc.cluster.local:8000"), "/")
	mempalaceURL = strings.TrimRight(envOr("MEMPALACE_URL", "http://mempalace.substrates.svc.cluster.local:8095"), "/")
	readPaths    = []string{"/api/memories", "/api/memory", "/memories", "/palace/list", "/list", "/recall"}
)

func tryRead(ctx context.Context, client *http.Client, params url.Values) (string, []interface{}, bool) {
	for _, p := range readPaths {
		u := mempalaceURL + p
		if len(params) > 0 {
			u += "?" + params.Encode()
		}
		hr, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		resp, err := client.Do(hr)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var arr []interface{}
		if err := json.Unmarshal(raw, &arr); err == nil {
			return p, arr, true
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err == nil {
			for _, k := range []string{"memories", "results", "items", "entries"} {
				if v, ok := obj[k].([]interface{}); ok {
					return p, v, true
				}
			}
			return p, []interface{}{obj}, true
		}
	}
	return "", nil, false
}

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "mempalace-read",
		Title:       "Read sovereign's MemPalace memories",
		Description: "List/read memory entries from MemPalace. Optional filters: query (substring), category, key, limit. Read-only against sibling cluster service (mempalace.substrates:8095). API surface auto-probed; see STATUS.md for endpoint discovery state.",
		SchemaIn: map[string]interface{}{
			"query":    "string (optional, substring filter)",
			"category": "string (optional)",
			"key":      "string (optional, exact-match key)",
			"limit":    "int (optional, default 50)",
		},
		SchemaOut: map[string]interface{}{
			"memories":      "array",
			"count":         "int",
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
			Query    string `json:"query"`
			Category string `json:"category"`
			Key      string `json:"key"`
			Limit    int    `json:"limit"`
		}
		if err := plugin.ParseJSON(body, &req); err != nil {
			return nil, err
		}
		if req.Limit == 0 {
			req.Limit = 50
		}
		params := url.Values{}
		if req.Query != "" {
			params.Set("q", req.Query)
		}
		if req.Category != "" {
			params.Set("category", req.Category)
		}
		if req.Key != "" {
			params.Set("key", req.Key)
		}
		params.Set("limit", fmt.Sprintf("%d", req.Limit))

		path, mems, ok := tryRead(ctx, s.HTTPClient(), params)
		if !ok {
			// honest 502 — match Python behaviour
			return nil, plugin.Errorf(502, "MemPalace API surface not discovered; see STATUS.md")
		}
		// client-side filtering fallback
		filt := mems
		if req.Query != "" {
			q := strings.ToLower(req.Query)
			tmp := filt[:0]
			for _, m := range filt {
				if mm, ok := m.(map[string]interface{}); ok {
					match := false
					for _, v := range mm {
						if strings.Contains(strings.ToLower(fmt.Sprintf("%v", v)), q) {
							match = true
							break
						}
					}
					if match {
						tmp = append(tmp, m)
					}
				}
			}
			filt = tmp
		}
		if req.Category != "" {
			tmp := filt[:0]
			for _, m := range filt {
				if mm, ok := m.(map[string]interface{}); ok {
					if c, _ := mm["category"].(string); c == req.Category {
						tmp = append(tmp, m)
					}
				}
			}
			filt = tmp
		}
		if req.Key != "" {
			tmp := filt[:0]
			for _, m := range filt {
				if mm, ok := m.(map[string]interface{}); ok {
					if k, _ := mm["key"].(string); k == req.Key {
						tmp = append(tmp, m)
					}
				}
			}
			filt = tmp
		}
		if len(filt) > req.Limit {
			filt = filt[:req.Limit]
		}
		return map[string]interface{}{
			"memories":      filt,
			"count":         len(filt),
			"endpoint_used": path,
		}, nil
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}
