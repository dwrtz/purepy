.DEFAULT_GOAL := build
AGENTS_HOME ?= $(HOME)/.agents
BINDIR ?= $(HOME)/.local/bin
PUREPY_SKILL := $(AGENTS_HOME)/skills/purepy
UV ?= uv
PYTHON_VERSION ?= 3.14
VENV := $(CURDIR)/.venv
PYTHON := $(VENV)/bin/python
FUZZ_TIME ?= 5s
FUZZ_PARALLEL ?= 2
FUZZ_TIMEOUT ?= 2m
FUZZ_MINIMIZE_TIME ?= 5s
FUZZ_MEMORY ?= 512MiB
BENCHMARK_ARGS ?=
BENCHMARK_BASELINE ?= benchmarks/baseline.json
BENCHMARK_CANDIDATE ?= build/benchmark-candidate.json
BENCHMARK_COMPARE_ARGS ?=
ROBUSTNESS_ARGS ?=
LOADTEST_ARGS ?=
ACCEPTANCE_PROFILE ?= benchmarks/acceptance-m4.json
ACCEPTANCE_BENCHMARK ?= docs/validation/2026-09-05/completion-benchmark.json
ACCEPTANCE_SERVICE ?= docs/validation/2026-09-05/completion-service.json
RELEASE_OUTPUT ?= dist/candidate
FUZZ_FLAGS = -run '^$$' -fuzztime "$(FUZZ_TIME)" -parallel "$(FUZZ_PARALLEL)" -timeout "$(FUZZ_TIMEOUT)" -fuzzminimizetime "$(FUZZ_MINIMIZE_TIME)"

.PHONY: install uninstall setup build test race fuzz-test robustness-test robustness-campaign python-test service-test syntax-test differential-test unicode-test unicode-generate coverage-test schema-test example serve loadtest benchmark benchmark-test benchmark-compare acceptance-test acceptance-check package-test package clean
install: build
	install -d "$(BINDIR)" "$(PUREPY_SKILL)"
	install -m 755 bin/purepy "$(BINDIR)/purepy"
	install -m 644 skills/purepy/SKILL.md "$(PUREPY_SKILL)/SKILL.md"
uninstall:
	rm -f "$(BINDIR)/purepy" "$(PUREPY_SKILL)/SKILL.md"
	@if test -d "$(PUREPY_SKILL)"; then rmdir "$(PUREPY_SKILL)" 2>/dev/null || test -d "$(PUREPY_SKILL)"; fi
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
	GOMEMLIMIT=$(FUZZ_MEMORY) go test ./internal/manifest $(FUZZ_FLAGS) -fuzz '^FuzzManifestLoad$$'
robustness-test: setup
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_robustness_campaign.py' -v
robustness-campaign: setup
	$(PYTHON) tools/robustness_campaign.py $(ROBUSTNESS_ARGS)
python-test: setup
	$(PYTHON) -m unittest discover -s python/tests -v
service-test: setup
	cd examples/reference_service && PYTHONPATH=src $(PYTHON) -m unittest discover -s tests -v
syntax-test: setup
	$(PYTHON) tools/differential_syntax.py
unicode-test: setup
	$(PYTHON) tools/generate_unicode_names.py --check --verify-cpython
	$(PYTHON) tools/generate_unicode_identifiers.py --check --verify-cpython
	go test ./internal/unicodenames ./internal/unicodeident
unicode-generate: setup
	$(PYTHON) tools/generate_unicode_names.py
	$(PYTHON) tools/generate_unicode_identifiers.py
differential-test: setup build
	go build -trimpath -buildvcs=false -o bin/purepy-semantic-probe ./tools/semantic_probe
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_differential*.py' -v
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_function*.py' -v
	$(PYTHON) tools/differential_semantics.py
	$(PYTHON) tools/differential_functions.py
	$(PYTHON) tools/differential_modules.py --verifier "$(CURDIR)/bin/purepy"
coverage-test: setup
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_spec_coverage.py' -v
	$(PYTHON) tools/spec_coverage.py --check
schema-test: build
	$(UV) run --no-project --with jsonschema==4.23.0 --python "$(PYTHON_VERSION)" python tools/check_schema_contract.py --verifier "$(CURDIR)/bin/purepy"
example:
	go run ./cmd/purepy check examples/reference_service
serve: setup
	cd examples/reference_service && PYTHONPATH=src $(PYTHON) -m host.main
loadtest: setup build
	cd examples/reference_service && PYTHONPATH=src $(PYTHON) -m loadtest.load --verifier "$(CURDIR)/bin/purepy" $(LOADTEST_ARGS)
benchmark: setup build
	$(PYTHON) tools/benchmark.py --verifier "$(CURDIR)/bin/purepy" $(BENCHMARK_ARGS)
benchmark-test: setup
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_benchmark*.py' -v
benchmark-compare: setup
	$(PYTHON) tools/benchmark_compare.py --baseline "$(BENCHMARK_BASELINE)" --candidate "$(BENCHMARK_CANDIDATE)" $(BENCHMARK_COMPARE_ARGS)
acceptance-test: setup
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_performance_acceptance.py' -v
acceptance-check: setup
	$(PYTHON) tools/performance_acceptance.py --profile "$(ACCEPTANCE_PROFILE)" --benchmark "$(ACCEPTANCE_BENCHMARK)" --service "$(ACCEPTANCE_SERVICE)"
package-test: setup
	$(PYTHON) -m unittest discover -s tools/tests -p 'test_package_binary.py' -v
package: setup
	$(PYTHON) tools/package_binary.py --output "$(RELEASE_OUTPUT)"
	SOURCE_DATE_EPOCH=315532800 $(UV) build python --out-dir "$(RELEASE_OUTPUT)"
	$(PYTHON) tools/package_binary.py --finalize --output "$(RELEASE_OUTPUT)" --require-python
	$(PYTHON) tools/package_binary.py --verify --output "$(RELEASE_OUTPUT)" --require-python
clean:
	go clean ./...
