#!/usr/bin/env bash
# End-to-end check: run the three scenarios and assert the rollout outcome of each.
set -euo pipefail
NS=demo
wait_for() { # $1 = expected status, $2 = timeout seconds
  local want=$1 t=$2 got
  for _ in $(seq 1 "$t"); do
    got=$(kubectl argo rollouts status rollouts-demo -n $NS --timeout 1s 2>/dev/null | tail -1 || true)
    [[ "$got" == *"$want"* ]] && { echo "   -> $got"; return 0; }
    sleep 1
  done
  echo "   expected '$want', last status: '$got'"; kubectl argo rollouts get rollout rollouts-demo -n $NS; return 1
}
echo "== green: expect Healthy";  task scenario:green; sleep 5; wait_for Healthy 240
echo "== red: expect Degraded";   task scenario:red;   sleep 5; wait_for Degraded 240
echo "== blue: expect Paused on an Inconclusive analysis"; task scenario:blue
for _ in $(seq 1 240); do
  phase=$(kubectl -n $NS get analysisrun --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[-1:].status.phase}' 2>/dev/null || true)
  [[ "$phase" == Inconclusive ]] && break; sleep 1
done
[[ "$phase" == Inconclusive ]] || { echo "   expected Inconclusive, got '$phase'"; exit 1; }
wait_for Paused 30
kubectl -n $NS get analysisrun
echo "all three outcomes as expected"
