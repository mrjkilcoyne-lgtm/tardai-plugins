// Plugin: mandate-tracking (Tier 1).
// Reads _meta/mandates/<id>.yaml from artefact surface and filters by status.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	plugin "github.com/mrjkilcoyne-lgtm/tardai-plugins/pkg/plugin"
	"gopkg.in/yaml.v3"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var selfBase = strings.TrimRight(envOr("SELF_BASE", "http://tool-mandate-tracking.tardai.svc.cluster.local:8000"), "/")

func listMandateFiles(ctx context.Context, s *plugin.Server) []string {
	base := s.ArtefactBase + "/api/artefacts"
	for _, params := range []url.Values{
		{"prefix": []string{"_meta/mandates/"}},
		{"path": []string{"_meta/mandates"}},
	} {
		u := base + "?" + params.Encode()
		hr, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		hr.Header.Set("Authorization", "Bearer "+s.Bearer)
		resp, err := s.HTTPClient().Do(hr)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var arr []string
		if err := json.Unmarshal(raw, &arr); err == nil {
			out := []string{}
			for _, p := range arr {
				if strings.HasSuffix(p, ".yaml") && strings.Contains(p, "_meta/mandates") {
					out = append(out, p)
				}
			}
			return out
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err == nil {
			for _, key := range []string{"files", "paths", "items"} {
				if items, ok := obj[key].([]interface{}); ok {
					out := []string{}
					for _, it := range items {
						var p string
						switch x := it.(type) {
						case string:
							p = x
						case map[string]interface{}:
							p, _ = x["path"].(string)
						}
						if strings.HasSuffix(p, ".yaml") && strings.Contains(p, "_meta/mandates") {
							out = append(out, p)
						}
					}
					return out
				}
			}
		}
	}
	return nil
}

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "mandate-tracking",
		Title:       "List sovereign's open / completed / blocked mandates",
		Description: "Reads _meta/mandates/<id>.yaml from the artefact surface. Filters by status. Returns structured list. Read-only — mandates are written via the artefact surface POST by sovereign or claude.",
		SchemaIn: map[string]interface{}{
			"status": "open|completed|blocked|all (optional, default all)",
		},
		SchemaOut: map[string]interface{}{
			"mandates":  "array",
			"count":     "int",
			"by_status": "object",
		},
		BlastRadius:     "read-only-cluster",
		DataSensitivity: "none",
		RateLimitPerMin: 60,
		EstimatedCost:   "zero",
		Endpoint:        selfBase + "/invoke",
		Deprecated:      false,
		Owner:           "claude",
	}
	s.InvokeFn = func(ctx context.Context, body []byte) (interface{}, error) {
		var req struct {
			Status string `json:"status"`
		}
		_ = plugin.ParseJSON(body, &req)
		if req.Status == "" {
			req.Status = "all"
		}
		paths := listMandateFiles(ctx, s)
		mandates := []map[string]interface{}{}
		for _, p := range paths {
			rel := strings.TrimLeft(p, "/")
			fetchPath := rel
			if !strings.HasPrefix(rel, "_meta/") {
				parts := strings.Split(rel, "/")
				fetchPath = "_meta/mandates/" + parts[len(parts)-1]
			}
			u := s.ArtefactBase + "/api/artefacts/" + fetchPath
			hr, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
			hr.Header.Set("Authorization", "Bearer "+s.Bearer)
			resp, err := s.HTTPClient().Do(hr)
			if err != nil {
				continue
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				continue
			}
			var doc map[string]interface{}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				continue
			}
			if doc != nil {
				mandates = append(mandates, doc)
			}
		}
		filtered := mandates
		if req.Status != "all" {
			tmp := []map[string]interface{}{}
			for _, m := range mandates {
				if s, _ := m["status"].(string); s == req.Status {
					tmp = append(tmp, m)
				}
			}
			filtered = tmp
		}
		byStatus := map[string]int{}
		for _, m := range filtered {
			st, _ := m["status"].(string)
			if st == "" {
				st = "unknown"
			}
			byStatus[st]++
		}
		return map[string]interface{}{
			"mandates":  filtered,
			"count":     len(filtered),
			"by_status": byStatus,
		}, nil
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}
