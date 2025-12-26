# ZeroFS CSI Driver E2E Validation Makefile

CLUSTER_NAME ?= zerofs-test
IMAGE_NAME ?= halceon/zerofs-csi-driver:latest
BENCH_IMAGE ?= halceon/zerofs-bench:latest
ZEROFSDIR ?= /shared/github/ZeroFS
# Detect architecture for Go build (linux/amd64 or linux/arm64)
ARCH ?= $(shell go env GOARCH)
OS ?= $(shell go env GOOS)
GOTOOLCHAIN ?= auto

.PHONY: all
all: run-validation

.PHONY: setup-infra
setup-infra:
	@echo "Creating k3d cluster '$(CLUSTER_NAME)'..."
	@echo "Note: NBD support is required on the host (e.g. 'sudo modprobe nbd max_part=100')."
	@echo "Mounting host /dev into k3d nodes to expose /dev/nbd* for CSI tests..."
	k3d cluster create $(CLUSTER_NAME) --agents 2 --wait --volume /dev:/dev@all
	@echo "Deploying MinIO..."
	kubectl apply -f validation/minio.yaml
	@echo "Waiting for MinIO to be ready..."
	kubectl rollout status deployment/minio -n default --timeout=180s
	@echo "Waiting for MinIO bucket init job to complete..."
	kubectl wait --for=condition=complete job/create-bucket -n default --timeout=180s

# docker build -t $(IMAGE_NAME) .
.PHONY: build
build:
	@echo "Building Go binary for linux/$(ARCH)..."
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 GOTOOLCHAIN=$(GOTOOLCHAIN) go build -v -o out/zerofs-csi-driver-linux-$(ARCH) ./cmd/driver
	@echo "Building Docker image '$(IMAGE_NAME)'..."
	docker buildx build --builder default --platform linux/$(ARCH) --load -t $(IMAGE_NAME) -f Dockerfile .
	@echo "Pushing Docker image '$(IMAGE_NAME)'..."
	docker push $(IMAGE_NAME)
	@echo "Docker image '$(IMAGE_NAME)' built and pushed (linux/$(ARCH))."

.PHONY: build-local
build-local:
	@echo "Building Go binary for linux/$(ARCH)..."
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 GOTOOLCHAIN=$(GOTOOLCHAIN) go build -v -o out/zerofs-csi-driver-linux-$(ARCH) ./cmd/driver
	@echo "Building Docker image '$(IMAGE_NAME)' (local load, no push)..."
	docker buildx build --builder default --platform linux/$(ARCH) --load -t $(IMAGE_NAME) -f Dockerfile .
	@echo "Docker image '$(IMAGE_NAME)' built (linux/$(ARCH))."

.PHONY: deploy
deploy:
	@echo "Importing image '$(IMAGE_NAME)' into cluster..."
	k3d image import $(IMAGE_NAME) -c $(CLUSTER_NAME)
	@echo "Deploying ZeroFS Driver..."
	kubectl apply -f validation/zerofs-driver.yaml
	@echo "Applying ZeroFS Configuration..."
	kubectl apply -f validation/zerofs-config.yaml
	@echo "Setting driver images to '$(IMAGE_NAME)'..."
	kubectl -n zerofs set image deployment/zerofs-csi-controller zerofs-csi-controller=$(IMAGE_NAME)
	kubectl -n zerofs set image daemonset/zerofs-csi-node zerofs-csi-node=$(IMAGE_NAME)
	@echo "Waiting for Driver to roll out..."
	kubectl rollout status deployment/zerofs-csi-controller -n zerofs --timeout=120s
	kubectl rollout status daemonset/zerofs-csi-node -n zerofs --timeout=120s

.PHONY: test
test:
	@echo "Running FIO Benchmark..."
	kubectl apply -f validation/fio.yaml
	@echo "Waiting for PVC to bind..."
	kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/fio-pvc -n default --timeout=300s
	@echo "Waiting for FIO to finish..."
	kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/fio-benchmark -n default --timeout=900s
	@echo "FIO output:"
	kubectl logs fio-benchmark -n default

.PHONY: test-external
test-external:
	@echo "Running durability-sensitive FIO benchmark (StorageClass: zerofs-external)..."
	kubectl apply -f validation/fio-external.yaml
	@echo "Waiting for PVC to bind..."
	kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/fio-external-pvc -n default --timeout=300s
	@echo "Waiting for FIO to finish (Succeeded/Failed)..."
	@bash -lc 'set -euo pipefail; for _ in $$(seq 1 360); do phase=$$(kubectl get pod fio-external -n default -o jsonpath="{.status.phase}" 2>/dev/null || true); if [ "$${phase}" = "Succeeded" ] || [ "$${phase}" = "Failed" ]; then echo "pod phase: $${phase}"; break; fi; sleep 5; done; phase=$$(kubectl get pod fio-external -n default -o jsonpath="{.status.phase}"); [ "$${phase}" = "Succeeded" ]'
	@echo "FIO output:"
	kubectl logs fio-external -n default

.PHONY: test-external-ext4
test-external-ext4:
	@echo "Running durability-sensitive FIO benchmark (StorageClass: zerofs-external-ext4)..."
	kubectl apply -f validation/zerofs-external-ext4-storageclass.yaml
	kubectl apply -f validation/fio-external-ext4.yaml
	@echo "Waiting for PVC to bind..."
	kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/fio-external-ext4-pvc -n default --timeout=300s
	@echo "Waiting for FIO to finish (Succeeded/Failed)..."
	@bash -lc 'set -euo pipefail; for _ in $$(seq 1 360); do phase=$$(kubectl get pod fio-external-ext4 -n default -o jsonpath="{.status.phase}" 2>/dev/null || true); if [ "$${phase}" = "Succeeded" ] || [ "$${phase}" = "Failed" ]; then echo "pod phase: $${phase}"; break; fi; sleep 5; done; phase=$$(kubectl get pod fio-external-ext4 -n default -o jsonpath="{.status.phase}"); [ "$${phase}" = "Succeeded" ]'
	@echo "FIO output:"
	kubectl logs fio-external-ext4 -n default

