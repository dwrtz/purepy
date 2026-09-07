# Preparing and publishing PurePy

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
python tools/package_binary.py --finalize --output dist/candidate
python tools/package_binary.py --verify --output dist/candidate
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

Verifier version and current language identifiers derive from `internal/app/check.go`.
The inventory also records the sole supported language, 0.2. A complete release
contains native and source archives. Python wheels and source distributions are
rejected: verified 0.2 programs have no PurePy runtime dependency.

## Reproduction and validation

Run the package unit tests with:

```sh
python -m unittest discover -s tools/tests -p 'test_package_binary.py' -v
```

Build into two fresh directories with the same source bytes, Go/C toolchain,
OS/architecture and build settings;
compare `SHA256SUMS`. The packaging workflow does this on both native targets.
Identical bytes across unrelated compilers or operating systems are not
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
publishes already verified native and source artifacts to a GitHub release. Its
input identifies an existing immutable release tag, which must match the verifier
version. The workflow validates the tag, checks shared source artifacts across
native builds, and rechecks the tag before publishing. There is no PyPI job.

Review notes and the checksum index before publication. Preserve dependency
license texts; the packaging tooling does not invent a project license grant.
