#!/usr/bin/env bash
# Offline proof of the gate logic, no Kubernetes needed.
# Runs the real rollouts-demo app (built from source) as one stable and three
# canary variants, the deterministic fake decider, and jev-gate, then checks
# that each canary lands in the expected Argo Rollouts outcome.
#
# Usage: hack/local-gate-test.sh <rollouts-demo-binary> <jev-gate-binary> [decision_url]
set -euo pipefail
DEMO=$1; GATE=$2; DECISION_URL=${3:-}
PIDS=()
cleanup() { kill "${PIDS[@]}" 2>/dev/null || true; }
trap cleanup EXIT

start() { "$@" >/dev/null 2>&1 & PIDS+=($!); }
start env COLOR=yellow             "$DEMO" --listen-addr :9100   # stable
start env COLOR=green              "$DEMO" --listen-addr :9101   # healthy canary
start env COLOR=red ERROR_RATE=15  "$DEMO" --listen-addr :9102   # = argoproj/rollouts-demo:bad-red
start env COLOR=blue LATENCY=1     "$DEMO" --listen-addr :9103   # blue, 1s slower, no errors
if [[ -z "$DECISION_URL" ]]; then
  start env LISTEN_ADDR=:9200 "$GATE" fake-decider
  DECISION_URL=http://localhost:9200/api/evaluate
fi
LISTEN_ADDR=:9300 DECISION_URL="$DECISION_URL" "$GATE" > /tmp/jev-gate-test.log 2>&1 & PIDS+=($!)
sleep 2

# Same thresholds as deploy/base/analysis-template.yaml
verdict() {
  python3 - "$1" <<'EOF'
import json, sys
d = json.loads(sys.argv[1])
sr, p = d["success_rate"], d["p_block"]
classic = "Successful" if sr >= 0.95 else "Failed"
jev = "Successful" if p < 0.3 else ("Failed" if p > 0.7 else "Inconclusive")
if "Failed" in (classic, jev):   outcome = "ABORT"
elif "Inconclusive" in (classic, jev): outcome = "PAUSE (Inconclusive)"
else: outcome = "PROMOTE"
print(f"success_rate={sr:<5} canary_p95={d['canary_p95_ms']:>7}ms stable_p95={d['stable_p95_ms']:>5}ms "
      f"p_block={p:<5} classic={classic:<10} jev={jev:<12} -> {outcome}  ({d.get('model')}, {d.get('decision_ms')}ms)")
EOF
}

for c in "green:9101" "bad-red:9102" "blue+1s:9103"; do
  name=${c%%:*}; port=${c##*:}
  printf "%-8s " "$name"
  verdict "$(curl -s "localhost:9300/evaluate?stable=http://localhost:9100&canary=http://localhost:$port")"
done
