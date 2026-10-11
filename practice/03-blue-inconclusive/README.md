# 03 — Blue: slower, never failing, and genuinely debatable

```bash
task scenario:blue
task watch
```

**What changes:** the image becomes `argoproj/rollouts-demo:blue` with `LATENCY=1`, so every `/color` call takes one extra second. (The demo's own `slow-blue` tag uses 2 seconds, which breaks the 1.5s SLO and isn't debatable.)

**What happens:**

1. One pod runs blue. In the UI the blue squares arrive late.
2. The AnalysisRun:
   - `success-rate`: 1.0 → **Successful**. The classic gate sees nothing wrong.
   - `jev-decision`: the canary is inside the SLO (p95 ~1,000ms < 1,500ms) but ~200× slower than stable. A reasonable judge is unsure, so `p_block` lands in the middle band (0.3–0.7). Neither condition matches → **Inconclusive**.
3. Argo pauses the rollout and waits for a human:

   ```bash
   task promote   # "1s is fine for this endpoint"
   task abort     # "200x slower is a regression, roll back"
   ```

## The state the model sees

This is what the gate sends as `state` (from a real local run). Paste it into the OpenJev or TypeSafe playground with a `noul` question to see what your model thinks before you run the lab:

```json
{
  "service": "rollouts-demo (the color API polled by the demo web UI)",
  "slo": { "p95_latency_ms_max": 1500, "success_rate_min": 0.95 },
  "stable": {
    "requests": 20, "success_rate": 1, "p50_ms": 2.7, "p95_ms": 5.3,
    "samples": ["200 \"yellow\" 5ms", "200 \"yellow\" 6ms", "200 \"yellow\" 5ms"]
  },
  "canary": {
    "requests": 20, "success_rate": 1, "p50_ms": 1001.6, "p95_ms": 1004.1,
    "samples": ["200 \"blue\" 1005ms", "200 \"blue\" 1002ms", "200 \"blue\" 1002ms"]
  }
}
```

Question (`noul`, named `block_release`):

> The canary release should be blocked from a full rollout because it would noticeably degrade the user experience compared with the stable release.

## If your model doesn't land in the middle

Real models aren't deterministic, and this case is debatable on purpose. If `p_block` comes back as, say, 0.82, the rollout aborts instead of pausing. Check `task gate:logs`, then decide what you actually want:

- Move the band in `deploy/app/analysis-template.yaml` (`successCondition` / `failureCondition`).
- Change the SLO the model is told about in `deploy/gate/gate.yaml` (`SLO_P95_MS`).
- Re-run `task scenario:green` then `task scenario:blue`.
