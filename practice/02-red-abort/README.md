# 02 — Red: a broken release the old gate already catches

```bash
task scenario:red
task watch
```

**What changes:** the image becomes `argoproj/rollouts-demo:bad-red`. The demo builds that tag with `ERROR_RATE=15`, which (despite the name) means only ~15% of requests succeed.

**What happens:**

1. One pod runs bad-red. Red squares show up in the UI, many of them failing.
2. The AnalysisRun:
   - `success-rate`: ~0.15–0.30 → **Failed**
   - `jev-decision`: `p_block` high (> 0.7) → **Failed**
3. Argo aborts the rollout, scales the canary down and keeps the previous version. Status: **Degraded** (the spec still says bad-red, but no traffic goes to it).

**The honest part:** the classic success-rate metric alone would have caught this. The Jev-style gate agrees, but it adds nothing here except cost. Don't sell a decision model on the cases a threshold already handles.
