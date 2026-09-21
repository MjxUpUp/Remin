BINARY := bin/remin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.3.0)

.PHONY: build test race vet constitution adapter-budget clean

build:
	go build -ldflags "-X github.com/remin-dev/remin/internal/cli.Version=$(VERSION)" -o $(BINARY) ./cmd/remin

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

# 宪法测试套件（六项）+ 依赖扫描（P1-N1）：CI 阻断合并
constitution:
	go test ./internal/core/eval/ -run 'TestConstitution' -v
	scripts/check-deps.sh
	scripts/adapter-budget.sh

# 适配器预算（P1-N2）：全部 ≤20%、单个 ≤8%
adapter-budget:
	scripts/adapter-budget.sh

clean:
	rm -rf bin
