.DEFAULT_GOAL := build
UV ?= uv
PYTHON_VERSION ?= 3.14
VENV := $(CURDIR)/.venv
PYTHON := $(VENV)/bin/python

.PHONY: setup build test race python-test service-test syntax-test example serve loadtest benchmark package clean
setup:
	UV_PROJECT_ENVIRONMENT="$(VENV)" $(UV) sync --locked --project python --python $(PYTHON_VERSION)
build:
	go build -trimpath -buildvcs=false -o bin/purepy ./cmd/purepy
test:
	go test ./...
race:
	go test -race ./...
python-test: setup
	$(PYTHON) -m unittest discover -s python/tests -v
service-test: setup
	cd examples/reference_service && PYTHONPATH=src $(PYTHON) -m unittest discover -s tests -v
syntax-test: setup
	$(PYTHON) tools/differential_syntax.py
example:
	go run ./cmd/purepy check examples/reference_service
serve: setup
	cd examples/reference_service && PYTHONPATH=src $(PYTHON) -m host.main
loadtest: setup build
	cd examples/reference_service && PYTHONPATH=src $(PYTHON) -m loadtest.load --verifier "$(CURDIR)/bin/purepy"
benchmark: setup build
	$(PYTHON) tools/benchmark.py --verifier "$(CURDIR)/bin/purepy"
package: setup build
	$(PYTHON) tools/package_binary.py
	SOURCE_DATE_EPOCH=315532800 $(UV) build python --out-dir dist
clean:
	go clean ./...
