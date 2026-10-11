#!/usr/bin/env bash
# Evidence run: yellow (reset) -> green -> red -> blue.
# For each release it waits until the canary pod is actually serving, HOLDS the
# rollout, asks the gate N times on the same canary (repeatability), then lets
# Argo Rollouts' own analysis decide. Everything lands in evidence/<timestamp>/.
set -uo pipefail
NS=demo; GNS=jev-system; R=rollouts-demo; N=${N:-10}
OUT="evidence/$(date +%Y%m%d-%H%M%S)"; mkdir -p "$OUT"
log() { echo "[$(date +%H:%M:%S)] $*" | tee -a "$OUT/summary.txt"; }
jp()  { kubectl -n $NS get rollout $R -o jsonpath="$1" 2>/dev/null; }

kubectl -n $GNS port-forward svc/jev-gate 18080:8080 >/dev/null 2>&1 & PF=$!
trap 'kill $PF 2>/dev/null; kubectl -n $NS patch rollout $R --type merge -p "{\"spec\":{\"paused\":false}}" >/dev/null 2>&1' EXIT
sleep 3
gate_call() { curl -s --max-time 40 "http://localhost:18080/evaluate?canary=http://rollouts-demo-canary.demo.svc.cluster.local&stable=http://rollouts-demo-stable.demo.svc.cluster.local" > "$1"; }
field() { grep -o "\"$2\":[^,}]*" "$1" | head -1 | cut -d: -f2 | tr -d '"'; }
echo "scenario,call,success_rate,canary_p95_ms,stable_p95_ms,p_block,decision_ms" > "$OUT/decisions.csv"

wait_canary_serving() { # until the new canary pod is ready behind rollouts-demo-canary
  local h s eps
  for _ in $(seq 1 120); do
    h=$(jp '{.status.currentPodHash}'); s=$(jp '{.status.stableRS}')
    eps=$(kubectl -n $NS get endpoints rollouts-demo-canary -o jsonpath='{.subsets[*].addresses[*].targetRef.name}' 2>/dev/null)
    sel=$(kubectl -n $NS get svc rollouts-demo-canary -o jsonpath='{.spec.selector.rollouts-pod-template-hash}' 2>/dev/null)
    # the endpoint list can show the new pod before Argo has switched the canary
    # Service selector, so also require the selector to point at the new hash,
    # then give kube-proxy a few seconds (the first run measured stable twice)
    if [[ -n "$h" && "$h" != "$s" && "$sel" == "$h" && "$eps" == *"$R-$h-"* ]]; then sleep 5; return 0; fi
    sleep 1
  done; return 1
}

reset_to_yellow() {
  log "=== reset: stable -> yellow (skips analysis with promote --full)"
  kubectl -n $NS patch rollout $R --type=json -p '[{"op":"replace","path":"/spec/template/spec/containers/0","value":{"name":"rollouts-demo","image":"argoproj/rollouts-demo:yellow","ports":[{"name":"http","containerPort":8080}],"env":[],"readinessProbe":{"httpGet":{"path":"/","port":"http"}},"resources":{"requests":{"cpu":"5m","memory":"32Mi"}}}}]' >/dev/null
  kubectl -n $NS patch rollout $R --type merge -p '{"spec":{"paused":false}}' >/dev/null
  sleep 3; kubectl argo rollouts promote $R -n $NS --full >/dev/null 2>&1
  for _ in $(seq 1 180); do [[ "$(jp '{.status.phase}')" == Healthy && "$(jp '{.spec.template.spec.containers[0].image}')" == *yellow ]] && break; sleep 1; done
  log "  phase=$(jp '{.status.phase}') image=$(jp '{.spec.template.spec.containers[0].image}')"
}

run() { # $1 name, $2 task
  local name=$1 t=$2 start t0 ph rs ar
  log "=== $name: task $t"
  start=$(date -u +%Y-%m-%dT%H:%M:%SZ); t0=$(date +%s)
  task "$t" >/dev/null
  if ! wait_canary_serving; then log "  canary never became ready"; return; fi
  kubectl -n $NS patch rollout $R --type merge -p '{"spec":{"paused":true}}' >/dev/null   # hold before the analysis step
  log "  canary serving after $(( $(date +%s) - t0 ))s; rollout held; asking the gate $N times"
  for i in $(seq 1 "$N"); do
    gate_call "$OUT/$name-call-$i.json"
    echo "$name,$i,$(field "$OUT/$name-call-$i.json" success_rate),$(field "$OUT/$name-call-$i.json" canary_p95_ms),$(field "$OUT/$name-call-$i.json" stable_p95_ms),$(field "$OUT/$name-call-$i.json" p_block),$(field "$OUT/$name-call-$i.json" decision_ms)" >> "$OUT/decisions.csv"
  done
  log "  p_block over $N calls: $(grep "^$name," "$OUT/decisions.csv" | cut -d, -f6 | sort | uniq -c | awk '{printf "%sx%s ", $1, $2}')"
  kubectl -n $NS patch rollout $R --type merge -p '{"spec":{"paused":false}}' >/dev/null   # release: Argo decides
  for _ in $(seq 1 300); do
    ph=$(jp '{.status.phase}'); rs=$(jp '{.status.pauseConditions[*].reason}')
    [[ "$ph" == Healthy || "$ph" == Degraded || "$rs" == *InconclusiveAnalysisRun* ]] && break
    sleep 1
  done
  log "  ARGO OUTCOME: phase=$(jp '{.status.phase}') pause=$(jp '{.status.pauseConditions[*].reason}') ($(( $(date +%s) - t0 ))s after release)"
  kubectl argo rollouts get rollout $R -n $NS --no-color > "$OUT/$name-rollout.txt" 2>&1
  ar=$(kubectl -n $NS get analysisrun --sort-by=.metadata.creationTimestamp -o name | tail -1)
  kubectl -n $NS get "$ar" -o yaml > "$OUT/$name-analysisrun.yaml"
  kubectl -n $NS get "$ar" -o jsonpath='  analysisrun {.metadata.name}: {.status.phase}{"\n"}{range .status.metricResults[*]}    {.name}: {.phase} value={.measurements[-1].value}{"\n"}{end}' | tee -a "$OUT/summary.txt"
  kubectl -n $GNS logs deploy/jev-gate --since-time="$start" > "$OUT/$name-gate.log" 2>&1
  # if blue ended Inconclusive, leave it for the human; otherwise continue
}

kubectl -n $GNS get deploy jev-gate -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}' | grep -v API_KEY > "$OUT/gate-env.txt"
reset_to_yellow
run green scenario:green
run red   scenario:red
run blue  scenario:blue
log "done. Evidence in $OUT"
log "If the rollout is paused (Inconclusive), decide with: task promote | task abort"
