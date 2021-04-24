all: build

build:
	GO111MODULE=on go build -o bin/kubectl-import-volume ./cmd/main.go

.PHONY: all build
