# ZeroFS CSI Driver E2E Validation Makefile

CLUSTER_NAME ?= zerofs-test
IMAGE_NAME ?= halceon/zerofs-csi-driver:latest
# Detect architecture for Go build (linux/amd64 or linux/arm64)
ARCH ?= $(shell go env GOARCH)
OS ?= $(shell go env GOOS)

.PHONY: all
all: run-validation

.PHONY: setup-infra
setup-infra:
	@echo "Creating k3d cluster '$(CLUSTER_NAME)'..."
	k3d cluster create $(CLUSTER_NAME) --agents 2 --wait
	@echo "Deploying MinIO..."
	kubectl apply -f validation/minio.yaml
	@echo "Waiting for MinIO to be ready..."
	kubectl roll out status deployment/minio -n default --timeout=60s

# docker build -t $(IMAGE_NAME) .
.PHONY: build
build:
	@echo "Building Go binary for linux/$(ARCH)..."
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 go build -v -o out/zerofs-csi-driver-linux-$(ARCH) ./cmd/driver
	@echo "Building Docker image '$(IMAGE_NAME)'..."
	dbuildx build \
		-t $(IMAGE_NAME) \
		--file Dockerfile \
		--platform linux/amd64,linux/arm64 \
		--builder alpha1 \
		--push \
		.
	@echo "Docker image '$(IMAGE_NAME)' built and pushed."

.PHONY: deploy
deploy:
	@echo "Importing image '$(IMAGE_NAME)' into cluster..."
	k3d image import $(IMAGE_NAME) -c $(CLUSTER_NAME)
	@echo "Applying ZeroFS Configuration..."
	kubectl apply -f validation/zerofs-config.yaml
	@echo "Deploying ZeroFS Driver..."
	kubectl apply -f validation/zerofs-driver.yaml
	@echo "Waiting for Driver to roll out..."
	kubectl rollout status deployment/zerofs-csi-controller -n zerofs --timeout=120s
	kubectl rollout status daemonset/zerofs-csi-node -n zerofs --timeout=120s

.PHONY: test
test:
	@echo "Running FIO Benchmark..."
	kubectl apply -f validation/fio.yaml

.PHONY: logs
logs:
	@echo "Collecting logs to validation-logs.txt..."
	@echo "=== ZeroFS CSI Controller Logs ===" > validation-logs.txt
	-kubectl logs -n zerofs -l app=zerofs-csi-controller --all-containers --tail=-1 >> validation-logs.txt 2>&1
	@echo "\n=== ZeroFS CSI Node Logs ===" >> validation-logs.txt
	-kubectl logs -n zerofs -l app=zerofs-csi-node --all-containers --tail=-1 >> validation-logs.txt 2>&1
	@echo "\n=== FIO Benchmark Logs/Describe ===" >> validation-logs.txt
	-kubectl describe pod fio-benchmark >> validation-logs.txt 2>&1
	-kubectl logs fio-benchmark >> validation-logs.txt 2>&1
	@echo "Logs exported to validation-logs.txt"

.PHONY: teardown
teardown:
	@echo "Deleting k3d cluster '$(CLUSTER_NAME)'..."
	k3d cluster delete $(CLUSTER_NAME)

.PHONY: run-validation
run-validation: setup-infra build deploy test
	@echo "Validation started. To verify results, run 'make logs' or check pods manually."
	@echo "NBD support is required on the host for FIO tests to succeed."
