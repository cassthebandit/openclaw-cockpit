GOFUMPT ?= gofumpt
GOLANGCI_LINT ?= golangci-lint

.PHONY: fmt fmt-check lint test race cross-build check
fmt:
	$(GOFUMPT) -w .
fmt-check:
	@files="$$($(GOFUMPT) -l .)" || exit $$?; \
		if [ -n "$$files" ]; then printf '%s\n' "$$files"; exit 1; fi
lint:
	$(GOLANGCI_LINT) run --timeout=5m --max-issues-per-linter=0 --max-same-issues=0
test:
	@command -v tmux >/dev/null
	go test ./...
	python3 -m unittest discover -s scripts -p 'test_*.py'
race:
	go test -race ./...
cross-build:
	@for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		GOOS=$${target%/*} GOARCH=$${target#*/} CGO_ENABLED=0 go build -o /dev/null ./cmd/openclaw-cockpit || exit; \
	done
check: fmt-check lint test race cross-build
