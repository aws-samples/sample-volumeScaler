# =============================================================================
# Makefile for VolumeScaler
# =============================================================================

# ---------------------------------------------------------------------------
# 1) Core Project Variables
# ---------------------------------------------------------------------------

# The default semantic version for your release
VERSION ?= v0.1.0

# Go package import path; update to match your actual module
PKG_PATH = github.com/aws-samples/sample-volumeScaler

# We embed commit SHA and build date into the binary
GIT_COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# IMPORTANT: main.go is in cmd/ now
MAIN_GO ?= ./cmd/main.go

# The name of the output binary
BINARY_NAME ?= volumescaler

# LDFLAGS to embed version metadata
LDFLAGS = "-X ${PKG_PATH}/cmd.commit=${GIT_COMMIT} \
           -X ${PKG_PATH}/cmd.buildDate=${BUILD_DATE} \
           -X ${PKG_PATH}/cmd.version=${VERSION} -s -w"


# ---------------------------------------------------------------------------
# 2) Multi-Arch Docker Build Variables
# ---------------------------------------------------------------------------

# Registry to push to; for example, your public ECR or Docker Hub
REGISTRY ?= public.ecr.aws/ghanem
IMAGE_NAME ?= volumescaler
TAG ?= $(VERSION)

# If you only want to build for your local OS/Arch, you can skip these
ALL_OS ?= linux
ALL_ARCH_linux ?= amd64 arm64
ALL_OSVERSION_linux ?= alpine  # or "alpine3.17", etc.

# Combine them for multi-arch
ALL_OS_ARCH_OSVERSION_linux = $(foreach arch,$(ALL_ARCH_linux),$(foreach osver,$(ALL_OSVERSION_linux),linux-$(arch)-$(osver)))
ALL_OS_ARCH_OSVERSION = $(ALL_OS_ARCH_OSVERSION_linux)

# A helper function to split "linux-amd64-alpine" into $(OS), $(ARCH), $(OSVERSION)
word-hyphen = $(word $2,$(subst -, ,$1))


# ---------------------------------------------------------------------------
# 3) Default Target
# ---------------------------------------------------------------------------
.PHONY: default
default: build


# ---------------------------------------------------------------------------
# 4) Build, Clean, Test, Coverage
# ---------------------------------------------------------------------------

# Build the operator for your host environment (Linux/amd64 if you run Linux on x86_64).
.PHONY: build
build:
	@echo "Building VolumeScaler binary (local OS/arch)..."
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags $(LDFLAGS) \
		-o bin/$(BINARY_NAME) \
		$(MAIN_GO)

# Remove local build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/ coverage.out coverage.html

# Basic unit tests
.PHONY: test
test:
	@echo "Running unit tests..."
	go test -v ./...

# Coverage with an HTML report
.PHONY: coverage
coverage:
	@echo "Generating test coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage HTML generated: coverage.html"


# ---------------------------------------------------------------------------
# 5) Docker Targets (Single-Arch & Multi-Arch)
# ---------------------------------------------------------------------------

# Build one Docker image for the local OS/Arch (whatever your Docker daemon is on).
.PHONY: image
image:
	@echo "Building Docker image: $(REGISTRY)/$(IMAGE_NAME):$(TAG) (local OS/arch) ..."
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(REGISTRY)/$(IMAGE_NAME):$(TAG) \
		-f Dockerfile \
		.

# Build multi-arch images for all OS/ARCH combos individually
.PHONY: all-image
all-image: $(addprefix sub-image-,$(ALL_OS_ARCH_OSVERSION))

sub-image-%:
	$(MAKE) OS=$(call word-hyphen,$*,1) ARCH=$(call word-hyphen,$*,2) OSVERSION=$(call word-hyphen,$*,3) buildx-image

# The actual build for each OS/ARCH/OSVERSION using --push
.PHONY: buildx-image
buildx-image:
	@echo "Building Docker image for platform=$(OS)/$(ARCH), OSVERSION=$(OSVERSION), tag=$(TAG)..."
	docker buildx build \
		--platform=$(OS)/$(ARCH) \
		--progress=plain \
		-t $(REGISTRY)/$(IMAGE_NAME):$(TAG)-$(OS)-$(ARCH)-$(OSVERSION) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-f Dockerfile \
		--push \
		.

# New target: Build and push a unified multi-arch image in one command.
.PHONY: multiarch-image
multiarch-image:
	@echo "Building and pushing multi-arch image for platforms linux/amd64,linux/arm64 with OSVERSION alpine..."
	docker buildx build \
		--platform=linux/amd64,linux/arm64 \
		--progress=plain \
		-t $(REGISTRY)/$(IMAGE_NAME):$(TAG) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-f Dockerfile \
		--push \
		.

# Create & push a Docker manifest that references each OS/ARCH tag under a single $(TAG)
.PHONY: create-manifest
create-manifest: all-image
	docker manifest create --amend \
		$(REGISTRY)/$(IMAGE_NAME):$(TAG) \
		$(foreach combo,$(ALL_OS_ARCH_OSVERSION),$(REGISTRY)/$(IMAGE_NAME):$(TAG)-$(combo))

.PHONY: push-manifest
push-manifest: create-manifest
	@echo "Pushing multi-arch manifest for $(REGISTRY)/$(IMAGE_NAME):$(TAG)..."
	docker manifest push --purge $(REGISTRY)/$(IMAGE_NAME):$(TAG)


# ---------------------------------------------------------------------------
# 6) Additional/Optional Targets
# ---------------------------------------------------------------------------

# Example: Lint with golangci-lint (if you use it)
.PHONY: lint
lint:
	@if ! command -v golangci-lint >/dev/null; then \
	  echo "Please install golangci-lint or run 'make tools' if you have a target for that." ; \
	  exit 1 ; \
	fi
	golangci-lint run --timeout=5m ./...

# Updated Release target that builds & pushes a multi-arch image in one go.
.PHONY: release
release: multiarch-image
	@echo "Release complete for version: $(VERSION)"