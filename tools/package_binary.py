"""Prepare and verify reproducible release artifacts without publishing them.

Source inventory comes from Git's index, or a verified extracted source archive.
Untracked additions require an explicit --include-source per reviewed file.
"""

import argparse
from email.parser import BytesParser
import gzip
from hashlib import sha256
import io
import json
from pathlib import Path, PurePosixPath
import re
import shlex
import subprocess
import tarfile
import tempfile
import tomllib
import zipfile

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = "RELEASE-MANIFEST.json"
INDEX = "release-index.json"
CHECKSUMS = "SHA256SUMS"
SCHEMA_LOCK = "docs/schema/frozen-v1.json"
FROZEN_SCHEMAS = {"manifests/schema/v1.json", "docs/schema/diagnostics-v1.json",
                  "docs/schema/capabilities-v1.json", "docs/schema/explain-v1.json"}
SUPPORTED_TARGETS = {"linux-amd64", "darwin-arm64"}
EXCLUDED_PARTS = {".git", ".venv", ".purepy-cache", "__pycache__", "node_modules", "dist", "build", "bin"}
REQUIRED_SOURCES = {
    "go.mod", "go.sum", "Makefile", "README.md", "internal/app/check.go",
    "python/pyproject.toml", "python/uv.lock", "python/purepy/value.py",
    "docs/PUREPY_SPEC.md", "docs/PUREPY_PLAN.md", "docs/IMPLEMENTATION.md",
    "docs/RELEASE.md", "docs/RELEASE_NOTES.md", "docs/SCHEMA_CONTRACT.md",
    "docs/LANGUAGE_GUIDE.md",
    "docs/CONFORMANCE.md", "docs/conformance/core.json", "docs/conformance/boundary.json",
    "docs/conformance/tooling.json", "docs/MANIFESTS.md", "docs/DIAGNOSTICS.md",
    "manifests/schema/v1.json", "docs/schema/diagnostics-v1.json",
    "docs/schema/capabilities-v1.json", "docs/schema/explain-v1.json", SCHEMA_LOCK,
    "examples/reference_service/README.md", "docs/validation/2026-09-05/service.json",
    "docs/validation/2026-09-05/service-run.json", "docs/validation/2026-09-05/README.md",
    "tools/package_binary.py", "tools/tests/test_package_binary.py",
    "tools/check_schema_contract.py", ".github/workflows/package.yml", ".github/workflows/publish.yml",
}


def json_bytes(value):
    return (json.dumps(value, sort_keys=True, indent=2, ensure_ascii=False) + "\n").encode()


def digest(data):
    return sha256(data).hexdigest()


def safe_name(name):
    path = PurePosixPath(name)
    if (not name or path.is_absolute() or ".." in path.parts or str(path) != name
            or "\\" in name or any(ord(char) < 32 or ord(char) == 127 for char in name)):
        raise ValueError(f"unsafe archive path: {name!r}")
    return path


def source_name(name):
    path = safe_name(name)
    if (any(part in EXCLUDED_PARTS or part.endswith(".egg-info") for part in path.parts)
            or any(part.startswith(".") for part in path.parts if part not in {".github", ".gitignore", ".python-version"})
            or path.suffix.lower() in {".pem", ".key", ".p12", ".pfx", ".pyc", ".pyo"}
            or path.name.lower() in {"credentials", "id_rsa", "id_ed25519"}):
        raise ValueError(f"excluded source path: {name}")
    return path


def read_source(root, name):
    path = source_name(name)
    current = root
    for part in path.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError(f"source symlink is forbidden: {name}")
    if not current.is_file():
        raise ValueError(f"source is not a regular file: {name}")
    return current.read_bytes()


def source_files(root, additions=()):
    if (root / MANIFEST).is_file():
        recorded = json.loads((root / MANIFEST).read_bytes())
        if recorded.get("kind") != "source":
            raise ValueError("extracted archive is not a source snapshot")
        names = [item["path"] for item in recorded["files"]]
    else:
        result = subprocess.run(["git", "ls-files", "--cached", "-z"], cwd=root,
                                check=True, capture_output=True)
        names = result.stdout.decode().rstrip("\0").split("\0")
    files = {name: read_source(root, name) for name in sorted(set(names) | set(additions))}
    if (root / MANIFEST).is_file() and file_inventory(files) != recorded["files"]:
        raise ValueError("extracted source snapshot changed; prepare a new release from a reviewed Git checkout")
    if ((root / MANIFEST).is_file()
            and digest(json_bytes(recorded["files"])) != recorded.get("source_tree_sha256")):
        raise ValueError("extracted source inventory fingerprint changed")
    missing = REQUIRED_SOURCES - files.keys()
    if missing:
        raise ValueError("release source inventory is missing: " + ", ".join(sorted(missing))
                         + "; add reviewed new files with --include-source PATH")
    return files


