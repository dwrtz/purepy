# Conformance cases

`cases.json` is a version-controlled integration corpus. Each entry has a unique
`name`, `valid` verdict, required diagnostic `codes`, and the contents of `app.py`
in `source`. Optional `files` adds verified modules relative to `src`; `manifest`
adds one explicit external manifest. The harness creates strict `purepy.toml`
configuration and runs the actual complete pipeline in an isolated directory.

Run `go test ./internal/app -run TestConformance` from the repository root.
The parser and security suites supply additional unsupported syntax and authority
probes. Valid fixtures should continue to pass and published diagnostic codes must
not be reassigned to different rule families.
