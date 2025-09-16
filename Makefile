VERSION := $(shell git describe --tags --always --dirty)
COMMIT_HASH := $(shell git rev-parse --short HEAD)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

.PHONY: build-macos-arm64
build-macos-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags "-X github.com/dmdmdm-nz/tunneld/pkg/version.Version=${VERSION} \
		-X github.com/dmdmdm-nz/tunneld/pkg/version.CommitHash=${COMMIT_HASH} \
		-X github.com/dmdmdm-nz/tunneld/pkg/version.BuildTime=${BUILD_TIME}" \
		-o ./tunneld-macos-arm64 cmd/tunneld/main.go

.PHONY: build-linux-x64
build-linux-x64:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X github.com/dmdmdm-nz/tunneld/pkg/version.Version=${VERSION} \
		-X github.com/dmdmdm-nz/tunneld/pkg/version.CommitHash=${COMMIT_HASH} \
		-X github.com/dmdmdm-nz/tunneld/pkg/version.BuildTime=${BUILD_TIME}" \
		-o ./tunneld-linux-x64 cmd/tunneld/main.go

.PHONY: build-linux-arm64
build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "-X github.com/dmdmdm-nz/tunneld/pkg/version.Version=${VERSION} \
		-X github.com/dmdmdm-nz/tunneld/pkg/version.CommitHash=${COMMIT_HASH} \
		-X github.com/dmdmdm-nz/tunneld/pkg/version.BuildTime=${BUILD_TIME}" \
		-o ./tunneld-linux-arm64 cmd/tunneld/main.go

.PHONY: build-all
build-all: build-macos-arm64 build-linux-x64 build-linux-arm64

.PHONY: clean
clean:
	rm -f ./tunneld-macos-arm64 ./tunneld-linux-x64 ./tunneld-linux-arm64
	go clean