.PHONY: soak
soak:
	@echo "Running long-running soak job (see validation/fio-soak.yaml)..."
	kubectl apply -f validation/fio-soak.yaml
	@echo "Waiting for soak Job to complete..."
	kubectl wait --for=condition=complete job/fio-soak -n default --timeout=7200s
	@echo "Soak output:"
	kubectl logs -n default job/fio-soak

.PHONY: bench-image
bench-image:
	@echo "Building ZeroFS bench image '$(BENCH_IMAGE)' from '$(ZEROFSDIR)/bench'..."
	docker build -f validation/zerofs-bench.Dockerfile -t $(BENCH_IMAGE) $(ZEROFSDIR)/bench
	@echo "Built ZeroFS bench image locally (not pushed)."

.PHONY: bench
bench: bench-image
	@echo "Importing bench image '$(BENCH_IMAGE)' into cluster..."
	k3d image import $(BENCH_IMAGE) -c $(CLUSTER_NAME)
	@echo "Running ZeroFS bench suite on a ZeroFS-backed PVC..."
	kubectl apply -f validation/zerofs-bench.yaml
	kubectl wait --for=condition=complete job/zerofs-bench -n default --timeout=1800s
	@echo "Bench output:"
	kubectl logs -n default job/zerofs-bench

.PHONY: logs
logs:
	@echo "Collecting logs to validation-logs.txt..."
	@echo "=== ZeroFS CSI Controller Logs ===" > validation-logs.txt
	@kubectl logs -n zerofs -l app=zerofs-csi-controller --all-containers --tail=-1 >> validation-logs.txt 2>&1 || true
	@echo "\n=== ZeroFS CSI Node Logs ===" >> validation-logs.txt
	@kubectl logs -n zerofs -l app=zerofs-csi-node --all-containers --tail=-1 >> validation-logs.txt 2>&1 || true
	@echo "\n=== FIO Benchmark Logs/Describe ===" >> validation-logs.txt
	@kubectl get pod fio-benchmark -n default >/dev/null 2>&1 && { kubectl describe pod fio-benchmark -n default >> validation-logs.txt 2>&1; kubectl logs fio-benchmark -n default >> validation-logs.txt 2>&1; } || echo "(fio-benchmark not found)" >> validation-logs.txt
	@echo "\n=== External FIO Benchmark Logs/Describe ===" >> validation-logs.txt
	@kubectl get pod fio-external -n default >/dev/null 2>&1 && { kubectl describe pod fio-external -n default >> validation-logs.txt 2>&1; kubectl logs fio-external -n default >> validation-logs.txt 2>&1; } || echo "(fio-external not found)" >> validation-logs.txt
	@echo "\n=== External FIO (ext4) Logs/Describe ===" >> validation-logs.txt
	@kubectl get pod fio-external-ext4 -n default >/dev/null 2>&1 && { kubectl describe pod fio-external-ext4 -n default >> validation-logs.txt 2>&1; kubectl logs fio-external-ext4 -n default >> validation-logs.txt 2>&1; } || echo "(fio-external-ext4 not found)" >> validation-logs.txt
	@echo "\n=== Soak Job Logs/Describe ===" >> validation-logs.txt
	@kubectl get job fio-soak -n default >/dev/null 2>&1 && { kubectl describe job fio-soak -n default >> validation-logs.txt 2>&1; kubectl logs -n default job/fio-soak >> validation-logs.txt 2>&1; } || echo "(fio-soak not found)" >> validation-logs.txt
	@echo "\n=== ZeroFS Bench Logs/Describe ===" >> validation-logs.txt
	@kubectl get job zerofs-bench -n default >/dev/null 2>&1 && { kubectl describe job zerofs-bench -n default >> validation-logs.txt 2>&1; kubectl logs -n default job/zerofs-bench >> validation-logs.txt 2>&1; } || echo "(zerofs-bench not found)" >> validation-logs.txt
	@kubectl get job zerofs-bench-external -n default >/dev/null 2>&1 && { kubectl describe job zerofs-bench-external -n default >> validation-logs.txt 2>&1; kubectl logs -n default job/zerofs-bench-external >> validation-logs.txt 2>&1; } || echo "(zerofs-bench-external not found)" >> validation-logs.txt
	@echo "Logs exported to validation-logs.txt"

.PHONY: nbd-regression
nbd-regression:
	@echo "Running NBD leak regression (ITERATIONS=$(ITERATIONS), CHAOS=$(CHAOS))..."
	@bash hack/nbd_regression.sh

.PHONY: nbd-regression-chaos
nbd-regression-chaos:
	@$(MAKE) nbd-regression CHAOS=1

.PHONY: teardown
teardown:
	@echo "Deleting k3d cluster '$(CLUSTER_NAME)'..."
	k3d cluster delete $(CLUSTER_NAME)

.PHONY: run-validation
run-validation: setup-infra build deploy test
	@echo "Validation started. To verify results, run 'make logs' or check pods manually."
	@echo "NBD support is required on the host for FIO tests to succeed."

.PHONY: run-validation-full
run-validation-full: setup-infra build deploy test bench
	@echo "Full validation (including ZeroFS bench) completed."
