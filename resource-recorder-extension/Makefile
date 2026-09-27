PACKAGE := resource-recorder-extension
DIST := dist
BUILD_DIR := $(DIST)/$(PACKAGE)
ZIP_FILE := $(DIST)/$(PACKAGE).zip

.DEFAULT_GOAL := build
.PHONY: build package check test clean

build: check
	rm -rf -- "$(BUILD_DIR)"
	mkdir -p "$(BUILD_DIR)/src" "$(BUILD_DIR)/styles" "$(BUILD_DIR)/icons" "$(BUILD_DIR)/fonts"
	cp manifest.json sidepanel.html "$(BUILD_DIR)/"
	cp src/*.js "$(BUILD_DIR)/src/"
	cp styles/*.css "$(BUILD_DIR)/styles/"
	cp icons/icon-*.png "$(BUILD_DIR)/icons/"
	cp fonts/NotoSansDisplay-Bold.ttf fonts/COPYRIGHT "$(BUILD_DIR)/fonts/"
	@echo "可加载目录：$(BUILD_DIR)"

package: build
	rm -f -- "$(ZIP_FILE)"
	cd "$(DIST)" && zip -q -r "$(PACKAGE).zip" "$(PACKAGE)"
	@echo "归档文件：$(ZIP_FILE)（解压后加载）"

check:
	npm run check

test:
	npm test

clean:
	rm -rf -- "$(DIST)"
