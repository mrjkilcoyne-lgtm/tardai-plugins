// Plugin: pod-introspect (Tier 1).
// Read-only pod/log/event/metrics view via in-pod ServiceAccount.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
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
	selfBase  = strings.TrimRight(envOr("SELF_BASE", "http://tool-pod-introspect.tardai.svc.cluster.local:8000"), "/")
	namespace = envOr("TARGET_NAMESPACE", "tardai")
	k8sAPI    = "https://kubernetes.default.svc"
	tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

func saToken() (string, error) {
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return "", plugin.Errorf(500, "service account token not mounted")
	}
	return strings.TrimSpace(string(data)), nil
}

func k8sClient() *http.Client {
	pool := x509.NewCertPool()
	if data, err := os.ReadFile(caPath); err == nil {
		pool.AppendCertsFromPEM(data)
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
}

func k8sGet(ctx context.Context, path string, params url.Values) (int, []byte, error) {
	tok, err := saToken()
	if err != nil {
		return 0, nil, err
	}
	u := k8sAPI + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	hr, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	hr.Header.Set("Authorization", "Bearer "+tok)
	resp, err := k8sClient().Do(hr)
	if err != nil {
		return 0, nil, plugin.Errorf(502, "k8s api: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body, nil
}

func main() {
	s := plugin.New()
	s.Manifest = plugin.Manifest{
		ID:          "pod-introspect",
		Title:       "Read pod state, logs, events in tardai namespace",
		Description: "Returns describe/logs/events/top for pods in the tardai namespace via the k8s API. Service account scoped to tardai-only RBAC. Actions: describe, logs, events, top.",
		SchemaIn: map[string]interface{}{
			"action":    "describe|logs|events|top",
			"pod":       "string (required for describe/logs)",
			"container": "string (optional for logs)",
			"lines":     "int (optional, default 100)",
		},
		SchemaOut: map[string]interface{}{
			"action": "string",
			"data":   "object|array|string",
		},
		BlastRadius:     "read-only-cluster",
		DataSensitivity: "none",
		RateLimitPerMin: 30,
		EstimatedCost:   "zero",
		Endpoint:        selfBase + "/invoke",
		Deprecated:      false,
		Owner:           "claude",
	}
	s.HealthExtra = func() map[string]interface{} { return map[string]interface{}{"namespace": namespace} }
	s.InvokeFn = func(ctx context.Context, body []byte) (interface{}, error) {
		var req struct {
			Action    string `json:"action"`
			Pod       string `json:"pod"`
			Container string `json:"container"`
			Lines     int    `json:"lines"`
		}
		if err := plugin.ParseJSON(body, &req); err != nil {
			return nil, err
		}
		if req.Lines == 0 {
			req.Lines = 100
		}
		switch req.Action {
		case "describe":
			if req.Pod == "" {
				return nil, plugin.Errorf(400, "pod required for describe")
			}
			st, raw, err := k8sGet(ctx, "/api/v1/namespaces/"+namespace+"/pods/"+req.Pod, nil)
			if err != nil {
				return nil, err
			}
			if st != 200 {
				return nil, plugin.Errorf(st, truncate(string(raw), 300))
			}
			var p map[string]interface{}
			_ = json.Unmarshal(raw, &p)
			meta, _ := p["metadata"].(map[string]interface{})
			spec, _ := p["spec"].(map[string]interface{})
			status, _ := p["status"].(map[string]interface{})
			var conts []map[string]interface{}
			if cs, ok := status["containerStatuses"].([]interface{}); ok {
				for _, c := range cs {
					if cm, ok := c.(map[string]interface{}); ok {
						stateKeys := []string{}
						if st, ok := cm["state"].(map[string]interface{}); ok {
							for k := range st {
								stateKeys = append(stateKeys, k)
							}
						}
						conts = append(conts, map[string]interface{}{
							"name":         cm["name"],
							"ready":        cm["ready"],
							"restartCount": cm["restartCount"],
							"image":        cm["image"],
							"state":        stateKeys,
						})
					}
				}
			}
			return map[string]interface{}{
				"action": "describe",
				"data": map[string]interface{}{
					"name":       safe(meta, "name"),
					"phase":      safe(status, "phase"),
					"node":       safe(spec, "nodeName"),
					"containers": conts,
					"conditions": status["conditions"],
					"startTime":  safe(status, "startTime"),
				},
			}, nil
		case "logs":
			if req.Pod == "" {
				return nil, plugin.Errorf(400, "pod required for logs")
			}
			params := url.Values{}
			params.Set("tailLines", fmt.Sprintf("%d", req.Lines))
			if req.Container != "" {
				params.Set("container", req.Container)
			}
			st, raw, err := k8sGet(ctx, "/api/v1/namespaces/"+namespace+"/pods/"+req.Pod+"/log", params)
			if err != nil {
				return nil, err
			}
			if st != 200 {
				return nil, plugin.Errorf(st, truncate(string(raw), 300))
			}
			return map[string]interface{}{"action": "logs", "data": string(raw)}, nil
		case "events":
			params := url.Values{}
			params.Set("limit", fmt.Sprintf("%d", req.Lines))
			if req.Pod != "" {
				params.Set("fieldSelector", "involvedObject.name="+req.Pod)
			}
			st, raw, err := k8sGet(ctx, "/api/v1/namespaces/"+namespace+"/events", params)
			if err != nil {
				return nil, err
			}
			if st != 200 {
				return nil, plugin.Errorf(st, truncate(string(raw), 300))
			}
			var data map[string]interface{}
			_ = json.Unmarshal(raw, &data)
			items, _ := data["items"].([]interface{})
			out := make([]map[string]interface{}, 0, len(items))
			for _, it := range items {
				e, _ := it.(map[string]interface{})
				inv, _ := e["involvedObject"].(map[string]interface{})
				ts := e["lastTimestamp"]
				if ts == nil {
					ts = e["eventTime"]
				}
				out = append(out, map[string]interface{}{
					"time":    ts,
					"type":    e["type"],
					"reason":  e["reason"],
					"object":  fmt.Sprintf("%v/%v", inv["kind"], inv["name"]),
					"message": e["message"],
				})
			}
			return map[string]interface{}{"action": "events", "data": out}, nil
		case "top":
			st, raw, err := k8sGet(ctx, "/apis/metrics.k8s.io/v1beta1/namespaces/"+namespace+"/pods", nil)
			if err != nil {
				return nil, err
			}
			if st != 200 {
				return map[string]interface{}{
					"action": "top", "data": []interface{}{},
					"error": fmt.Sprintf("metrics-server returned %d", st),
				}, nil
			}
			var data map[string]interface{}
			_ = json.Unmarshal(raw, &data)
			items, _ := data["items"].([]interface{})
			out := make([]map[string]interface{}, 0, len(items))
			for _, it := range items {
				m, _ := it.(map[string]interface{})
				meta, _ := m["metadata"].(map[string]interface{})
				cs, _ := m["containers"].([]interface{})
				cl := make([]map[string]interface{}, 0, len(cs))
				for _, c := range cs {
					cm, _ := c.(map[string]interface{})
					usage, _ := cm["usage"].(map[string]interface{})
					cl = append(cl, map[string]interface{}{
						"name":   cm["name"],
						"cpu":    usage["cpu"],
						"memory": usage["memory"],
					})
				}
				out = append(out, map[string]interface{}{"name": meta["name"], "containers": cl})
			}
			return map[string]interface{}{"action": "top", "data": out}, nil
		default:
			return nil, plugin.Errorf(400, "action must be one of: describe, logs, events, top")
		}
	}
	if err := s.Run(":8000"); err != nil {
		os.Exit(1)
	}
}

func safe(m map[string]interface{}, k string) interface{} {
	if m == nil {
		return nil
	}
	return m[k]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
