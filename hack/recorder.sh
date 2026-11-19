#!/usr/bin/env bash
# Per-second recorder for a checkout promotion (#21) or a GameDay (#22). Read-only.
# usage: recorder.sh <outdir> <old-stable-hash|-> <max-seconds>
# With a hash: stops 150s after the Rollout is Healthy on another hash. With "-": records max-seconds.
set -uo pipefail
out=$1; old=$2; max=$3
mkdir -p "$out"; rm -f "$out"/*.log
T='(now|strftime("%H:%M:%S"))'

kubectl get pods -n shop -w -o json | jq --unbuffered -c "{t:$T, pod:.metadata.name, role:.metadata.labels[\"cnpg.io/instanceRole\"], hash:.metadata.labels[\"rollouts-pod-template-hash\"], phase:.status.phase, ready:([.status.conditions[]?|select(.type==\"Ready\")|.status]|first), deleting:(.metadata.deletionTimestamp!=null), ip:.status.podIP, node:.spec.nodeName}" > "$out/pods.log" &
w1=$!
kubectl get endpointslices -n shop -w -o json | jq --unbuffered -c "select((.metadata.labels[\"kubernetes.io/service-name\"] // \"\")|test(\"^(checkout|shop-db-rw)\")) | {t:$T, svc:.metadata.labels[\"kubernetes.io/service-name\"], eps:[.endpoints[]?|\"\(.addresses[0]) ready=\(.conditions.ready) serving=\(.conditions.serving) term=\(.conditions.terminating)\"]}" > "$out/eps.log" &
w2=$!
kubectl get rollout checkout -n shop -w -o json | jq --unbuffered -c "{t:$T, phase:.status.phase, step:.status.currentStepIndex, stable:.status.stableRS, current:.status.currentPodHash, msg:(.status.message // \"\")}" > "$out/rollout.log" &
w3=$!
kubectl get httproute checkout -n shop -w -o json | jq --unbuffered -c "{t:$T, weights:[.spec.rules[0].backendRefs[]|\"\(.name)=\(.weight)\"]}" > "$out/route.log" &
w4=$!

kubectl get analysisrun -n shop -w -o json | jq --unbuffered -c "{t:$T, run:.metadata.name, phase:.status.phase, msg:(.status.message // \"\"), metrics:[.status.metricResults[]? | \"\(.name) ok=\(.successful // 0) fail=\(.failed // 0) last=\(.measurements[-1].value // \"-\")\"]}" > "$out/analysis.log" &
hp0=$!
kubectl get cluster shop-db -n shop -w -o json | jq --unbuffered -c "{t:$T, phase:.status.phase, primary:.status.currentPrimary, target:.status.targetPrimary, ready:.status.readyInstances}" > "$out/cnpg.log" &
w5=$!
kubectl get nodes -w -o json | jq --unbuffered -c "{t:$T, node:.metadata.name, ready:([.status.conditions[]|select(.type==\"Ready\")|.status]|first), taints:[.spec.taints[]?.key]}" > "$out/nodes.log" &
w6=$!

# Hubble flows (TCP flags per connection) for checkout:8080, one stream per worker node's agent.
hp=""
for n in worker worker2 worker3; do
  a=$(kubectl get pods -n kube-system -l k8s-app=cilium --field-selector spec.nodeName=sre-platform-$n -o jsonpath='{.items[0].metadata.name}')
  kubectl exec -n kube-system "$a" -c cilium-agent -- hubble observe --follow --pod shop/checkout --port 8080 -o compact > "$out/hubble-$n.log" 2>&1 &
  hp="$hp $!"
done

sf=$(kubectl get pods -n shop -l app=storefront -o jsonpath='{.items[*].metadata.name}')
envoys=$(kubectl get pods -n kube-system -l k8s-app=cilium-envoy -o json | jq -r '.items[] | "\(.metadata.name)@\(.spec.nodeName)"')
keep='upstream_(cx_connect_fail|cx_connect_timeout|cx_destroy_local_with_active_rq|cx_destroy_remote_with_active_rq|cx_none_healthy|rq_pending_failure_eject|rq_pending_overflow|rq_rx_reset|rq_tx_reset|rq_timeout|rq_retry|cx_total|rq_total)\{'

end=$((SECONDS+max)); promoted=0
while [ $SECONDS -lt $end ]; do
  t=$(date -u +%H:%M:%S)
  for s in $sf; do
    kubectl get --request-timeout=2s --raw "/api/v1/namespaces/shop/pods/$s:9090/proxy/metrics" 2>/dev/null | awk -v t="$t" -v p="$s" '/^http_server_requests_total/ {print t, p, $0}' >> "$out/storefront.log"
  done
  for e in $envoys; do
    kubectl get --request-timeout=2s --raw "/api/v1/namespaces/kube-system/pods/${e%@*}:9964/proxy/metrics" 2>/dev/null | grep -E "$keep" | grep -E 'checkout-(stable|canary)' | awk -v t="$t" -v n="${e#*@}" '{print t, n, $0}' >> "$out/envoy.log"
  done
  if [ "$old" != "-" ] && [ $promoted -eq 0 ] && jq -e --arg old "$old" 'select((.phase=="Healthy" and .stable==.current and .stable!=$old) or .phase=="Degraded")' "$out/rollout.log" >/dev/null 2>&1; then
    promoted=1; end=$((SECONDS+150)); echo "promotion or abort seen at $t, recording 150s more"
  fi
done
kill $w1 $w2 $w3 $w4 $w5 $w6 $hp0 $hp 2>/dev/null
pkill -P $$ 2>/dev/null
echo "done promoted=$promoted lines: $(wc -l "$out"/*.log | tr '\n' ' ')"
