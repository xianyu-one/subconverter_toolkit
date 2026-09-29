GO ?= go
DIST ?= dist
IMAGE ?= geox-sync:local
IMAGE_TAGS ?= $(IMAGE)
PLATFORMS ?= linux/amd64,linux/arm64
GOPROXY ?= https://proxy.golang.org

.PHONY: build test vet docker docker-multi docker-push

build:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o $(DIST)/geox-sync .

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

docker:
	docker build --build-arg GOPROXY=$(GOPROXY) -t $(IMAGE) .

docker-multi:
	docker buildx build --build-arg GOPROXY=$(GOPROXY) --platform $(PLATFORMS) --output type=cacheonly $(foreach tag,$(IMAGE_TAGS),--tag $(tag)) .

docker-push:
	docker buildx build --build-arg GOPROXY=$(GOPROXY) --platform $(PLATFORMS) --push $(foreach tag,$(IMAGE_TAGS),--tag $(tag)) .
