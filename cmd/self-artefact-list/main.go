// Plugin: self-artefact-list (Tier 0).
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	plugin "github.com/mrjkilcoyne-lgtm/tardai-plugins/pkg/plugin"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var selfBase = strings.TrimRight(envOr("SELF_BASE", "http://tool-self-artefact-list.tardai.svc.cluster.local:8000"), "/")

func safePrefix(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return "", plugin.Errorf(400, "prefix must be relative")
	}
	clean := path.Clean(strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/"))
	if clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || clean == ".." {
		return "", plugin.Errorf(400, "illegal prefix component")
	}
	return clean, nil
}

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "self-artefact-list",
		Title:       "List TARDAI's own artefact surface",
		Description: "LIST files under a prefix on the artefact surface. Returns array of paths.",
		SchemaIn: map[string]interface{}{
			"prefix": "string (optional, default empty = root)",
		},
		SchemaOut: map[string]interface{}{
			"paths": "string[]",
			"count": "int",
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
		var req struct {
			Prefix string `json:"prefix"`
		}
		if err := plugin.ParseJSON(body, &req); err != nil {
			return nil, err
		}
		prefix, err := safePrefix(req.Prefix)
		if err != nil {
			return nil, err
		}
		u := s.ArtefactBase + "/api/artefacts"
		if prefix != "" {
			u += "?prefix=" + url.QueryEscape(prefix)
		}
		hr, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		hr.Header.Set("Authorization", "Bearer "+s.Bearer)
		resp, err := s.HTTPClient().Do(hr)
		if err != nil {
			return nil, plugin.Errorf(502, "surface fetch: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return nil, plugin.Errorf(resp.StatusCode, "surface returned %d", resp.StatusCode)
		}
		var paths []string
		if err := json.Unmarshal(raw, &paths); err != nil {
			return nil, plugin.Errorf(502, "surface returned non-array: %v", err)
		}
		return map[string]interface{}{
			"paths":  paths,
			"count":  len(paths),
			"prefix": prefix,
		}, nil
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}
