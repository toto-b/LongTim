# Longwave Online — Entwicklungs- und Deployment-Targets.
# Alles laeuft in der Nix-DevShell (nix develop) oder mit lokal installierten Tools.

# Bash über PATH auflösen: auf NixOS gibt es kein /bin/bash.
SHELL := $(shell command -v bash 2>/dev/null || echo /bin/sh)

# VERSION landet per -ldflags im Binary und als OCI-Label im Image. Darüber ist
# der Release am laufenden Pod erkennbar: `curl /api/version`.
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

SERVER_IMG  ?= longwave-server
WEB_IMG     ?= longwave-web

# TAG ist nur der Image-Zeiger für den lokalen kind-Ablauf und steht deshalb fest
# auf "dev" — derselbe Wert wie in k8s/kustomization.yaml, damit Manifeste und
# geladene Images ohne weitere Verdrahtung zusammenpassen.
# Für eine echte Registry: TAG=$(VERSION) setzen und die Manifeste mit
# `kustomize edit set image` oder einem Overlay darauf zeigen lassen.
TAG         ?= dev
NAMESPACE   ?= longwave
KIND_CLUSTER?= longwave
MONITORING_NS ?= monitoring
HELM_RELEASE  ?= kps

# Chart-Versionen gepinnt: derselbe Aufruf ergibt in zwei Wochen dasselbe
# Ergebnis (12 Factor II — Abhängigkeiten explizit und festgelegt).
INGRESS_CHART_VERSION ?= 4.15.1
KPS_CHART_VERSION     ?= 88.3.0

# Projektlokale Kubeconfig statt ~/.kube/config. Zwei Gründe: der Cluster dieses
# Projekts vermischt sich nicht mit anderen Kontexten, und `kind`/`kubectl`
# scheitern nicht an einer bereits vorhandenen, kaputten Nutzer-Kubeconfig.
# Für kubectl außerhalb von make: export KUBECONFIG=$(PWD)/.kube/config
export KUBECONFIG ?= $(PWD)/.kube/config

.DEFAULT_GOAL := help

## help: Diese Übersicht anzeigen
.PHONY: help
help:
	@echo "Longwave Online — Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | column -t -s ':'
	@echo ""
	@echo "  VERSION=$(VERSION)"

# ---------------------------------------------------------------------------
# Backend
# ---------------------------------------------------------------------------

## test: Alle Go-Tests mit Race-Detector
.PHONY: test
test:
	cd server && go test ./... -race -count=1

## lint: gofmt-Prüfung und go vet
.PHONY: lint
lint:
	@cd server && test -z "$$(gofmt -l .)" || { echo "nicht formatiert:"; gofmt -l server; exit 1; }
	cd server && go vet ./...

## build: Server-Binary nach server/bin/server bauen
.PHONY: build
build:
	cd server && go build -ldflags "-X main.version=$(VERSION)" -o bin/server ./cmd/server

## validate-scales: Skalendatei prüfen (Admin-Kommando im selben Binary)
.PHONY: validate-scales
validate-scales:
	cd server && go run ./cmd/server --validate-scales

# ---------------------------------------------------------------------------
# Lokaler Entwicklungs-Loop
# ---------------------------------------------------------------------------

## dev: Server auf :8080 starten und das Frontend gleich mitliefern
.PHONY: dev
dev:
	cd server && LOG_FORMAT=text LOG_LEVEL=debug STATIC_DIR=../web \
		go run -ldflags "-X main.version=$(VERSION)" ./cmd/server

# ---------------------------------------------------------------------------
# Container
# ---------------------------------------------------------------------------

## images: Beide Images bauen
.PHONY: images
images: image-server image-web

## image-server: Game-Server-Image bauen
.PHONY: image-server
image-server:
	docker build --build-arg VERSION=$(VERSION) -t $(SERVER_IMG):$(TAG) ./server

## image-web: Frontend-Image bauen
.PHONY: image-web
image-web:
	docker build --build-arg VERSION=$(VERSION) -t $(WEB_IMG):$(TAG) ./web

## smoke: Server-Image starten und Health/Metrics prüfen
.PHONY: smoke
smoke:
	./scripts/smoke.sh $(SERVER_IMG):$(TAG) $(WEB_IMG):$(TAG)

# ---------------------------------------------------------------------------
# Kubernetes (kind)
# ---------------------------------------------------------------------------

## cluster: kind-Cluster anlegen
.PHONY: cluster
cluster:
	@mkdir -p $(dir $(KUBECONFIG))
	kind create cluster --name $(KIND_CLUSTER) --config kind-cluster.yaml --kubeconfig $(KUBECONFIG)