def versions(files):
    source = files["internal/app/check.go"].decode()
    values = {}
    for key, constant in (("verifier", "Version"), ("specification", "SpecificationVersion"),
                          ("language", "LanguageVersion"), ("python_syntax", "PythonSyntaxVersion")):
        matches = re.findall(r'^const ' + constant + r' = "([A-Za-z0-9.+-]+)"$', source, re.M)
        if len(matches) != 1:
            raise ValueError(f"cannot resolve authoritative {constant}")
        values[key] = matches[0]
    metadata = tomllib.loads(files["python/pyproject.toml"].decode())["project"]
    values["python_package"] = metadata["version"]
    values["python_distribution"] = metadata["name"]
    if any(not re.fullmatch(r"[A-Za-z0-9.+_-]+", values[key]) for key in ("python_package", "python_distribution")):
        raise ValueError("unsafe Python distribution name or version")
    lock = tomllib.loads(files["python/uv.lock"].decode())
    locked = [item["version"] for item in lock["package"] if item["name"] == values["python_distribution"]]
    if locked != [values["python_package"]]:
        raise ValueError("Python project name/version and uv.lock disagree")
    return values


def check_schema_lock(files):
    lock = json.loads(files[SCHEMA_LOCK])
    if lock.get("schema") != 1 or lock.get("manifest_schema") != 1 or lock.get("json_schema") != 1:
        raise ValueError("unsupported schema freeze contract")
    if set(lock.get("files", {})) != FROZEN_SCHEMAS:
        raise ValueError("schema freeze inventory is incomplete")
    for name, expected in lock["files"].items():
        if digest(files[name]) != expected:
            raise ValueError(f"frozen schema changed: {name}; review and version the contract explicitly")
    for name, declaration in (("internal/app/check.go", "JSONSchema"), ("internal/manifest/manifest.go", "SchemaVersion")):
        if not re.search(r"^const " + declaration + r" = 1$", files[name].decode(), re.M):
            raise ValueError(f"{declaration} implementation disagrees with frozen schema")


def file_inventory(files, executable=()):
    return [{"path": name, "size": len(data), "sha256": digest(data),
             "mode": 0o755 if name in executable else 0o644}
            for name, data in sorted(files.items())]


def write_archive(destination, files, metadata, executable=()):
    for name in files:
        safe_name(name)
    if MANIFEST in files:
        raise ValueError("reserved archive manifest name")
    manifest = {"schema": 1, **metadata, "files": file_inventory(files, executable)}
    members = {**files, MANIFEST: json_bytes(manifest)}
    prefix = destination.name.removesuffix(".tar.gz")
    safe_name(prefix)
    with destination.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0, compresslevel=9) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                for name, data in sorted(members.items()):
                    info = tarfile.TarInfo(f"{prefix}/{name}")
                    info.size = len(data)
                    info.mode = 0o755 if name in executable else 0o644
                    archive.addfile(info, io.BytesIO(data))
    verify_archive(destination)


