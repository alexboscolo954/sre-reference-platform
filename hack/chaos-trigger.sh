#!/usr/bin/env bash
# Waits for a checkout canary (a hash other than <old>) with a Ready pod, then runs `make chaos`
# and logs the experiment's injection status for a minute. Run from the repo root.
# usage: chaos-trigger.sh <old-stable-hash> <logfile>
set -uo pipefail
old=$1; log=$2; : > "$log"
for i in $(seq 1 600); do
  cur=$(kubectl get rollout checkout -n shop -o jsonpath='{.status.currentPodHash}' 2>/dev/null)
  if [ -n "$cur" ] && [ "$cur" != "$old" ]; then
    ready=$(kubectl get pods -n shop -l "rollouts-pod-template-hash=$cur" -o json | jq '[.items[] | select(any(.status.conditions[]?; .type=="Ready" and .status=="True"))] | length')
    if [ "${ready:-0}" -ge 1 ]; then
      echo "$(date -u +%H:%M:%S) canary $cur has a Ready pod, running make chaos" >> "$log"
      make chaos >> "$log" 2>&1
      echo "$(date -u +%H:%M:%S) make chaos exit=$?" >> "$log"
      for j in $(seq 1 12); do
        echo "$(date -u +%H:%M:%S) $(kubectl get networkchaos checkout-canary-delay -n shop -o json 2>/dev/null | jq -c '{true_conditions: [.status.conditions[]? | select(.status=="True") | .type], records: [.status.experiment.containerRecords[]? | "\(.id) \(.phase)"]}')" >> "$log"
        sleep 5
      done
      exit 0
    fi
  fi
  sleep 2
done
echo "$(date -u +%H:%M:%S) timeout: no canary appeared" >> "$log"; exit 1
