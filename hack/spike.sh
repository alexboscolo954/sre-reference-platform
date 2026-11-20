#!/usr/bin/env bash
# Chaos Mesh isolation spike for #21. Throwaway: everything it creates is deleted at the end.
set -uo pipefail
k=kubectl
rtt() { # rtt <ns> <target-ip>  -> avg round trip in ms over 5 pings
  $k exec -n "$1" spike -- ping -c 5 -q "$2" 2>&1 | awk -F'[=/ ]+' '/round-trip|rtt/ {print $(NF-2) " ms avg"} /100% packet loss/ {print "100% loss"}'
}
chaos() { # chaos <name> <object-ns> <selector-ns>
  cat <<EOF | $k apply -f - 2>&1
apiVersion: chaos-mesh.org/v1alpha1
kind: NetworkChaos
metadata: {name: $1, namespace: $2}
spec:
  action: delay
  mode: all
  selector:
    namespaces: [$3]
    labelSelectors: {app: chaos-spike}
  delay: {latency: 200ms}
EOF
}
status() { # status <name> <ns>
  $k get networkchaos "$1" -n "$2" -o json 2>/dev/null | jq -c '{conditions: ([.status.conditions[]? | select(.status=="True") | .type]), records: ([.status.experiment.containerRecords[]? | "\(.id) \(.phase)"])}'
}

echo "== 1. chaos-daemon mounts and view of the host"
$k get ds chaos-daemon -n chaos-mesh -o json | jq -c '.spec.template.spec | {hostPID, privileged: .containers[0].securityContext.privileged, hostPaths: [.volumes[] | select(.hostPath) | .hostPath.path]}'
d=$($k get pods -n chaos-mesh -l app.kubernetes.io/component=chaos-daemon -o jsonpath='{.items[0].metadata.name}')
echo "daemon pod: $d on $($k get pod "$d" -n chaos-mesh -o jsonpath='{.spec.nodeName}')"
$k exec -n chaos-mesh "$d" -- sh -c 'ls -l /var/run/docker.sock 2>&1 | tail -1; ls /run/containerd/containerd.sock; echo "kubelets visible: $(ps -e -o comm= | grep -c "^kubelet$")"; echo "containerd visible: $(ps -e -o comm= | grep -c "^containerd$")"; echo "processes visible: $(ps -e | wc -l)"' 2>&1

echo "== setup"
$k create namespace chaos-spike 2>&1
for ns in shop chaos-spike; do
  $k run spike -n $ns --image=busybox:1.37 --labels=app=chaos-spike --restart=Never -- sleep 3600 2>&1
done
$k wait pod/spike -n shop --for=condition=Ready --timeout=120s 2>&1
$k wait pod/spike -n chaos-spike --for=condition=Ready --timeout=120s 2>&1
target=$($k get pods -n kube-system -l k8s-app=kube-dns -o jsonpath='{.items[0].status.podIP}')
echo "ping target (a CoreDNS pod): $target"
echo "namespace annotations: shop=$($k get ns shop -o jsonpath='{.metadata.annotations.chaos-mesh\.org/inject}') chaos-spike=$($k get ns chaos-spike -o jsonpath='{.metadata.annotations.chaos-mesh\.org/inject}')"

echo "== baseline"
echo "shop pod:        $(rtt shop "$target")"
echo "chaos-spike pod: $(rtt chaos-spike "$target")"

echo "== 3+4. 200ms delay on the shop pod"
chaos spike-shop shop shop
$k wait networkchaos/spike-shop -n shop --for=jsonpath='{.status.conditions[?(@.type=="AllInjected")].status}'=True --timeout=90s 2>&1
echo "status: $(status spike-shop shop)"
echo "shop pod:        $(rtt shop "$target")"
echo "chaos-spike pod: $(rtt chaos-spike "$target")"

echo "== 2a. NetworkChaos created inside the unannotated namespace"
chaos spike-own chaos-spike chaos-spike
echo "== 2b. NetworkChaos created in shop but selecting the unannotated namespace"
chaos spike-cross shop chaos-spike
$k wait networkchaos/spike-own -n chaos-spike --for=jsonpath='{.status.conditions[?(@.type=="AllInjected")].status}'=True --timeout=45s 2>&1 | tail -1
$k wait networkchaos/spike-cross -n shop --for=jsonpath='{.status.conditions[?(@.type=="AllInjected")].status}'=True --timeout=45s 2>&1 | tail -1
echo "2a status: $(status spike-own chaos-spike)"
echo "2b status: $(status spike-cross shop)"
echo "chaos-spike pod: $(rtt chaos-spike "$target")"

echo "== recovery: delete the shop delay"
$k delete networkchaos spike-shop -n shop --wait=true --timeout=60s 2>&1
echo "shop pod:        $(rtt shop "$target")"

echo "== cleanup"
$k delete networkchaos spike-cross -n shop --ignore-not-found --timeout=60s 2>&1
$k delete networkchaos spike-own -n chaos-spike --ignore-not-found --timeout=60s 2>&1
$k delete pod spike -n shop --grace-period=0 --force 2>&1 | tail -1
$k delete namespace chaos-spike --timeout=120s 2>&1
echo "left over: networkchaos=$($k get networkchaos -A --no-headers 2>/dev/null | wc -l | tr -d ' ') spike-pods=$($k get pods -A -l app=chaos-spike --no-headers 2>/dev/null | wc -l | tr -d ' ') ns=$($k get ns chaos-spike --no-headers 2>/dev/null | wc -l | tr -d ' ')"