def verify_archive(archive_path):
    files = {}
    executable = []
    prefix = archive_path.name.removesuffix(".tar.gz")
    with tarfile.open(archive_path, "r:gz") as archive:
        for member in archive:
            path = safe_name(member.name)
            if len(path.parts) < 2 or path.parts[0] != prefix:
                raise ValueError(f"unexpected archive root in {member.name}")
            name = str(PurePosixPath(*path.parts[1:]))
            if name in files or not member.isfile():
                raise ValueError(f"duplicate or non-regular archive member: {member.name}")
            if (member.uid or member.gid or member.uname or member.gname or member.mtime
                    or member.mode not in {0o644, 0o755} or member.pax_headers):
                raise ValueError(f"non-normalized archive metadata: {member.name}")
            files[name] = archive.extractfile(member).read()
            if member.mode == 0o755:
                executable.append(name)
    if MANIFEST not in files:
        raise ValueError("missing archive inventory")
    manifest = json.loads(files.pop(MANIFEST))
    if manifest.get("schema") != 1 or manifest.get("kind") not in {"binary", "source"}:
        raise ValueError("unknown archive inventory schema or kind")
    suffix = "source" if manifest["kind"] == "source" else manifest.get("target")
    if prefix != f"purepy-{manifest['versions']['verifier']}-{suffix}":
        raise ValueError("archive filename disagrees with its version or target")
    if manifest.get("files") != file_inventory(files, executable):
        raise ValueError("archive content differs from its inventory")
    if manifest["kind"] == "source":
        if digest(json_bytes(manifest["files"])) != manifest.get("source_tree_sha256"):
            raise ValueError("source snapshot fingerprint differs from inventory")
        if REQUIRED_SOURCES - files.keys():
            raise ValueError("source archive is incomplete")
        for name in files:
            source_name(name)
        check_schema_lock(files)
        if versions(files) != manifest.get("versions"):
            raise ValueError("archive version metadata differs from source")
    else:
        if manifest.get("target") not in SUPPORTED_TARGETS or executable != ["purepy"]:
            raise ValueError("binary archive target or executable permissions are invalid")
        if not {"purepy", "docs/RELEASE_NOTES.md", "docs/PUREPY_SPEC.md", "licenses/GO.txt"}.issubset(files):
            raise ValueError("binary archive is incomplete")
    return manifest


def json_stream(data):
    decoder = json.JSONDecoder()
    while data.strip():
        value, end = decoder.raw_decode(data.lstrip())
        yield value
        data = data.lstrip()[end:]


def build_snapshot(files, expected_binary=None):
    """Compile captured inputs, binding the binary to the archived source bytes."""
    with tempfile.TemporaryDirectory(prefix="purepy-release-") as directory:
        snapshot = Path(directory)
        for name, data in files.items():
            destination = snapshot / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(data)
        binary = snapshot / "purepy"
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-o", str(binary), "./cmd/purepy"],
                       cwd=snapshot, check=True)
        binary_data = binary.read_bytes()
        if expected_binary is not None and expected_binary.read_bytes() != binary_data:
            raise ValueError("provided binary differs from a fresh build of the archived source snapshot")
        info = subprocess.run(["go", "version", "-m", str(binary)], check=True, capture_output=True, text=True).stdout
        settings = dict(re.findall(r"^\s*build\s+([^=]+)=(.*)$", info, re.M))
        target = f"{settings['GOOS']}-{settings['GOARCH']}"
        if target not in SUPPORTED_TARGETS:
            raise ValueError(f"unvalidated native release target {target}")
        dependencies = subprocess.run(["go", "list", "-deps", "-json", "./cmd/purepy"], cwd=snapshot,
                                      check=True, text=True, capture_output=True).stdout
        modules = {}
        for item in json_stream(dependencies):
            module = item.get("Module", {})
            if module and not module.get("Main"):
                modules[module["Path"]] = module
        licenses = {}
        module_metadata = []
        for module_path, module in sorted(modules.items()):
            module_dir = Path(module["Dir"])
            names = sorted(path for path in module_dir.rglob("*")
                           if path.is_file() and path.name in {"LICENSE", "LICENSE.txt", "LICENSE.md", "COPYING", "NOTICE"})
            if not names:
                raise ValueError(f"missing dependency license: {module_path}")
            for path in names:
                name = f"licenses/{module_path}@{module['Version']}/{path.relative_to(module_dir).as_posix()}"
                licenses[name] = path.read_bytes()
            module_metadata.append({key.lower(): module[key] for key in ("Path", "Version", "Sum")})
        goroot = subprocess.run(["go", "env", "GOROOT"], check=True, capture_output=True, text=True).stdout.strip()
        go_license = next((candidate for candidate in (Path(goroot) / "LICENSE", Path(goroot).parent / "LICENSE")
                           if candidate.is_file()), None)
        if go_license is None:
            raise ValueError("Go toolchain license could not be located")
        licenses["licenses/GO.txt"] = go_license.read_bytes()
        licenses["licenses/UNICODE.txt"] = files["internal/unicodenames/ucd/LICENSE"]
        compiler = subprocess.run(["go", "env", "CC"], check=True, capture_output=True, text=True).stdout.strip()
        compiler_version = subprocess.run([*shlex.split(compiler), "--version"], check=True,
                                          capture_output=True, text=True).stdout.splitlines()[0]
        return binary_data, target, {"go_toolchain": info.splitlines()[0].split()[-1],
                                     "c_compiler": compiler_version, "settings": settings,
                                     "modules": module_metadata}, licenses


