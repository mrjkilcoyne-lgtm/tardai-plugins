// Plugin: cost-sense (Tier 1).
// Aggregates spend honestly — unavailable when creds missing, never fabricated.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	plugin "github.com/mrjkilcoyne-lgtm/tardai-plugins/pkg/plugin"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	selfBase = strings.TrimRight(envOr("SELF_BASE", "http://tool-cost-sense.tardai.svc.cluster.local:8000"), "/")
)

func unavail(src, reason string) map[string]interface{} {
	return map[string]interface{}{"source": src, "unavailable": true, "reason": reason}
}

func anthropicUsage() map[string]interface{} {
	if os.Getenv("ANTHROPIC_ADMIN_TOKEN") == "" {
		return unavail("anthropic", "ANTHROPIC_ADMIN_TOKEN not set")
	}
	return unavail("anthropic", "Anthropic admin usage API integration not implemented; token present but no canonical endpoint wired")
}

func civoUsage(ctx context.Context) map[string]interface{} {
	key := os.Getenv("CIVO_API_KEY")
	if key == "" {
		return unavail("civo", "CIVO_API_KEY not set")
	}
	hr, _ := http.NewRequestWithContext(ctx, "GET", "https://api.civo.com/v2/billing", nil)
	hr.Header.Set("Authorization", "bearer "+key)
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(hr)
	if err != nil {
		return unavail("civo", "civo API error: "+err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return unavail("civo", fmt.Sprintf("civo API %d", resp.StatusCode))
	}
	raw, _ := io.ReadAll(resp.Body)
	var data interface{}
	_ = json.Unmarshal(raw, &data)
	return map[string]interface{}{"source": "civo", "unavailable": false, "data": data}
}

func vercelUsage(ctx context.Context) map[string]interface{} {
	tok := os.Getenv("VERCEL_TOKEN")
	if tok == "" {
		return unavail("vercel", "VERCEL_TOKEN not set")
	}
	hr, _ := http.NewRequestWithContext(ctx, "GET", "https://api.vercel.com/v1/usage", nil)
	hr.Header.Set("Authorization", "Bearer "+tok)
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(hr)
	if err != nil {
		return unavail("vercel", "vercel API error: "+err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return unavail("vercel", fmt.Sprintf("vercel API %d", resp.StatusCode))
	}
	raw, _ := io.ReadAll(resp.Body)
	var data interface{}
	_ = json.Unmarshal(raw, &data)
	return map[string]interface{}{"source": "vercel", "unavailable": false, "data": data}
}

func periodStart(period string) time.Time {
	now := time.Now().UTC()
	switch period {
	case "this-week":
		// ISO week: Monday start
		off := int(now.Weekday()) - 1
		if off < 0 {
			off = 6
		}
		d := now.AddDate(0, 0, -off)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	case "this-month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
}

func busInvocations(ctx context.Context, s *plugin.Server, period string) map[string]interface{} {
	start := periodStart(period)
	today := time.Now().UTC()
	byCaller := map[string]int{}
	byTool := map[string]int{}
	var filesRead, filesMissing []string
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		url := s.ArtefactBase + "/api/artefacts/_meta/audit/" + day + ".jsonl"
		hr, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		hr.Header.Set("Authorization", "Bearer "+s.Bearer)
		resp, err := s.HTTPClient().Do(hr)
		if err != nil {
			filesMissing = append(filesMissing, day)
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			filesMissing = append(filesMissing, day)
			continue
		}
		filesRead = append(filesRead, day)
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var entry map[string]interface{}
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				continue
			}
			cs, _ := entry["caller_session"].(string)
			if cs == "" {
				cs = "unknown"
			}
			byCaller[cs]++
			tid, _ := entry["tool_id"].(string)
			if tid == "" {
				tid = "unknown"
			}
			byTool[tid]++
		}
	}
	total := 0
	for _, v := range byCaller {
		total += v
	}
	return map[string]interface{}{
		"source":      "bus-audit",
		"unavailable": false,
		"data": map[string]interface{}{
			"by_caller_session": byCaller,
			"by_tool_id":        byTool,
			"files_read":        filesRead,
			"files_missing":     filesMissing,
			"total_invocations": total,
		},
	}
}

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "cost-sense",
		Title:       "Aggregate spend across Anthropic, Civo, Vercel, plus per-realiser invocation counts",
		Description: "Pulls usage from each available source (returns unavailable for any missing credentials — no fabrication). Also groups Bus audit log invocations by caller_session for per-realiser allocation.",
		SchemaIn: map[string]interface{}{
			"period": "today|this-week|this-month (optional, default today)",
			"scope":  "all|<service-name> (optional, default all)",
		},
		SchemaOut: map[string]interface{}{
			"period":  "string",
			"sources": "array",
			"summary": "object",
		},
		BlastRadius:     "read-only-external",
		DataSensitivity: "financial",
		RateLimitPerMin: 30,
		EstimatedCost:   "low",
		Endpoint:        selfBase + "/invoke",
		Deprecated:      false,
		Owner:           "claude",
	}
	s.HealthExtra = func() map[string]interface{} {
		return map[string]interface{}{
			"credentials_present": map[string]bool{
				"anthropic": os.Getenv("ANTHROPIC_ADMIN_TOKEN") != "",
				"civo":      os.Getenv("CIVO_API_KEY") != "",
				"vercel":    os.Getenv("VERCEL_TOKEN") != "",
			},
		}
	}
	s.InvokeFn = func(ctx context.Context, body []byte) (interface{}, error) {
		var req struct {
			Period string `json:"period"`
			Scope  string `json:"scope"`
		}
		_ = plugin.ParseJSON(body, &req)
		if req.Period == "" {
			req.Period = "today"
		}
		if req.Scope == "" {
			req.Scope = "all"
		}
		var sources []map[string]interface{}
		if req.Scope == "all" || req.Scope == "anthropic" {
			sources = append(sources, anthropicUsage())
		}
		if req.Scope == "all" || req.Scope == "civo" {
			sources = append(sources, civoUsage(ctx))
		}
		if req.Scope == "all" || req.Scope == "vercel" {
			sources = append(sources, vercelUsage(ctx))
		}
		if req.Scope == "all" || req.Scope == "bus-audit" || req.Scope == "realisers" {
			sources = append(sources, busInvocations(ctx, s, req.Period))
		}
		availCount, unavailCount := 0, 0
		var unavailReasons []map[string]interface{}
		for _, src := range sources {
			if u, _ := src["unavailable"].(bool); u {
				unavailCount++
				unavailReasons = append(unavailReasons, map[string]interface{}{
					"source": src["source"], "reason": src["reason"],
				})
			} else {
				availCount++
			}
		}
		return map[string]interface{}{
			"period":  req.Period,
			"scope":   req.Scope,
			"sources": sources,
			"summary": map[string]interface{}{
				"available_count":      availCount,
				"unavailable_count":    unavailCount,
				"unavailable_reasons":  unavailReasons,
			},
		}, nil
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}
