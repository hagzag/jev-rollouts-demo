// jev-gate: a tiny HTTP service that Argo Rollouts' web metric provider calls
// during a canary analysis step.
//
// For every GET /evaluate it:
//  1. probes the stable and canary Services of the rollouts-demo app,
//  2. turns what it saw into a JSON "state" (success rate, p95 latency, samples),
//  3. asks a Jev-style decision service ONE typed question (a "noul": yes/no
//     with a probability) via the OpenJev wire format (POST /api/evaluate),
//  4. returns {success_rate, p_block, ...} so the AnalysisTemplate can apply
//     successCondition / failureCondition thresholds.
//
// The same binary can also run as a deterministic fake decider
// (`jev-gate fake-decider`) so the three scenarios reproduce in CI without
// spending tokens. Readers use the hosted or self-hosted decider instead.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	listen         string
	decisionURL    string // OpenJev-compatible endpoint, e.g. https://jev.opendef.com/api/evaluate
	decisionAPIKey string // Bearer token for the hosted service (empty for self-hosted / fake)
	model          string // forwarded as "model" (used by self-hosted OpenJev; hosted uses your account settings)
	mode           string // forwarded as "mode" (OpenJev: parallel | oneshot | decider)
	probes         int
	concurrency    int
	sloSuccess     float64
	sloP95ms       float64
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}

func loadConfig() config {
	return config{
		listen:         env("LISTEN_ADDR", ":8080"),
		decisionURL:    env("DECISION_URL", "http://jev-decider:8080/api/evaluate"),
		decisionAPIKey: os.Getenv("DECISION_API_KEY"),
		model:          os.Getenv("DECISION_MODEL"),
		mode:           os.Getenv("DECISION_MODE"),
		probes:         envInt("PROBES", 20),
		concurrency:    envInt("PROBE_CONCURRENCY", 10),
		sloSuccess:     envFloat("SLO_SUCCESS_RATE", 0.95),
		sloP95ms:       envFloat("SLO_P95_MS", 1500),
	}
}

// ---------------------------------------------------------------- probing

type probeResult struct {
	Requests    int      `json:"requests"`
	SuccessRate float64  `json:"success_rate"`
	P50ms       float64  `json:"p50_ms"`
	P95ms       float64  `json:"p95_ms"`
	Samples     []string `json:"samples"`
}

var probeClient = &http.Client{Timeout: 10 * time.Second}

// probe sends n requests to the rollouts-demo /color endpoint, the same
// endpoint the demo UI polls, and records status + latency.
func probe(baseURL string, n, conc int) probeResult {
	type one struct {
		ok  bool
		ms  float64
		txt string
	}
	results := make([]one, n)
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			resp, err := probeClient.Get(strings.TrimRight(baseURL, "/") + "/color")
			ms := float64(time.Since(start).Microseconds()) / 1000
			if err != nil {
				results[i] = one{false, ms, "error: " + err.Error()}
				return
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			resp.Body.Close()
			results[i] = one{resp.StatusCode < 500, ms,
				fmt.Sprintf("%d %s %.0fms", resp.StatusCode, strings.TrimSpace(string(body)), ms)}
		}(i)
	}
	wg.Wait()

	var okCount int
	lat := make([]float64, 0, n)
	samples := []string{}
	for i, r := range results {
		if r.ok {
			okCount++
		}
		lat = append(lat, r.ms)
		if i < 5 { // a handful of raw lines, like log lines, for the model to read
			samples = append(samples, r.txt)
		}
	}
	sort.Float64s(lat)
	return probeResult{
		Requests:    n,
		SuccessRate: round(float64(okCount)/float64(n), 3),
		P50ms:       round(percentile(lat, 0.50), 1),
		P95ms:       round(percentile(lat, 0.95), 1),
		Samples:     samples,
	}
}

func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// ---------------------------------------------------------------- decision

// The question we ask. One noul = one probability. Phrased so that a high
// probability means "stop this rollout".
const blockQuestion = "The canary release should be blocked from a full rollout because it " +
	"would noticeably degrade the user experience compared with the stable release."

type evaluateRequest struct {
	State     any                       `json:"state"`
	Questions map[string]map[string]any `json:"questions"`
	Model     string                    `json:"model,omitempty"`
	Mode      string                    `json:"mode,omitempty"`
}

