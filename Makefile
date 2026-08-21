GO ?= go
export PATH := /usr/local/go/bin:$(PATH)

.PHONY: build dev test itest lint fakeclaude vet e2e-context e2e-m1 ui ui-dev image image-multiarch clean

build: ui
	$(GO) build -o bin/spool ./cmd/spool

# backend-only build (uses whatever is in web/dist, placeholder included)
server:
	$(GO) build -o bin/spool ./cmd/spool

fakeclaude:
	$(GO) build -o bin/fakeclaude ./cmd/fakeclaude

# tier 2 (docs/QUALITY.md): real binary + fakeclaude over HTTP. The docker
# workstation suites run against a real daemon and the fakeclaude image;
# without a reachable daemon they self-skip with a notice.
itest: server fakeclaude
	@if docker version >/dev/null 2>&1; then \
		docker build -q -t spool-workstation-itest -f itest/testdata/workstation/Dockerfile bin >/dev/null; \
	else echo "docker daemon unreachable — docker workstation itests will skip"; fi
	$(GO) test -tags integration -count=1 -timeout 600s ./itest/... ./internal/runtime/docker/

# Build the workstation image. `image` builds for the host architecture and loads
# it into the local Docker daemon, so it can be run directly. `image-multiarch`
# builds amd64 and arm64 together for pushing to a registry (one-time setup:
# `docker buildx create --use`); a multi-arch build can't be loaded into the local
# daemon, so it's for publishing rather than local use.
image:
	docker build -t spool-workstation -f docker/workstation/Dockerfile .

image-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 \
	    -t spool-workstation -f docker/workstation/Dockerfile .

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
