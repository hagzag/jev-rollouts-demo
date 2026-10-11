package main

// A deterministic stand-in for a Jev-style decision service. It speaks the
// same wire format as OpenJev (POST /api/evaluate) but answers with fixed
// rules instead of a model, so the three demo scenarios always produce the
// same outcome. It exists for CI and for offline testing; the demo itself
// uses the hosted or self-hosted decider.

import (
	"encoding/json"
	"log"
	"net/http"
)

type fakeState struct {
	SLO struct {
		P95Max float64 `json:"p95_latency_ms_max"`
	} `json:"slo"`
	Stable probeResult `json:"stable"`
	Canary probeResult `json:"canary"`
}

func fakeBlockProbability(s fakeState) float64 {
	switch {
	case s.Canary.SuccessRate < 0.90:
		return 0.95 // clearly broken
	case s.SLO.P95Max > 0 && s.Canary.P95ms > s.SLO.P95Max:
		return 0.90 // breaks the latency SLO
	case s.Canary.P95ms > 20*maxf(s.Stable.P95ms, 1):
		return 0.50 // inside the SLO but far slower than stable: a judgment call
	default:
		return 0.05
	}
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func runFakeDecider(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/evaluate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			State     json.RawMessage           `json:"state"`
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		var s fakeState
		_ = json.Unmarshal(req.State, &s)
		p := fakeBlockProbability(s)
		answers := map[string]any{}
		for name := range req.Questions {
			answers[name] = map[string]any{"type": "noul", "noul": p}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "fake-decider",
			"answers": answers,
			"usage":   map[string]any{"input_tokens": nil, "output_tokens": nil},
		})
	})
	log.Printf("fake decider listening on %s (deterministic, for CI)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
