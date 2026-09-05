# Preparing and publishing PurePy 0.1

Release preparation captures an explicit source inventory, compiles that
snapshot, and emits normalized source and native-binary archives. The binary
archive includes the spec, plan, schemas, conformance maps, reference service,
validation reports and dependency licenses. The complete source archive contains
the executable conformance corpus and the tools needed to reproduce validation.
No packaging command executes or imports analyzed application code.

## Local preparation

Use Go 1.24 or later, a native C compiler, Python 3.12 or later for packaging,
and uv. The supported native artifact matrix is Linux amd64 and macOS arm64.
From a reviewed Git checkout, use a fresh output directory:

```sh
python tools/package_binary.py --output dist/candidate
SOURCE_DATE_EPOCH=315532800 uv build python --out-dir dist/candidate
python tools/package_binary.py --finalize --output dist/candidate --require-python
python tools/package_binary.py --verify --output dist/candidate --require-python
```

The archive source list uses Git's tracked files, including their current working
bytes. For a development snapshot, add each reviewed untracked implementation
file explicitly with `--include-source relative/path`. Missing required release
files fail preparation. New tests/docs outside the required set also need explicit
inclusion until tracked. Caches, build products, hidden local configuration,
private-key filenames and symlinks are rejected if selected. Untracked files are
never swept into the archive automatically. This is an inclusion safeguard,
not a content-level secret scanner; review the tracked inventory before release.

`--binary bin/purepy` additionally checks that an existing local binary is
identical to the new snapshot build. The normal path always builds fresh from
captured bytes, preventing a same-version stale binary from being packaged with
newer sources. Sources copied into the temporary build tree are exactly those
in the archive, even if other tasks edit the working tree during compilation.

Each tar.gz has a single versioned root, sorted regular files, fixed permissions,
zero ownership and timestamps, no gzip filename and a `RELEASE-MANIFEST.json`
with member hashes, versions and source fingerprint. The binary inventory also
records Go toolchain/build settings and linked module versions/sums. `SHA256SUMS`
covers archives and `release-index.json`. Verification reads archives without
extracting them, rejects duplicate/path-traversal/link members, checks internal
inventories and rejects stale or mismatched artifacts. It is read-only.

Verifier version, specification, language and syntax identifiers derive from
`internal/app/check.go`; the Python distribution name/version derive from
`python/pyproject.toml` and must agree with `python/uv.lock`. They intentionally
need not have the same version. Wheel and sdist metadata must match those
identifiers; the import remains `purepy` regardless of distribution name.
The chosen Python distribution is `purepy-lang`; its wheel and source archive
filenames use the normalized prefix `purepy_lang`.

## Reproduction and validation

Finalization validates the Python source distribution and normalizes its tar and
gzip metadata without changing file contents. Setuptools's sdist writer does not
fully honor `SOURCE_DATE_EPOCH`; this step removes build-clock and local-owner
differences. The wheel builder honors the fixed epoch. uv's local `.gitignore`
output marker is ignored only when it contains its exact expected `*` bytes; it
is not included in release checksums or uploaded artifacts.

Run the package unit tests with:

```sh
python -m unittest discover -s tools/tests -p 'test_package_binary.py' -v
```

Build into two fresh directories with the same source bytes, Go/C toolchain,
OS/architecture, build settings, Python build backend and `SOURCE_DATE_EPOCH`;
compare `SHA256SUMS`. The packaging workflow does this on both native targets.
Python metadata pins the build backend, and uv uses the project lock for runtime
tests. Identical bytes across unrelated compilers or operating systems are not
promised. Binary archives are platform-specific; source archives have no
platform-specific metadata and match across the native jobs.

After verifying an archive, extract it with a safe tar implementation. An
extracted source tree retains its inventory and supports the same packaging
command without Git. Modified extracted files fail the inventory check; make a
reviewed Git checkout to create a different release. Rebuild with the recorded
toolchain/settings and compare the binary hash. Run the source tree's documented
unit, conformance, differential, service, fuzz and performance gates before
promoting a release candidate. Historical soak reports remain labeled historical;
current finite validation results must be recorded against the promoted snapshot.

## Publication

`.github/workflows/package.yml` prepares, verifies and uploads workflow artifacts
without publishing a package registry release. `.github/workflows/publish.yml`
publishes already verified artifacts to a GitHub release and the Python wheel
and sdist through PyPI Trusted Publishing. Its inputs identify the immutable
release tag; the Python distribution must belong to the maintainer and configure
this repository/workflow/environment as a trusted publisher before the job runs.
The workflow does not create PyPI credentials or assume ownership of a name.

Configure the `purepy-lang` PyPI project, or its pending publisher before the
first upload, with these exact Trusted Publishing settings:

| Setting | Value |
| --- | --- |
| PyPI project name | `purepy-lang` |
| GitHub owner | `dwrtz` |
| GitHub repository | `purepy` |
| Workflow filename | `publish.yml` |
| Environment | `pypi` |

After PyPI publication, install the small Python runtime with:

```sh
python -m pip install purepy-lang
```

The Python import remains `from purepy import value`. The native Go verifier is
distributed through [GitHub releases](https://github.com/dwrtz/purepy/releases).
PyPI hosts only the Python support runtime; configuring a pending publisher does
not itself upload a release.

Maintain release identifiers intentionally. A development verifier archive can
accompany a separately versioned Python runtime, but a final verifier tag must
match the verifier's source identifier. Review the generated notes and checksum
index before publication. Preserve dependency license texts; no project license
grant is invented by the packaging tooling. A project license can be selected and
added by the owner independently of artifact preparation.
