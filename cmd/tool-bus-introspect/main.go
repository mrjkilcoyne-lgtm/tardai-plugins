// Plugin: tool-bus-introspect (Tier 0).
// Calls the Bus's own /api/tools/manifest and returns an enriched view.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

var selfBase = strings.TrimRight(envOr("SELF_BASE", "http://tool-tool-bus-introspect.tardai.svc.cluster.local:8000"), "/")

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "tool-bus-introspect",
		Title:       "Introspect the Tool Bus itself",
		Description: "Returns the Bus's manifest enriched with counts and a breakdown by blast_radius. Recursion is the feature: this tool is registered in the Bus and dispatched by the Bus, so sovereign can ask 'what can you do?' through her own dispatcher.",
		SchemaIn:    map[string]interface{}{},
		SchemaOut: map[string]interface{}{
			"tool_count":      "int",
			"tools":           "array",
			"by_blast_radius": "object",
			"disabled_count":  "int",
		},
		BlastRadius:     "read-only-cluster",
		DataSensitivity: "none",
		RateLimitPerMin: 120,
		EstimatedCost:   "zero",
		Endpoint:        selfBase + "/invoke",
		Deprecated:      false,
		Owner:           "claude-overnight-agent",
	}
	s.InvokeFn = func(ctx context.Context, body []byte) (interface{}, error) {
		url := s.BusBase + "/api/tools/manifest"
		hr, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		hr.Header.Set("Authorization", "Bearer "+s.Bearer)
		resp, err := s.HTTPClient().Do(hr)
		if err != nil {
			return nil, plugin.Errorf(502, "bus fetch: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return nil, plugin.Errorf(resp.StatusCode, "bus returned %d", resp.StatusCode)
		}
		var tools []map[string]interface{}
		if err := json.Unmarshal(raw, &tools); err != nil {
			return nil, plugin.Errorf(502, "bus returned non-array: %v", err)
		}
		byRadius := map[string]int{}
		disabled := 0
		summary := make([]map[string]interface{}, 0, len(tools))
		for _, t := range tools {
			r, _ := t["blast_radius"].(string)
			if r == "" {
				r = "unknown"
			}
			byRadius[r]++
			if d, _ := t["disabled"].(bool); d {
				disabled++
			}
			summary = append(summary, map[string]interface{}{
				"id":                      t["id"],
				"title":                   t["title"],
				"blast_radius":            t["blast_radius"],
				"data_sensitivity":        t["data_sensitivity"],
				"effective_confirm_tier":  t["effective_confirm_tier"],
				"disabled":                t["disabled"],
				"owner":                   t["owner"],
			})
		}
		return map[string]interface{}{
			"tool_count":      len(tools),
			"tools":           summary,
			"by_blast_radius": byRadius,
			"disabled_count":  disabled,
		}, nil
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}
