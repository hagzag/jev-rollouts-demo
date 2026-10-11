# 04 — Self-hosted decider (advanced, optional)

Same gate, same question, same thresholds. Only the URL the gate calls changes: instead of `https://jev.opendef.com/api/evaluate`, it calls OpenJev running next to it in `jev-system`.

```env
# .env
DECIDER=self-hosted
OPENAI_API_KEY=sk-...
OPENJEV_MODEL=gpt-4o-mini
# OPENAI_BASE_URL=http://my-vllm.internal:8000/v1   # a model you serve yourself
```

```bash
task decider:self-hosted   # on an existing cluster
# or, from scratch:  task up
```

**What it does:**

1. Builds [OpenJev](https://github.com/SiliconLabAI/OpenJev) (MIT) from a pinned commit into `openjev:local` and imports it into k3d.
2. Stores `OPENAI_API_KEY` as the `openjev-credentials` Secret and the model / base URL as the `openjev-config` ConfigMap.
3. Points the gate at `http://openjev:3001/api/evaluate` in `parallel` mode (one tiny probability call per option, then normalized).

**Check it:**

```bash
kubectl -n jev-system logs deploy/openjev
kubectl -n jev-system port-forward svc/openjev 3001   # OpenJev's own playground at http://localhost:3001
```

**Why bother:** with `OPENAI_BASE_URL` pointing at a model you run (vLLM, Ollama, LiteLLM), the decision never leaves your network. For this lab that doesn't matter (no PII). For regulated workloads it's the difference between "a vendor" and "infrastructure".

**What you give up:** OpenJev is an approximation built on a chat model. The latency is a normal LLM call, and the probability is a normalized score, not a trained calibration. Measure it on your own releases before you trust the thresholds.
