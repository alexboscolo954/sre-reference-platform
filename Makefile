CLUSTER     := sre-platform
KIND_DIR    := infrastructure/local-kind
GATEWAY_API := v1.6.3
CILIUM      := 1.20.3
URL         := http://localhost:8080

# Isolation from other clusters on this machine (same values as mise.toml [env])
export KUBECONFIG := $(CURDIR)/kubeconfig
export KIND_EXPERIMENTAL_DOCKER_NETWORK := $(CLUSTER)

ARGOCD      := 10.11.1
AGE_KEY     := $(HOME)/.config/sops/age/keys.txt

.PHONY: up gitops down smoke url

up:
	kind create cluster --config $(KIND_DIR)/kind-config.yaml
	kubectl apply --server-side -f https://github.com/kubernetes-sigs/gateway-api/releases/download/$(GATEWAY_API)/standard-install.yaml
	helm upgrade --install cilium cilium --repo https://helm.cilium.io --version $(CILIUM) \
		-n kube-system -f $(KIND_DIR)/cilium-values.yaml
	cilium status --wait --wait-duration 15m
	$(MAKE) gitops

gitops:
	kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
	kubectl -n argocd create secret generic sops-age --from-file=keys.txt=$(AGE_KEY) --dry-run=client -o yaml | kubectl apply -f -
	helm upgrade --install argo-cd argo-cd --repo https://argoproj.github.io/argo-helm --version $(ARGOCD) \
		-n argocd -f gitops/argocd-values.yaml --wait
	kubectl apply -f gitops/root-app.yaml

down:
	kind delete cluster --name $(CLUSTER)
	-docker network rm $(CLUSTER)

smoke:
	kubectl apply -f $(KIND_DIR)/smoke.yaml
	kubectl -n smoke rollout status deploy/podinfo --timeout=120s
	kubectl -n smoke wait gateway/smoke --for=condition=Programmed --timeout=120s
	curl -fsS -o /dev/null -w '%{http_code}\n' --retry 10 --retry-all-errors --retry-delay 3 $(URL)

url:
	@echo $(URL)
