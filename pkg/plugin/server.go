// Package plugin is the shared scaffold for TARDAI Tool Bus plugins.
//
// Each plugin in cmd/<id>/main.go constructs a Server, sets its Manifest,
// PlanFn (optional), InvokeFn, and calls Run(":8000"). The library handles
// bearer auth, /healthz, /plan, /invoke, manifest registration with the Bus
// (with backoff), and audit-log push to the artefact surface.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Manifest mirrors the Python tool manifest sent to /api/tools/register.
// JSON shape must match the Bus's expectation: {"manifest": {...}}.
type Manifest struct {
	ID                  string                 `json:"id"`
	Title               string                 `json:"title"`
	Description         string                 `json:"description"`
	SchemaIn            map[string]interface{} `json:"schema_in"`
	SchemaOut           map[string]interface{} `json:"schema_out"`
	BlastRadius         string                 `json:"blast_radius"`
	DataSensitivity     string                 `json:"data_sensitivity"`
	RateLimitPerMin     int                    `json:"rate_limit_per_min"`
	EstimatedCost       string                 `json:"estimated_cost"`
	Endpoint            string                 `json:"endpoint"`
	PlanEndpoint        string                 `json:"plan_endpoint,omitempty"`
	Deprecated          bool                   `json:"deprecated"`
	Owner               string                 `json:"owner"`
	RequiredConfirmTier string                 `json:"required_confirm_tier,omitempty"`
}

// HandlerFn is the signature for /plan and /invoke handlers. Body is the
// raw JSON request body. Return any value JSON-marshalable, or an error.
// Errors of type *HTTPError control the response status; other errors
// become 500.
type HandlerFn func(ctx context.Context, body []byte) (interface{}, error)

// HTTPError carries an explicit status code.
type HTTPError struct {
	Status int
	Msg    string
}

func (e *HTTPError) Error() string { return e.Msg }

// Errorf creates a status-coded error.
func Errorf(status int, format string, a ...interface{}) error {
	return &HTTPError{Status: status, Msg: fmt.Sprintf(format, a...)}
}

// Server is one plugin's HTTP service.
type Server struct {
	Manifest     Manifest
	BusBase      string // env BUS_BASE
	ArtefactBase string // env ARTEFACT_BASE
	Bearer       string // env BEARER
	PlanFn       HandlerFn
	InvokeFn     HandlerFn

	// HealthExtra is merged into /healthz response if non-nil.
	HealthExtra func() map[string]interface{}

	httpClient *http.Client
}

// New constructs a Server populating BusBase, ArtefactBase, and Bearer
// from environment variables. The Manifest must be filled in by the
// caller before Run.
func New() *Server {
	bearer := os.Getenv("BEARER")
	if bearer == "" {
		log.Fatalf("BEARER required")
	}
	return &Server{
		BusBase:      strings.TrimRight(envOr("BUS_BASE", "http://tardai-tool-bus.tardai.svc.cluster.local:8000"), "/"),
		ArtefactBase: strings.TrimRight(envOr("ARTEFACT_BASE", "http://tardai-artefacts.tardai.svc.cluster.local:8000"), "/"),
		Bearer:       bearer,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// HTTPClient returns a shared *http.Client. Callers may override the
// timeout with their own client when needed.
func (s *Server) HTTPClient() *http.Client { return s.httpClient }

// Register pushes the manifest to the Bus with retry/backoff. Runs in a
// goroutine from Run; errors are logged but do not block startup.
func (s *Server) Register() error {
	url := s.BusBase + "/api/tools/register"
	payload := map[string]interface{}{"manifest": s.Manifest}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 30; attempt++ {
		req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+s.Bearer)
		req.Header.Set("Content-Type", "application/json")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req = req.WithContext(ctx)
		resp, err := s.httpClient.Do(req)
		cancel()
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 201 {
				log.Printf("[%s] registered with bus: %d", s.Manifest.ID, resp.StatusCode)
				return nil
			}
			b, _ := io.ReadAll(resp.Body)
			log.Printf("[%s] register attempt %d: %d %s", s.Manifest.ID, attempt, resp.StatusCode, truncate(string(b), 200))
		} else {
			log.Printf("[%s] register attempt %d: %v", s.Manifest.ID, attempt, err)
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("registration gave up after 30 attempts")
}

// Audit posts a single NDJSON entry to the artefact surface for today.
func (s *Server) Audit(toolID, scope, phase, status string, latencyMs int) {
	day := time.Now().UTC().Format("2006-01-02")
	url := fmt.Sprintf("%s/api/artefacts/_meta/audit/%s.jsonl", s.ArtefactBase, day)
	entry := map[string]interface{}{
		"ts":         time.Now().UTC().Format(time.RFC3339Nano),
		"tool_id":    toolID,
		"scope":      scope,
		"phase":      phase,
		"status":     status,
		"latency_ms": latencyMs,
	}
	line, _ := json.Marshal(entry)
	body := append(line, '\n')
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.Bearer)
	req.Header.Set("Content-Type", "application/x-ndjson")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := s.httpClient.Do(req.WithContext(ctx))
	if err != nil {
		log.Printf("[%s] audit push failed: %v", s.Manifest.ID, err)
		return
	}
	resp.Body.Close()
}

func (s *Server) checkAuth(r *http.Request) error {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || h[7:] != s.Bearer {
		return &HTTPError{Status: 401, Msg: "bad bearer"}
	}
	return nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request, fn HandlerFn, phase string) {
	t0 := time.Now()
	if err := s.checkAuth(r); err != nil {
		writeErr(w, err)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, &HTTPError{Status: 400, Msg: "read body: " + err.Error()})
		return
	}
	if fn == nil {
		writeErr(w, &HTTPError{Status: 404, Msg: "phase not implemented"})
		return
	}
	out, err := fn(r.Context(), body)
	latency := int(time.Since(t0).Milliseconds())
	status := "ok"
	if err != nil {
		status = "error"
		go s.Audit(s.Manifest.ID, s.Manifest.BlastRadius, phase, status, latency)
		writeErr(w, err)
		return
	}
	go s.Audit(s.Manifest.ID, s.Manifest.BlastRadius, phase, status, latency)
	writeJSON(w, 200, out)
}

func writeErr(w http.ResponseWriter, err error) {
	if he, ok := err.(*HTTPError); ok {
		writeJSON(w, he.Status, map[string]string{"detail": he.Msg})
		return
	}
	writeJSON(w, 500, map[string]string{"detail": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Run starts the HTTP server on addr (e.g. ":8000") and blocks.
// Registration with the Bus happens in a background goroutine.
func (s *Server) Run(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]interface{}{"ok": true}
		if s.HealthExtra != nil {
			for k, v := range s.HealthExtra() {
				out[k] = v
			}
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("/invoke", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			writeErr(w, &HTTPError{Status: 405, Msg: "POST required"})
			return
		}
		s.handle(w, r, s.InvokeFn, "invoke")
	})
	if s.PlanFn != nil {
		mux.HandleFunc("/plan", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				writeErr(w, &HTTPError{Status: 405, Msg: "POST required"})
				return
			}
			s.handle(w, r, s.PlanFn, "plan")
		})
	}
	go func() {
		if err := s.Register(); err != nil {
			log.Printf("[%s] %v", s.Manifest.ID, err)
		}
	}()
	log.Printf("[%s] listening on %s", s.Manifest.ID, addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}

// ParseJSON unmarshals body into v; returns 400 on bad JSON.
func ParseJSON(body []byte, v interface{}) error {
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return &HTTPError{Status: 400, Msg: "invalid JSON: " + err.Error()}
	}
	return nil
}
