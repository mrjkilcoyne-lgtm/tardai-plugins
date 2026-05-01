// Plugin: http-egress (Tier 1 keystone).
// Sovereign-controlled outbound HTTP via allowlist ConfigMap.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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
	selfBase   = strings.TrimRight(envOr("SELF_BASE", "http://tool-http-egress.tardai.svc.cluster.local:8000"), "/")
	policyPath = "/etc/tardai-policy/egress-allowlist"
	defaultAllowlist = []string{
		"api.github.com", "api.stripe.com", "api.cal.com", "api.vercel.com",
		"api.cloudflare.com", "api.civo.com", "hooks.slack.com",
		"api.anthropic.com", "api.openai.com",
	}
)

func loadAllowlist() map[string]struct{} {
	out := map[string]struct{}{}
	if data, err := os.ReadFile(policyPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			out[line] = struct{}{}
		}
		if len(out) > 0 {
			return out
		}
	}
	for _, h := range defaultAllowlist {
		out[h] = struct{}{}
	}
	return out
}

func redactHeaders(in map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range in {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "cookie" || lk == "x-api-key" || lk == "api-key" {
			out[k] = "***REDACTED***"
		} else {
			out[k] = v
		}
	}
	return out
}

type parsed struct {
	Method    string
	URL       string
	Host      string
	Headers   map[string]interface{}
	Body      interface{}
	TimeoutMs int
}

func parseRequest(body []byte) (*parsed, error) {
	var req struct {
		Method    string                 `json:"method"`
		URL       string                 `json:"url"`
		Headers   map[string]interface{} `json:"headers"`
		Body      interface{}            `json:"body"`
		TimeoutMs interface{}            `json:"timeout_ms"`
	}
	if err := plugin.ParseJSON(body, &req); err != nil {
		return nil, err
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = "GET"
	}
	if req.URL == "" {
		return nil, plugin.Errorf(400, "url required")
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, plugin.Errorf(400, "scheme must be http(s)")
	}
	host := u.Hostname()
	allow := loadAllowlist()
	if _, ok := allow[host]; !ok {
		return nil, plugin.Errorf(403, "host %q not in allowlist", host)
	}
	timeout := 10000
	switch v := req.TimeoutMs.(type) {
	case float64:
		timeout = int(v)
	case int:
		timeout = v
	}
	if req.Headers == nil {
		req.Headers = map[string]interface{}{}
	}
	return &parsed{
		Method:    method,
		URL:       req.URL,
		Host:      host,
		Headers:   req.Headers,
		Body:      req.Body,
		TimeoutMs: timeout,
	}, nil
}

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "http-egress",
		Title:       "Outbound HTTP to sovereign-allowlisted hosts",
		Description: "Issues HTTP requests to hosts on the sovereign-controlled allowlist (ConfigMap tardai-tool-bus-policy). All non-allowlisted hosts return 403 with audit. Two-phase: /plan shows redacted request; /invoke runs.",
		SchemaIn: map[string]interface{}{
			"method":     "GET|POST|PUT|PATCH|DELETE|HEAD",
			"url":        "string (must hit allowlisted host)",
			"headers":    "object (optional)",
			"body":       "string|object (optional)",
			"timeout_ms": "int (optional, default 10000)",
		},
		SchemaOut: map[string]interface{}{
			"status":     "int",
			"headers":    "object",
			"body":       "string",
			"latency_ms": "int",
		},
		BlastRadius:     "write-external",
		DataSensitivity: "none",
		RateLimitPerMin: 60,
		EstimatedCost:   "low",
		Endpoint:        selfBase + "/invoke",
		PlanEndpoint:    selfBase + "/plan",
		Deprecated:      false,
		Owner:           "claude",
	}
	s.HealthExtra = func() map[string]interface{} {
		return map[string]interface{}{"allowlist_size": len(loadAllowlist())}
	}
	s.PlanFn = func(ctx context.Context, body []byte) (interface{}, error) {
		p, err := parseRequest(body)
		if err != nil {
			return nil, err
		}
		var preview interface{} = p.Body
		if str, ok := preview.(string); ok && len(str) > 500 {
			preview = str[:500] + "...[truncated]"
		}
		return map[string]interface{}{
			"would_do":       p.Method + " " + p.URL,
			"host":           p.Host,
			"headers":        redactHeaders(p.Headers),
			"body_preview":   preview,
			"timeout_ms":     p.TimeoutMs,
			"allowlist_size": len(loadAllowlist()),
		}, nil
	}
	s.InvokeFn = func(ctx context.Context, body []byte) (interface{}, error) {
		p, err := parseRequest(body)
		if err != nil {
			return nil, err
		}
		t0 := time.Now()
		var rdr io.Reader
		if p.Body != nil {
			switch b := p.Body.(type) {
			case string:
				rdr = strings.NewReader(b)
			default:
				j, _ := json.Marshal(b)
				rdr = bytes.NewReader(j)
				if _, ok := p.Headers["Content-Type"]; !ok {
					p.Headers["Content-Type"] = "application/json"
				}
			}
		}
		hr, err := http.NewRequestWithContext(ctx, p.Method, p.URL, rdr)
		if err != nil {
			return nil, plugin.Errorf(400, "build request: %v", err)
		}
		for k, v := range p.Headers {
			if vs, ok := v.(string); ok {
				hr.Header.Set(k, vs)
			}
		}
		client := &http.Client{
			Timeout: time.Duration(p.TimeoutMs) * time.Millisecond,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Do(hr)
		if err != nil {
			return nil, plugin.Errorf(502, "request failed: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		latency := int(time.Since(t0).Milliseconds())
		bodyStr := string(raw)
		if len(bodyStr) > 100000 {
			bodyStr = bodyStr[:100000] + "...[truncated]"
		}
		respHeaders := map[string]string{}
		for k, v := range resp.Header {
			respHeaders[k] = strings.Join(v, ", ")
		}
		return map[string]interface{}{
			"status":     resp.StatusCode,
			"headers":    respHeaders,
			"body":       bodyStr,
			"latency_ms": latency,
		}, nil
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}
