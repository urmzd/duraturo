.PHONY: all init build test test-integration lint fmt check demo

# Every go.mod in the tree is a module: root (zero third-party deps),
# adapters (pgx / go-redis), examples (the only code importing adapters).
MODULES := $(shell find . -name go.mod -not -path './.git/*' -exec dirname {} \;)

all: check

init:
	git config core.hooksPath .githooks
	@for m in $(MODULES); do (cd $$m && go mod download && go mod tidy); done

build:
	@for m in $(MODULES); do echo "build $$m" && (cd $$m && go build ./...); done

test:
	@for m in $(MODULES); do echo "test $$m" && (cd $$m && go test ./...); done

test-integration:
	docker compose up -d --wait
	@for m in adapters/postgres adapters/redis examples/chaos; do \
		[ -d $$m ] && echo "integration $$m" && (cd $$m && go test -tags=integration ./...) || true; done

lint:
	@for m in $(MODULES); do echo "lint $$m" && (cd $$m && golangci-lint run); done

fmt:
	gofmt -w .

check: fmt lint test

demo:
	docker compose up -d --wait
	cd examples/quickstart && go run .
