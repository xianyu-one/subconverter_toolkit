GO ?= go
DIST ?= dist
IMAGE ?= mihomo-observer:local
IMAGE_TAGS ?= $(IMAGE)
PLATFORMS ?= linux/amd64,linux/arm64
GOFLAGS_BUILD = -trimpath -ldflags=-s\ -w
PACKAGE = ./cmd/mihomo-observer

.PHONY: build test vet docker docker-multi docker-push cross \
	linux-amd64 linux-arm64 linux-armv7 windows-amd64 windows-arm64

build:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build $(GOFLAGS_BUILD) -o $(DIST)/mihomo-observer $(PACKAGE)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

docker:
	docker build -t $(IMAGE) .

docker-multi:
	docker buildx build --platform $(PLATFORMS) --output type=cacheonly $(foreach tag,$(IMAGE_TAGS),--tag $(tag)) .

docker-push:
	docker buildx build --platform $(PLATFORMS) --push $(foreach tag,$(IMAGE_TAGS),--tag $(tag)) .

cross: linux-amd64 linux-arm64 linux-armv7 windows-amd64 windows-arm64

linux-amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS_BUILD) -o $(DIST)/mihomo-observer-linux-amd64 $(PACKAGE)

linux-arm64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS_BUILD) -o $(DIST)/mihomo-observer-linux-arm64 $(PACKAGE)

linux-armv7:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GO) build $(GOFLAGS_BUILD) -o $(DIST)/mihomo-observer-linux-armv7 $(PACKAGE)

windows-amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS_BUILD) -o $(DIST)/mihomo-observer-windows-amd64.exe $(PACKAGE)

windows-arm64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build $(GOFLAGS_BUILD) -o $(DIST)/mihomo-observer-windows-arm64.exe $(PACKAGE)
