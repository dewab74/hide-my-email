VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install man test lint vuln hooks check clean

build: ## build ./hme
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o hme ./cmd/hme

install: ## install hme into GOBIN
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/hme

man: ## generate the manual pages into ./man
	go run ./cmd/hme man man

test: ## run the tests with the race detector
	go test -race ./...

lint: ## run golangci-lint
	golangci-lint run ./...

vuln: ## check dependencies for known vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...

hooks: ## install the git hooks (once per clone)
	pre-commit install --install-hooks

check: ## everything the hooks and CI run
	pre-commit run --all-files
	$(MAKE) test vuln

clean:
	rm -rf hme hme.exe dist man