def python_artifact(path, version_info, source_inventory):
    version = version_info["python_package"]
    project = version_info["python_distribution"]
    normalized = re.sub(r"[-_.]+", "_", project).lower()
    prefix = f"{normalized}-{version}"
    expected_runtime = {item["path"].removeprefix("python/"): item["sha256"] for item in source_inventory
                        if item["path"].startswith("python/purepy/")}
    if path.name == f"{prefix}-py3-none-any.whl":
        with zipfile.ZipFile(path) as archive:
            names = archive.namelist()
            if len(names) != len(set(names)):
                raise ValueError("duplicate wheel member")
            for name in names:
                safe_name(name.rstrip("/"))
            expected = f"{prefix}.dist-info/METADATA"
            if not {"purepy/value.py", "purepy/py.typed", expected}.issubset(names):
                raise ValueError("Python wheel is incomplete")
            for name, expected_hash in expected_runtime.items():
                if name not in names or digest(archive.read(name)) != expected_hash:
                    raise ValueError(f"wheel code differs from source snapshot: {name}")
            if {name for name in names if name.startswith("purepy/")} != set(expected_runtime):
                raise ValueError("wheel contains unexpected runtime files")
            metadata = BytesParser().parsebytes(archive.read(expected))
        kind = "python_wheel"
    elif path.name == f"{prefix}.tar.gz":
        with tarfile.open(path, "r:gz") as archive:
            members = archive.getmembers()
            names = [item.name for item in members]
            if len(names) != len(set(names)):
                raise ValueError("duplicate Python source member")
            for member in members:
                safe_name(member.name.rstrip("/"))
                if not member.isfile() and not member.isdir():
                    raise ValueError("non-regular Python source member")
            expected = f"{prefix}/PKG-INFO"
            if not {f"{prefix}/purepy/value.py", f"{prefix}/pyproject.toml", expected}.issubset(names):
                raise ValueError("Python source package is incomplete")
            expected_runtime["pyproject.toml"] = next(item["sha256"] for item in source_inventory
                                                      if item["path"] == "python/pyproject.toml")
            for name, expected_hash in expected_runtime.items():
                member_name = f"{prefix}/{name}"
                if member_name not in names or digest(archive.extractfile(member_name).read()) != expected_hash:
                    raise ValueError(f"Python source distribution differs from source snapshot: {name}")
            metadata = BytesParser().parsebytes(archive.extractfile(expected).read())
        kind = "python_sdist"
    else:
        raise ValueError(f"unexpected release artifact: {path.name}")
    if metadata.get("Name") != project or metadata.get("Version") != version:
        raise ValueError(f"Python artifact metadata disagrees with project: {path.name}")
    return kind


def normalize_python_sdist(path):
    """Normalize setuptools sdist metadata after validating source identity."""
    with tarfile.open(path, "r:gz") as archive:
        files = {item.name: archive.extractfile(item).read() for item in archive if item.isfile()}
    output = io.BytesIO()
    with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0, compresslevel=9) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
            for name, data in sorted(files.items()):
                member = tarfile.TarInfo(name)
                member.mode = 0o644
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
    path.write_bytes(output.getvalue())


def finalize(output, require_python=False):
    artifacts, manifests, python_paths = [], [], []
    for path in sorted(output.iterdir()):
        if path.name in {INDEX, CHECKSUMS}:
            continue
        if not path.is_file() or path.is_symlink():
            raise ValueError(f"unexpected output entry: {path.name}")
        # uv build writes this fixed local marker into its output directory.
        # It is not an artifact and must not enter the published inventory.
        if path.name == ".gitignore" and path.read_bytes() == b"*":
            continue
        if re.fullmatch(r"purepy-.+-(?:source|linux-amd64|darwin-arm64)\.tar\.gz", path.name):
            manifest = verify_archive(path)
            manifests.append(manifest)
            artifacts.append({"file": path.name, "kind": manifest["kind"], "sha256": digest(path.read_bytes()),
                              "size": path.stat().st_size, "target": manifest.get("target")})
        else:
            python_paths.append(path)
    if {item["kind"] for item in manifests} != {"binary", "source"}:
        raise ValueError("release requires a binary archive and source archive")
    sources = [item for item in manifests if item["kind"] == "source"]
    targets = [item["target"] for item in manifests if item["kind"] == "binary"]
    if len(sources) != 1 or len(targets) != len(set(targets)):
        raise ValueError("release contains duplicate source snapshots or binary targets")
    identity = {(json.dumps(item["versions"], sort_keys=True), item["source_tree_sha256"]) for item in manifests}
    if len(identity) != 1:
        raise ValueError("release archives disagree on source snapshot or versions")
    version_info = manifests[0]["versions"]
    for path in python_paths:
        kind = python_artifact(path, version_info, sources[0]["files"])
        if kind == "python_sdist":
            normalize_python_sdist(path)
        artifacts.append({"file": path.name, "kind": kind,
                          "sha256": digest(path.read_bytes()), "size": path.stat().st_size})
    kinds = [item["kind"] for item in artifacts]
    if require_python and (kinds.count("python_wheel") != 1 or kinds.count("python_sdist") != 1):
        raise ValueError("complete release requires exactly one Python wheel and source distribution")
    artifacts.sort(key=lambda item: item["file"])
    index = {"schema": 1, "versions": version_info, "source_tree_sha256": manifests[0]["source_tree_sha256"],
             "complete": "python_wheel" in kinds and "python_sdist" in kinds, "artifacts": artifacts}
    (output / INDEX).write_bytes(json_bytes(index))
    checksums = "".join(f"{item['sha256']}  {item['file']}\n" for item in artifacts)
    checksums += f"{digest((output / INDEX).read_bytes())}  {INDEX}\n"
    (output / CHECKSUMS).write_text(checksums, encoding="utf-8")
    return index


