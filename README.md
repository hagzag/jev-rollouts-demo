# Jev × Argo Rollouts — a calibrated canary gate

Companion lab for the post **"Promote, Abort, or Ask a Human: A Jev-Style Gate for Argo Rollouts"** by [Haggai Philip Zagury (HagZag)](https://portfolio.hagzag.com).

A classic canary gate asks one question: *did the new version return errors?* This lab adds a second one, asked to a [Jev](https://console.typesafe.ai/playground)-style System 1 decision service: *should this release be blocked because it would degrade the user experience?* The answer is a probability, and Argo Rollouts turns it into one of three outcomes:

| Release | What it does | Classic gate (success rate ≥ 95%) | Jev-style gate (`p_block`) | Rollout |
|---|---|---|---|---|
| `argoproj/rollouts-demo:green` | healthy | ✅ Successful | < 0.3 ✅ Successful | **Promote** |
| `argoproj/rollouts-demo:bad-red` | ~85% HTTP 500 | ❌ Failed | > 0.7 ❌ Failed | **Abort** (roll back) |
| `argoproj/rollouts-demo:blue` + `LATENCY=1` | 1s slower, zero errors | ✅ Successful | **the model's call** | Promote, Abort, or Inconclusive (pause): see `task evidence` |

The interesting row is the last one. A success-rate check can't see a release that's slow but never fails, so the decision is left to the model, and the middle band of its probability maps to Argo's built-in **Inconclusive** state (the rollout stops and waits for a person). Whether a given model actually *uses* that middle band is exactly what `task evidence` measures: it holds each canary and asks the same question 10 times.

Everything runs locally on [`k3d`](https://k3d.io). The app is the official, unmodified [Argo Rollouts demo image](https://github.com/argoproj/rollouts-demo); only its tag and env change per scenario.

## How it works

```
Argo Rollouts canary step
  └─ AnalysisTemplate "jev-gate" (2 web metrics, run in parallel)
       ├─ success-rate  ──GET /evaluate?decide=false──┐
       └─ jev-decision  ──GET /evaluate──────────────┐ │
                                                     ▼ ▼
                                               jev-gate (Go, in-cluster)
                                     probes rollouts-demo-stable and -canary (/color)
                                     builds a JSON "state": success rate, p50/p95, samples
                                                     │
                                POST /api/evaluate  (OpenJev / Jev wire format)
                                {state, questions: {block_release: {type: "noul"}}}
                                                     ▼
                       Option 1: jev.opendef.com (hosted)   or   Option 2: OpenJev in your cluster
                                                     │
                                      {"answers":{"block_release":{"noul":0.5}}}
                                                     ▼
       successCondition: result < 0.3   failureCondition: result > 0.7   else → Inconclusive
```

Key files:

- [`deploy/app/analysis-template.yaml`](deploy/app/analysis-template.yaml): the two metrics and the thresholds
- [`deploy/app/rollout.yaml`](deploy/app/rollout.yaml): 5 replicas, canary 20% → analysis → 60% → 100%
- [`gate/main.go`](gate/main.go): probe, build state, ask one question, return JSON
- [`Taskfile.yml`](Taskfile.yml): everything you run

## Prereqs

- Docker, [`k3d`](https://k3d.io) ≥ v5, `kubectl` ≥ v1.28, [`task`](https://taskfile.dev)
- The Argo Rollouts kubectl plugin: `brew install argoproj/tap/kubectl-argo-rollouts`

```bash
cp .env.example .env   # then pick Option 1 or Option 2 below
```

> **Data note:** the gate sends latency numbers and a few `200 "green" 3ms` lines to the decision service. No PII or PHI, so a hosted service is fine for this lab. For real workloads, read the "What would break" section of the companion post first.

## Option 1 — hosted OpenJev (recommended)

[OpenJev](https://jev.opendef.com/) is an open approximation of TypeSafe Jev: same `state` + typed `questions` request, same `choice` / `score` / `noul` types. It scores each option with an OpenAI-compatible model **you** bring, then normalizes the result.

1. Go to **https://jev.opendef.com** and click **Start free with Google**.
2. In **Settings**, add an LLM configuration with your OpenAI-compatible provider key (an OpenAI key works; `gpt-4o-mini` is a good, cheap default) and **make it the default**. The API calls use your account's default configuration; your provider key stays in your OpenJev account, not in the cluster, so `OPENAI_API_KEY` in `.env` is not used in this option.

   If the gate logs say `No default LLM configuration with a provider key for this account`, this step is missing.
3. In **API keys**, create a key (it starts with `ojv_live_`).
4. Optional: try the question in the OpenJev **Playground** first. Paste a state like the one in [`practice/03-blue-inconclusive`](practice/03-blue-inconclusive/README.md) and a `noul` question.
5. Put the key in `.env`:

   ```env
   DECIDER=hosted
   OPENJEV_API_KEY=ojv_live_...
   ```

6. Bring it up:

   ```bash
   task up
   ```

## Option 2 — self-hosted OpenJev (advanced, optional)

Same request format, same gate, same thresholds. The only difference is that [OpenJev (MIT)](https://github.com/SiliconLabAI/OpenJev) runs inside your cluster and calls your OpenAI-compatible provider directly, so nothing goes through opendef.com. Point `OPENAI_BASE_URL` at a model you serve yourself (vLLM, Ollama, LiteLLM) and nothing leaves your network at all.

```env
DECIDER=self-hosted
OPENAI_API_KEY=sk-...
OPENJEV_MODEL=gpt-4o-mini
# OPENAI_BASE_URL=http://my-vllm.internal:8000/v1
```

```bash
task up          # builds the OpenJev image from a pinned commit; first build takes a few minutes
```

Already ran Option 1? Switch in place with `task decider:self-hosted`. Walkthrough: [`practice/04-self-hosted-decider`](practice/04-self-hosted-decider/README.md).

## Run the three scenarios

Open the demo UI at **http://localhost:8080** (the colored grid shows which version answers) and, in a second terminal:

```bash
task watch
```

Then, one at a time, waiting for each to settle:

| Step | Command | Expect | Lab notes |
|---|---|---|---|
| 1 | `task scenario:green` | Analysis ✔ → Healthy, all pods green | [`practice/01-green-promote`](practice/01-green-promote/README.md) |
| 2 | `task scenario:red` | Analysis ✖ → Degraded, rolled back to green | [`practice/02-red-abort`](practice/02-red-abort/README.md) |
| 3 | `task scenario:blue` | Depends on the model: run it twice and compare | [`practice/03-blue-inconclusive`](practice/03-blue-inconclusive/README.md) |

Useful while it runs:

```bash
task gate:logs   # every decision: success rate, p95, p_block, model, decision time
task analysis    # per-metric phase and measured value of the latest AnalysisRun
task promote     # your call on an Inconclusive pause: continue
task abort       # your call on an Inconclusive pause: roll back
```

> **Real models aren't deterministic.** Rows 1 and 2 are clear-cut. Row 3 is a judgment call by design, so a real model may land just outside the 0.3–0.7 band. That's the point of the lab, not a bug: look at `p_block` in `task gate:logs`, then move the thresholds in [`analysis-template.yaml`](deploy/app/analysis-template.yaml) or the SLO in [`deploy/gate/gate.yaml`](deploy/gate/gate.yaml) and run it again.

## Test without a cluster or a model

The gate logic can be proven offline. This builds the real rollouts-demo app from source, runs it as a stable and three canaries, and uses a deterministic fake decider:

```bash
task test:local
```

```
green    success_rate=1     canary_p95=    3.7ms stable_p95=  3.5ms p_block=0.05  classic=Successful jev=Successful   -> PROMOTE
bad-red  success_rate=0.25  canary_p95=    4.3ms stable_p95=  5.1ms p_block=0.95  classic=Failed     jev=Failed       -> ABORT
blue+1s  success_rate=1     canary_p95=   1002ms stable_p95=  1.3ms p_block=0.5   classic=Successful jev=Inconclusive -> PAUSE (Inconclusive)
```

`DECIDER=fake task up` runs the same fake decider inside the cluster (used by CI; not a reader option).

## Cleanup

```bash
task down
```

## License

MIT. The demo app images are © the Argo Project (Apache-2.0). OpenJev is © SiliconLabAI (MIT).
