GO ?= go
BUN ?= bun
VERSION ?= development
# Release binaries default to amd64: the host helper uploads them to registered hosts,
# which must be x86_64 (internal/hosts/host_agent.go). Override for another server arch.
GOARCH ?= amd64
# Magpie keeps its documented pin (third_party/magpie/WORKBENCH.md); it does not inherit VERSION.
MAGPIE_VERSION ?= 0.1.1265
# Go test temp files: tmpfs is ~100x faster than the direnv TMPDIR on disk (51s vs 0.02s).
TMPDIR_TEST ?= /tmp

.PHONY: frontend foundry-ui foundry-test build binaries test check

frontend:
	cd web && $(BUN) run build

# Foundry's own UI (third_party/foundry/ui) builds into foundry_loop/web, which the Go
# binary embeds; the build is checked in, so rebuild it only when ui/ changes.
foundry-ui:
	cd third_party/foundry/ui && $(BUN) install --frozen-lockfile && $(BUN) run build

# Foundry's own suites: Python 3.11+ (standard library only) and its UI (translation engine and
# coverage of every UI string).
foundry-test:
	cd third_party/foundry && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests
	cd third_party/foundry/ui && $(BUN) install --frozen-lockfile && $(BUN) test tests

build: frontend
	$(MAKE) binaries

binaries:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) GOAMD64=v1 $(GO) build -mod=readonly -tags nogui -trimpath -ldflags '-X main.version=$(VERSION)' -o dist/workbench ./cmd/workbench
	cd third_party/magpie && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) GOAMD64=v1 $(GO) build -mod=readonly -tags nogui -trimpath -ldflags '-X main.version=$(MAGPIE_VERSION)' -o ../../dist/magpie .

test: frontend
	TMPDIR=$(TMPDIR_TEST) CGO_ENABLED=0 GOAMD64=v1 $(GO) test -tags nogui ./cmd/... ./internal/... ./web/...

check: test foundry-test
	cd web && $(BUN) test tests/unit
	python3 scripts/check_docker_web_output.py
	cd web && $(BUN) run typecheck
	CGO_ENABLED=0 GOAMD64=v1 $(GO) vet -tags nogui ./cmd/... ./internal/... ./web/...
