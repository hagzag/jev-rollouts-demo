# 01 — Green: the boring, happy path

```bash
task scenario:green
task watch
```

**What changes:** the Rollout's image becomes `argoproj/rollouts-demo:green`.

**What happens:**

1. One of five pods (20%) runs green. The demo UI at http://localhost:8080 starts showing green squares.
2. After a 20s pause, the `jev-gate` AnalysisRun starts both metrics:
   - `success-rate`: the canary answers 100% of probes → **Successful**
   - `jev-decision`: green is as fast as stable, so `p_block` is low (< 0.3) → **Successful**
3. The rollout goes to 60%, then 100%. Status: **Healthy**.

**Check it:**

```bash
task analysis     # both metrics Successful, with their measured values
task gate:logs    # e.g. success=1.000 p95=4ms stable_p95=4ms p_block=0.05
```

Nothing interesting happened, which is exactly what you want from a gate on a healthy release. Note the `decision_ms` in the gate logs: that's what the Jev-style check costs you per analysis.
