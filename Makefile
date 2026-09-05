.DEFAULT_GOAL := build
UV ?= uv
PYTHON_VERSION ?= 3.14
VENV := $(CURDIR)/.venv
PYTHON := $(VENV)/bin/python
FUZZ_TIME ?= 5s
FUZZ_PARALLEL ?= 2
FUZZ_TIMEOUT ?= 2m
FUZZ_MINIMIZE_TIME ?= 5s
FUZZ_MEMORY ?= 512MiB
FUZZ_FLAGS = -run '^$$' -fuzztime "$(FUZZ_TIME)" -parallel "$(FUZZ_PARALLEL)" -timeout "$(FUZZ_TIMEOUT)" -fuzzminimizetime "$(FUZZ_MINIMIZE_TIME)"

.PHONY: setup build test race fuzz-test python-test service-test syntax-test differential-test unicode-test unicode-generate coverage-test example serve loadtest benchmark package clean
setup:
	UV_PROJECT_ENVIRONMENT="$(VENV)" $(UV) sync --locked --project python --python $(PYTHON_VERSION)
build:
	go build -trimpath -buildvcs=false -o bin/purepy ./cmd/purepy
test:
	go test ./...
race:
	go test -race ./...
fuzz-test:
	GOMEMLIMIT=$(FUZZ_MEMORY) go test ./internal/check $(FUZZ_FLAGS) -fuzz '^FuzzCheckerSemantics$$'
	GOMEMLIMIT=$(FUZZ_MEMORY) go test ./internal/check $(FUZZ_FLAGS) -fuzz '^FuzzCheckerSource$$'
	GOMEMLIMIT=$(FUZZ_MEMORY) go test ./internal/cache $(FUZZ_FLAGS) -fuzz '^FuzzCacheSummary$$'
	GOMEMLIMIT=$(FUZZ_MEMORY) go test ./internal/cache $(FUZZ_FLAGS) -fuzz '^FuzzCacheArtifact$$'
	GOMEMLIMIT=$(FUZZ_MEMORY) go test ./internal/app $(FUZZ_FLAGS) -fuzz '^FuzzCacheFallback$$'
	GOMEMLIMIT=$(FUZZ_MEMORY) go test ./internal/frontend $(FUZZ_FLAGS) -fuzz '^FuzzParseNeverPanics$$'
	GOMEMLIMIT=$(FUZZ_MEMORY) go test ./internal/manifest $(FUZZ_FLAGS) -fuzz '^FuzzTypeSyntax$$'
python-test: setup
	$(PYTHON) -m unittest discover -s python/tests -v
service-test: setup
	cd examples/reference_service && PYTHONPATH=src $(PYTHON) -m unittest discover -s tests -v
syntax-test: setup
	$(PYTHON) tools/differential_syntax.py
unicode-test: setup
	$(PYTHON) tools/generate_unicode_names.py --check --verify-cpython
	go test ./internal/unicodenames
unicode-generate: setup
	$(PYTHON) tools/generate_unicode_names.py
differential-test: setup
	go build -trimpath -buildvcs=false -o bin/purepy-semantic-probe ./tools/semantic_probe
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_differential_semantics.py' -v
	$(PYTHON) tools/differential_semantics.py
coverage-test: setup
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_spec_coverage.py' -v
	$(PYTHON) tools/spec_coverage.py --check
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
