// Plugin: self-artefact-read (Tier 0).
// Proxies GET to the artefact surface so sovereign can read her own writes.
package main

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
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

var selfBase = strings.TrimRight(envOr("SELF_BASE", "http://tool-self-artefact-read.tardai.svc.cluster.local:8000"), "/")

func safePath(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return "", plugin.Errorf(400, "path must be relative")
	}
	clean := path.Clean(strings.ReplaceAll(p, "\\", "/"))
	if clean == "." || clean == "/" || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || clean == ".." {
		return "", plugin.Errorf(400, "illegal path component")
	}
	return clean, nil
}

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "self-artefact-read",
		Title:       "Read TARDAI's own artefact surface",
		Description: "GET a file from the artefact write surface. Returns body as utf-8 text (or base64 if encoding=base64). Sovereign's first eye on her own writing.",
		SchemaIn: map[string]interface{}{
			"path":     "string",
			"encoding": "utf8|base64 (optional, default utf8)",
		},
		SchemaOut: map[string]interface{}{
			"body":         "string",
			"bytes":        "int",
			"content_type": "string",
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
			Path     string `json:"path"`
			Encoding string `json:"encoding"`
		}
		if err := plugin.ParseJSON(body, &req); err != nil {
			return nil, err
		}
		p, err := safePath(req.Path)
		if err != nil {
			return nil, err
		}
		encoding := req.Encoding
		if encoding == "" {
			encoding = "utf8"
		}
		url := s.ArtefactBase + "/api/artefacts/" + p
		hr, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
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
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/octet-stream"
		}
		var bodyStr string
		if encoding == "base64" {
			bodyStr = base64.StdEncoding.EncodeToString(raw)
		} else {
			// best-effort utf8 decode (Go strings are bytes, valid utf8 passes through)
			bodyStr = string(raw)
		}
		return map[string]interface{}{
			"body":         bodyStr,
			"bytes":        len(raw),
			"content_type": ct,
			"encoding":     encoding,
		}, nil
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}
