// Plugin: time-sense (Tier 1).
package main

import (
	"context"
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
	selfBase     = strings.TrimRight(envOr("SELF_BASE", "http://tool-time-sense.tardai.svc.cluster.local:8000"), "/")
	bootMono     = time.Now()
	bootWallStr  = time.Now().UTC().Format(time.RFC3339Nano)
)

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "time-sense",
		Title:       "Wall-clock and monotonic time, with optional delta",
		Description: "Returns current UTC wall-clock and a monotonic counter. If relative_to (ISO timestamp) is supplied, also returns delta in seconds. Cheap; intended for sovereign to ground herself in time.",
		SchemaIn: map[string]interface{}{
			"format":      "iso|epoch|human (optional, default iso)",
			"relative_to": "ISO timestamp (optional)",
		},
		SchemaOut: map[string]interface{}{
			"now_iso":   "string",
			"now_epoch": "float",
			"now_human": "string",
			"monotonic": "float",
			"uptime_s":  "float",
			"boot_wall": "string",
			"delta_s":   "float|null",
		},
		BlastRadius:     "read-only-cluster",
		DataSensitivity: "none",
		RateLimitPerMin: 600,
		EstimatedCost:   "zero",
		Endpoint:        selfBase + "/invoke",
		Deprecated:      false,
		Owner:           "claude",
	}
	s.InvokeFn = func(ctx context.Context, body []byte) (interface{}, error) {
		var req struct {
			Format     string `json:"format"`
			RelativeTo string `json:"relative_to"`
		}
		if err := plugin.ParseJSON(body, &req); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		var deltaS interface{} = nil
		if req.RelativeTo != "" {
			r := strings.Replace(req.RelativeTo, "Z", "+00:00", 1)
			ref, err := time.Parse(time.RFC3339Nano, r)
			if err != nil {
				ref, err = time.Parse(time.RFC3339, r)
			}
			if err != nil {
				return nil, plugin.Errorf(400, "relative_to not ISO: %q", req.RelativeTo)
			}
			deltaS = now.Sub(ref).Seconds()
		}
		return map[string]interface{}{
			"now_iso":   now.Format(time.RFC3339Nano),
			"now_epoch": float64(now.UnixNano()) / 1e9,
			"now_human": now.Format("2006-01-02 15:04:05") + " UTC",
			"monotonic": time.Since(bootMono).Seconds() + 1, // arbitrary baseline; matches uptime+1 to avoid 0
			"uptime_s":  time.Since(bootMono).Seconds(),
			"boot_wall": bootWallStr,
			"delta_s":   deltaS,
		}, nil
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}
