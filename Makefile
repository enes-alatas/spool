GO ?= go
export PATH := /usr/local/go/bin:$(PATH)

.PHONY: build dev test itest lint secret-scan workflow-lint fakeclaude egress vet e2e-context e2e-m1 ui ui-dev image image-multiarch clean

build: ui
	$(GO) build -o bin/spool ./cmd/spool

# backend-only build (uses whatever is in web/dist, placeholder included)
server:
	$(GO) build -o bin/spool ./cmd/spool

fakeclaude:
	$(GO) build -o bin/fakeclaude ./cmd/fakeclaude

# The egress proxy (ADR-0028), built static and arch-suffixed: its image is
# FROM scratch and picks the binary by TARGETARCH, so one Dockerfile serves
# both the local build and the multi-arch one.
egress:
	CGO_ENABLED=0 GOOS=linux GOARCH=$$($(GO) env GOARCH) \
	    $(GO) build -o bin/spool-egress-$$($(GO) env GOARCH) ./cmd/spool-egress

# tier 2 (docs/QUALITY.md): real binary + fakeclaude over HTTP. The docker
# workstation suites run against a real daemon and the fakeclaude image;
# without a reachable daemon they self-skip with a notice.
itest: server fakeclaude egress
	@if docker version >/dev/null 2>&1; then \
		docker build -q -t spool-workstation-itest -f itest/testdata/workstation/Dockerfile bin >/dev/null; \
		docker build -q -t spool-egress-itest -f docker/egress/Dockerfile bin >/dev/null; \
	else echo "docker daemon unreachable — docker workstation itests will skip"; fi
	$(GO) test -tags integration -count=1 -timeout 600s ./itest/... ./internal/runtime/docker/

# Build the workstation image. `image` builds for the host architecture and loads
# it into the local Docker daemon, so it can be run directly. `image-multiarch`
# builds amd64 and arm64 together for pushing to a registry (one-time setup:
# `docker buildx create --use`); a multi-arch build can't be loaded into the local
# daemon, so it's for publishing rather than local use.
image: egress
	docker build -t spool-workstation -f docker/workstation/Dockerfile .
	docker build -t spool-egress -f docker/egress/Dockerfile bin

image-multiarch:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o bin/spool-egress-amd64 ./cmd/spool-egress
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -o bin/spool-egress-arm64 ./cmd/spool-egress
	docker buildx build --platform linux/amd64,linux/arm64 \
	    -t spool-workstation -f docker/workstation/Dockerfile .
	docker buildx build --platform linux/amd64,linux/arm64 \
	    -t spool-egress -f docker/egress/Dockerfile bin

# gofmt (fails on diff) + vet + golangci-lint when installed (CI pins it)
lint:
	@fmtout=$$(gofmt -l cmd internal itest); if [ -n "$$fmtout" ]; then echo "gofmt needed:"; echo "$$fmtout"; exit 1; fi
	$(GO) vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "golangci-lint not installed — skipped (CI runs it)"; fi

ui:
	cd web && npm install --silent && npm run build

ui-dev:
	cd web && npm run dev

dev: server
	./bin/spool --listen 127.0.0.1:8080 --data-dir ./.data

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# What CI runs on every PR, against the same base. Run it before you push if
# a change went anywhere near a credential. Both guards' tests run first:
# they share a pattern list, so either one can break the other.
secret-scan:
	@bash scripts/secret-scan-test.sh >/dev/null
	@bash scripts/secret-redact-test.sh >/dev/null
	@bash scripts/secret-redact-body-test.sh >/dev/null
	bash scripts/secret-scan.sh

# The rules CI's `workflows` job applies, runnable before you push. Cheap
# enough to run on any change to .github/workflows or scripts.
workflow-lint:
	bash scripts/workflow-lint-test.sh
	bash scripts/ci-health-test.sh

e2e-m1: server
	bash scripts/e2e/m1.sh

e2e-context: server
	bash scripts/e2e/context.sh

e2e-m2: server
	bash scripts/e2e/m2.sh

e2e-m4: server
	bash scripts/e2e/m4.sh

e2e-m5: server
	bash scripts/e2e/m5.sh

e2e-m6: server
	bash scripts/e2e/m6.sh

clean:
	rm -rf bin web/dist/assets web/dist/index.html
