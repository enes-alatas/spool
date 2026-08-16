GO ?= go
export PATH := /usr/local/go/bin:$(PATH)

.PHONY: build dev test vet e2e-m1 ui ui-dev clean

build: ui
	$(GO) build -o bin/spool ./cmd/spool

# backend-only build (uses whatever is in web/dist, placeholder included)
server:
	$(GO) build -o bin/spool ./cmd/spool

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