## ingress: ingress-nginx per Helm installieren (gepinnte Chart-Version)
.PHONY: ingress
ingress:
	helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
	helm repo update ingress-nginx
	helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
		--namespace ingress-nginx --create-namespace \
		--version $(INGRESS_CHART_VERSION) \
		-f ingress-nginx-values.yaml \
		--wait --timeout 10m

## kubeconfig: Pfad der projektlokalen Kubeconfig ausgeben
.PHONY: kubeconfig
kubeconfig:
	@echo 'export KUBECONFIG=$(KUBECONFIG)'

## cluster-delete: kind-Cluster entfernen
.PHONY: cluster-delete
cluster-delete:
	kind delete cluster --name $(KIND_CLUSTER) --kubeconfig $(KUBECONFIG)

## load: Images in den kind-Cluster laden (keine Registry nötig)
.PHONY: load
load: images
	kind load docker-image --name $(KIND_CLUSTER) $(SERVER_IMG):$(TAG) $(WEB_IMG):$(TAG)

## manifests: Gerenderte Manifeste nach stdout ausgeben (zum Prüfen)
.PHONY: manifests
manifests:
	@kustomize build --load-restrictor LoadRestrictionsNone k8s

## deploy: Manifeste anwenden und auf den Rollout warten
.PHONY: deploy
deploy:
	# LoadRestrictionsNone, weil die ConfigMaps aus server/ und monitoring/ gelesen
	# werden — eine Quelle je Datei statt Kopien unter k8s/.
	kustomize build --load-restrictor LoadRestrictionsNone k8s | kubectl apply -f -
	# Neustart erzwingen: der Image-Tag bleibt ":dev", der Inhalt aendert sich aber
	# bei jedem `make load`. Ohne das laesst Kubernetes die Pods laufen, weil sich
	# aus seiner Sicht nichts geaendert hat — und man debuggt den alten Stand.
	kubectl -n $(NAMESPACE) rollout restart deploy/longwave-server deploy/longwave-web
	kubectl -n $(NAMESPACE) rollout status deploy/longwave-server --timeout=180s
	kubectl -n $(NAMESPACE) rollout status deploy/longwave-web --timeout=180s

## undeploy: Alle Longwave-Ressourcen entfernen
.PHONY: undeploy
undeploy:
	kubectl delete namespace $(NAMESPACE) --ignore-not-found

## up: Cluster anlegen, Monitoring installieren, Images laden, deployen
.PHONY: up
up:
	# Reihenfolge zaehlt: ServiceMonitor ist eine CRD des Prometheus Operators.
	# Sowohl k8s/50-servicemonitor.yaml als auch das ingress-nginx-Chart legen so
	# eine Ressource an — ohne vorher installiertes Monitoring kennt der Cluster
	# die Art nicht und beide Schritte scheitern.
	$(MAKE) cluster
	$(MAKE) monitoring
	$(MAKE) ingress
	$(MAKE) load
	$(MAKE) deploy
	@echo ""
	@echo "Fertig. Eintrag in /etc/hosts nicht vergessen:"
	@echo "  127.0.0.1 longwave.local"
	@echo "Dann öffnen: http://longwave.local"

## logs: Logs des Game-Servers folgen
.PHONY: logs
logs:
	kubectl -n $(NAMESPACE) logs -l app.kubernetes.io/name=longwave-server -f --tail=50

# ---------------------------------------------------------------------------
# Monitoring (CNCF: Prometheus + Grafana, installiert via Helm)
# ---------------------------------------------------------------------------

## monitoring: kube-prometheus-stack per Helm installieren
.PHONY: monitoring
monitoring:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
	helm repo update prometheus-community
	helm upgrade --install $(HELM_RELEASE) prometheus-community/kube-prometheus-stack \
		--namespace $(MONITORING_NS) --create-namespace \
		--version $(KPS_CHART_VERSION) \
		-f monitoring/kube-prometheus-values.yaml \
		--wait --timeout 15m

## grafana: Grafana auf http://localhost:3000 weiterleiten (admin/longwave)
.PHONY: grafana
grafana:
	@echo "Grafana: http://localhost:3000 — admin / longwave"
	kubectl -n $(MONITORING_NS) port-forward svc/$(HELM_RELEASE)-grafana 3000:80

## prometheus: Prometheus-UI auf http://localhost:9090 weiterleiten
.PHONY: prometheus
prometheus:
	kubectl -n $(MONITORING_NS) port-forward svc/$(HELM_RELEASE)-kube-prometheus-stack-prometheus 9090:9090