type evaluateResponse struct {
	Model   string                    `json:"model"`
	Answers map[string]map[string]any `json:"answers"`
	Error   string                    `json:"error,omitempty"`
}

var decisionClient = &http.Client{Timeout: 25 * time.Second}

func decide(cfg config, state any) (float64, string, error) {
	reqBody := evaluateRequest{
		State: state,
		Questions: map[string]map[string]any{
			"block_release": {"type": "noul", "instructions": blockQuestion},
		},
		Model: cfg.model,
		Mode:  cfg.mode,
	}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, cfg.decisionURL, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if cfg.decisionAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.decisionAPIKey)
	}
	resp, err := decisionClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out evaluateResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, "", fmt.Errorf("decision service returned %d, non-JSON body: %.200s", resp.StatusCode, raw)
	}
	if resp.StatusCode >= 300 || out.Error != "" {
		return 0, out.Model, fmt.Errorf("decision service returned %d: %s", resp.StatusCode, out.Error)
	}
	ans, ok := out.Answers["block_release"]
	if !ok {
		return 0, out.Model, fmt.Errorf("decision service response has no 'block_release' answer: %.200s", raw)
	}
	p, ok := ans["noul"].(float64)
	if !ok {
		return 0, out.Model, fmt.Errorf("'block_release.noul' is not a number: %v", ans["noul"])
	}
	return p, out.Model, nil
}

// ---------------------------------------------------------------- handler

type gateResponse struct {
	SuccessRate float64     `json:"success_rate"`
	CanaryP95ms float64     `json:"canary_p95_ms"`
	StableP95ms float64     `json:"stable_p95_ms"`
	PBlock      *float64    `json:"p_block,omitempty"`
	Model       string      `json:"model,omitempty"`
	DecisionMs  int64       `json:"decision_ms,omitempty"`
	State       interface{} `json:"state"`
}

func evaluateHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		canary := r.URL.Query().Get("canary")
		stable := r.URL.Query().Get("stable")
		if canary == "" || stable == "" {
			http.Error(w, `{"error":"canary and stable query params are required"}`, http.StatusBadRequest)
			return
		}

		var canaryRes, stableRes probeResult
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); canaryRes = probe(canary, cfg.probes, cfg.concurrency) }()
		go func() { defer wg.Done(); stableRes = probe(stable, cfg.probes, cfg.concurrency) }()
		wg.Wait()

		state := map[string]any{
			"service": "rollouts-demo (the color API polled by the demo web UI)",
			"slo": map[string]any{
				"success_rate_min":   cfg.sloSuccess,
				"p95_latency_ms_max": cfg.sloP95ms,
			},
			"stable": stableRes,
			"canary": canaryRes,
		}
		out := gateResponse{
			SuccessRate: canaryRes.SuccessRate,
			CanaryP95ms: canaryRes.P95ms,
			StableP95ms: stableRes.P95ms,
			State:       state,
		}

		// ?decide=false lets the "classic" success-rate metric reuse the probe
		// without paying for a decision call.
		if r.URL.Query().Get("decide") != "false" {
			start := time.Now()
			p, model, err := decide(cfg, state)
			out.DecisionMs = time.Since(start).Milliseconds()
			if err != nil {
				log.Printf("decision error: %v", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway) // Argo marks the measurement as Error
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			p = round(p, 3)
			out.PBlock = &p
			out.Model = model
		}

		log.Printf("canary=%s success=%.3f p95=%.0fms stable_p95=%.0fms p_block=%v decision_ms=%d",
			canary, out.SuccessRate, out.CanaryP95ms, out.StableP95ms, ptr(out.PBlock), out.DecisionMs)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func ptr(p *float64) any {
	if p == nil {
		return "skipped"
	}
	return *p
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "fake-decider" {
		runFakeDecider(env("LISTEN_ADDR", ":8080"))
		return
	}
	cfg := loadConfig()
	mux := http.NewServeMux()
	mux.HandleFunc("/evaluate", evaluateHandler(cfg))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	log.Printf("jev-gate listening on %s, decision_url=%s mode=%q model=%q",
		cfg.listen, cfg.decisionURL, cfg.mode, cfg.model)
	log.Fatal(http.ListenAndServe(cfg.listen, mux))
}