def verify_release(output, require_python=False):
    expected_index = (output / INDEX).read_bytes()
    expected_checksums = (output / CHECKSUMS).read_bytes()
    with tempfile.TemporaryDirectory(prefix="purepy-release-verify-") as directory:
        temporary = Path(directory)
        for path in output.iterdir():
            if path.name not in {INDEX, CHECKSUMS}:
                if not path.is_file() or path.is_symlink():
                    raise ValueError(f"unexpected output entry: {path.name}")
                (temporary / path.name).write_bytes(path.read_bytes())
        result = finalize(temporary, require_python)
        if expected_index != (temporary / INDEX).read_bytes() or expected_checksums != (temporary / CHECKSUMS).read_bytes():
            raise ValueError("release index/checksums disagree with artifact contents")
        return result


def package(root, output, additions=(), expected_binary=None):
    files = source_files(root, additions)
    version_info = versions(files)
    check_schema_lock(files)
    if output.exists() and any(output.iterdir()):
        raise ValueError("package output must be empty; use a fresh directory to avoid stale artifacts")
    source_hash = digest(json_bytes(file_inventory(files)))
    binary, target, build, licenses = build_snapshot(files, expected_binary)
    metadata = {"versions": version_info, "source_tree_sha256": source_hash}
    output.mkdir(parents=True, exist_ok=True)
    prefix = f"purepy-{version_info['verifier']}"
    write_archive(output / f"{prefix}-source.tar.gz", files, {**metadata, "kind": "source"})
    bundle = {name: data for name, data in files.items()
              if name == "README.md" or name.startswith(("docs/", "manifests/schema/", "examples/reference_service/"))}
    bundle.update(licenses)
    bundle["purepy"] = binary
    write_archive(output / f"{prefix}-{target}.tar.gz", bundle,
                  {**metadata, "kind": "binary", "target": target, "build": build}, executable={"purepy"})
    return finalize(output)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--output", type=Path, default=ROOT / "dist")
    parser.add_argument("--include-source", action="append", default=[], metavar="PATH",
                        help="reviewed untracked source file, relative to root (repeatable)")
    parser.add_argument("--binary", type=Path, help="require this binary to match a fresh source-snapshot build")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--finalize", action="store_true", help="add Python artifacts and regenerate checksums")
    mode.add_argument("--verify", action="store_true", help="verify inventories, metadata and checksums")
    parser.add_argument("--require-python", action="store_true", help="require both wheel and source distribution")
    args = parser.parse_args(argv)
    try:
        if args.verify:
            result = verify_release(args.output, args.require_python)
        elif args.finalize:
            result = finalize(args.output, args.require_python)
        else:
            if args.require_python:
                raise ValueError("build Python artifacts, then use --finalize --require-python")
            result = package(args.root, args.output, args.include_source, args.binary)
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError, tarfile.TarError, zipfile.BadZipFile) as error:
        parser.exit(1, f"release preparation failed: {error}\n")
    print(json.dumps({"output": str(args.output), "source_tree_sha256": result["source_tree_sha256"],
                      "complete": result["complete"], "artifacts": len(result["artifacts"])}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
