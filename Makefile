.PHONY: build bundle desktop desktop-package test vet

MODEL_DIR ?= .models
RUNTIME_DIR ?= .runtime/onnxruntime-linux-x64-1.22.0
BUNDLE_OUT ?= dist/pdfsearch-linux-amd64

build:
	go build -buildvcs=false -o bin/pdfsearch ./cmd/pdfsearch

bundle:
	go run -buildvcs=false ./cmd/bundlebuild --model-dir "$(MODEL_DIR)" --runtime-dir "$(RUNTIME_DIR)" --out "$(BUNDLE_OUT)"

desktop: bundle
	cd desktop && npm run build

desktop-package: bundle
	cd desktop && npm run package:linux

test:
	go test ./...

vet:
	go vet ./...
