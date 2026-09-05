"""Release artifacts must identify, reproduce and safely contain their sources."""

from copy import deepcopy
import gzip
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import package_binary as release


def sources():
    files = {name: (release.ROOT / name).read_bytes() for name in release.REQUIRED_SOURCES}
    for name in ("internal/manifest/manifest.go", "python/purepy/__init__.py", "python/purepy/py.typed",
                 "internal/unicodenames/ucd/LICENSE"):
        files[name] = (release.ROOT / name).read_bytes()
    return files


def metadata(files):
    return {"versions": release.versions(files), "source_tree_sha256": release.digest(release.json_bytes(release.file_inventory(files)))}


def archives(output, files=None):
    files = sources() if files is None else files
    version = release.versions(files)["verifier"]
    common = metadata(files)
    source = output / f"purepy-{version}-source.tar.gz"
    binary = output / f"purepy-{version}-darwin-arm64.tar.gz"
    release.write_archive(source, files, {**common, "kind": "source"})
    release.write_archive(binary, {"purepy": b"test executable", "licenses/GO.txt": b"test license",
                                  "docs/RELEASE_NOTES.md": b"notes", "docs/PUREPY_SPEC.md": b"spec"},
                          {**common, "kind": "binary", "target": "darwin-arm64"}, {"purepy"})
    return source, binary


def python_packages(output, files=None, tamper=False):
    files = sources() if files is None else files
    versions = release.versions(files)
    name = versions["python_distribution"].replace("-", "_")
    prefix = f"{name}-{versions['python_package']}"
    info = f"Name: {versions['python_distribution']}\nVersion: {versions['python_package']}\n".encode()
    runtime = {name.removeprefix("python/"): data for name, data in files.items() if name.startswith("python/purepy/")}
    with zipfile.ZipFile(output / f"{prefix}-py3-none-any.whl", "w") as archive:
        # Match the fixed epoch used by the real wheel build; writestr with a
        # plain filename otherwise injects the wall clock into test artifacts.
        for name, data in sorted(runtime.items()):
            entry = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            archive.writestr(entry, b"different implementation" if tamper and name == "purepy/value.py" else data)
        archive.writestr(zipfile.ZipInfo(f"{prefix}.dist-info/METADATA", date_time=(1980, 1, 1, 0, 0, 0)), info)
    with tarfile.open(output / f"{prefix}.tar.gz", "w:gz") as archive:
        for name, data in {**runtime, "PKG-INFO": info, "pyproject.toml": files["python/pyproject.toml"]}.items():
            entry = tarfile.TarInfo(f"{prefix}/{name}")
            entry.size = len(data)
            archive.addfile(entry, io.BytesIO(data))


class ReleaseArtifactTests(unittest.TestCase):
    def test_archives_are_byte_identical_and_extract_with_fixed_permissions(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            left, right = root / "first", root / "second"
            left.mkdir(); right.mkdir()
            first = archives(left)
            second = archives(right, dict(reversed(list(sources().items()))))
            for a, b in zip(first, second):
                self.assertEqual(a.read_bytes(), b.read_bytes())
                self.assertEqual(a.read_bytes()[4:8], bytes(4))  # gzip mtime
                self.assertFalse(a.read_bytes()[3] & 8)  # no embedded local filename
                with tarfile.open(a) as archive:
                    self.assertEqual(archive.getnames(), sorted(archive.getnames()))
                    archive.extractall(root / "extracted", filter="data")
                    for member in archive:
                        extracted = root / "extracted" / member.name
                        self.assertTrue(extracted.is_file())
                        self.assertEqual(extracted.stat().st_mode & 0o777, member.mode)
            source = root / "extracted" / first[0].name.removesuffix(".tar.gz")
            self.assertEqual(release.source_files(source), sources())
            (source / "README.md").write_text("changed")
            with self.assertRaisesRegex(ValueError, "snapshot changed"):
                release.source_files(source)

    def test_git_inventory_does_not_sweep_untracked_or_ignored_files(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "tracked.py").write_text("tracked")
            (root / "new.py").write_text("new")
            (root / ".env").write_text("secret")
            result = subprocess.CompletedProcess([], 0, stdout=b"tracked.py\0")
            with patch.object(release, "REQUIRED_SOURCES", {"tracked.py"}), patch.object(release.subprocess, "run", return_value=result):
                self.assertEqual(set(release.source_files(root)), {"tracked.py"})
                self.assertEqual(set(release.source_files(root, ["new.py"])), {"tracked.py", "new.py"})
                with self.assertRaisesRegex(ValueError, "excluded"):
                    release.source_files(root, [".env"])

    def test_source_symlink_and_linked_parent_are_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "real").mkdir()
            (root / "real/file.py").write_text("content")
            (root / "linked.py").symlink_to(root / "real/file.py")
            (root / "linked").symlink_to(root / "real", target_is_directory=True)
            for name in ("linked.py", "linked/file.py"):
                with self.assertRaisesRegex(ValueError, "symlink"):
                    release.read_source(root, name)

    def test_unsafe_source_names_fail_closed(self):
        for name in ("../outside", "/absolute", "a/../b", "a//b", "a\\b", "a\nb", ".git/config",
                     ".venv/file", "src/__pycache__/a.pyc", "private.pem", "credentials", "build/output"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                release.source_name(name)

    def test_version_owners_and_lock_must_agree(self):
        files = sources()
        files["internal/app/check.go"] = files["internal/app/check.go"].replace(
            ('const Version = "' + release.versions(files)["verifier"] + '"').encode(),
            b'const Version = "3.2.1-dev"')
        files["python/pyproject.toml"] = b'[project]\nname = "purepy-runtime"\nversion = "7.8.9"\n'
        files["python/uv.lock"] = b'[[package]]\nname = "purepy-runtime"\nversion = "7.8.9"\n'
        versions = release.versions(files)
        self.assertEqual(versions["verifier"], "3.2.1-dev")
        self.assertEqual(versions["python_package"], "7.8.9")
        self.assertEqual(versions["python_distribution"], "purepy-runtime")
        files["python/uv.lock"] = files["python/uv.lock"].replace(b'"7.8.9"', b'"7.8.10"')
        with self.assertRaisesRegex(ValueError, "disagree"):
            release.versions(files)

    def test_schema_pin_detects_drift_and_missing_entries(self):
        files = sources()
        release.check_schema_lock(files)
        files["docs/schema/diagnostics-v1.json"] += b" "
        with self.assertRaisesRegex(ValueError, "frozen schema changed"):
            release.check_schema_lock(files)
        files = sources()
        lock = json.loads(files[release.SCHEMA_LOCK]); lock["files"] = {}
        files[release.SCHEMA_LOCK] = release.json_bytes(lock)
        with self.assertRaisesRegex(ValueError, "incomplete"):
            release.check_schema_lock(files)

    def test_complete_bundle_and_read_only_verification(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp)
            archives(output)
            self.assertFalse(release.finalize(output)["complete"])
            with self.assertRaisesRegex(ValueError, "wheel and source"):
                release.finalize(output, require_python=True)
            python_packages(output)
            (output / ".gitignore").write_bytes(b"*")  # uv's output marker
            result = release.finalize(output, require_python=True)
            self.assertTrue(result["complete"])
            before = {p.name: p.read_bytes() for p in output.iterdir()}
            self.assertEqual(release.verify_release(output, True), result)
            self.assertEqual(before, {p.name: p.read_bytes() for p in output.iterdir()})
            self.assertEqual(len(result["artifacts"]), 4)
            (output / ".gitignore").write_text("unexpected contents")
            with self.assertRaisesRegex(ValueError, "unexpected release artifact"):
                release.verify_release(output, True)

    def test_same_version_wrong_python_implementation_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp)
            archives(output)
            python_packages(output, tamper=True)
            with self.assertRaisesRegex(ValueError, "wheel code differs"):
                release.finalize(output, require_python=True)

    def test_sdist_normalization_removes_clock_and_owner_variation(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            left, right = root / "first", root / "second"
            left.mkdir(); right.mkdir()
            for output, second in ((left, 0), (right, 2)):
                archives(output)
                # Separate ZIP's two-second clock windows deterministically.
                with patch("zipfile.time.localtime", return_value=(2026, 9, 5, 12, 0, second, 5, 248, -1)):
                    python_packages(output)
            version_info = release.versions(sources())
            project = version_info["python_distribution"].replace("-", "_")
            sdist = right / f"{project}-{version_info['python_package']}.tar.gz"
            with tarfile.open(sdist) as archive:
                entries = [(deepcopy(item), archive.extractfile(item).read()) for item in archive]
            with tarfile.open(sdist, "w:gz") as archive:
                for item, data in reversed(entries):
                    item.mtime = 123456789
                    item.uid, item.gid = 501, 20
                    item.uname, item.gname = "local-user", "local-group"
                    archive.addfile(item, io.BytesIO(data))
            for output in (left, right):
                release.finalize(output, require_python=True)
                release.verify_release(output, require_python=True)
            self.assertEqual((left / release.CHECKSUMS).read_bytes(), (right / release.CHECKSUMS).read_bytes())

    def test_index_checksum_and_payload_tampering_fail_without_rewriting(self):
        for name in (release.INDEX, release.CHECKSUMS):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temp:
                output = Path(temp)
                archives(output); release.finalize(output)
                (output / name).write_bytes((output / name).read_bytes() + b" ")
                before = (output / name).read_bytes()
                with self.assertRaisesRegex(ValueError, "index/checksums disagree"):
                    release.verify_release(output)
                self.assertEqual((output / name).read_bytes(), before)
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp)
            _, binary = archives(output)
            raw = gzip.decompress(binary.read_bytes()).replace(b"test executable", b"evil executable")
            binary.write_bytes(gzip.compress(raw, mtime=0))
            with self.assertRaisesRegex(ValueError, "content differs"):
                release.verify_archive(binary)

    def test_raw_sdist_gzip_header_tampering_fails_without_rewriting(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp)
            archives(output)
            python_packages(output)
            index = release.finalize(output, require_python=True)
            artifact = next(item for item in index["artifacts"] if item["kind"] == "python_sdist")
            sdist = output / artifact["file"]
            changed = bytearray(sdist.read_bytes())
            changed[4:8] = (12345).to_bytes(4, "little")
            sdist.write_bytes(changed)
            self.assertNotEqual(release.digest(changed), artifact["sha256"])
            before = {path.name: path.read_bytes() for path in output.iterdir()}
            with self.assertRaisesRegex(ValueError, "checksum"):
                release.verify_release(output, require_python=True)
            self.assertEqual(before, {path.name: path.read_bytes() for path in output.iterdir()})

    def test_release_index_rejects_ambiguous_unsafe_and_incomplete_inventories(self):
        for mutation in ("duplicate", "traversal", "boolean_size", "bad_digest", "missing"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temp:
                output = Path(temp)
                archives(output)
                index = release.finalize(output)
                artifact = index["artifacts"][0]
                if mutation == "duplicate":
                    index["artifacts"].append(deepcopy(artifact))
                elif mutation == "traversal":
                    artifact["file"] = "../outside.tar.gz"
                elif mutation == "boolean_size":
                    artifact["size"] = True
                elif mutation == "bad_digest":
                    artifact["sha256"] = "not a sha256 digest"
                else:
                    (output / artifact["file"]).unlink()
                (output / release.INDEX).write_bytes(release.json_bytes(index))
                before = {path.name: path.read_bytes() for path in output.iterdir()}
                with self.assertRaises(ValueError):
                    release.verify_release(output)
                self.assertEqual(before, {path.name: path.read_bytes() for path in output.iterdir()})

    def test_duplicate_link_traversal_and_non_normalized_tar_members_fail(self):
        for kind in ("duplicate", "link", "traversal", "timestamp"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temp:
                output = Path(temp)
                _, binary = archives(output)
                with tarfile.open(binary) as archive:
                    entries = [(deepcopy(member), archive.extractfile(member).read()) for member in archive]
                if kind == "duplicate":
                    entries.append(entries[-1])
                elif kind == "link":
                    entries[-1][0].type = tarfile.SYMTYPE
                    entries[-1][0].linkname = "/outside"
                elif kind == "traversal":
                    entries[-1][0].name += "/../escape"
                else:
                    entries[-1][0].mtime = 1
                with tarfile.open(binary, "w:gz", format=tarfile.USTAR_FORMAT) as archive:
                    for member, data in entries:
                        archive.addfile(member, io.BytesIO(data))
                with self.assertRaises(ValueError):
                    release.verify_archive(binary)

    def test_stale_artifacts_and_mismatched_snapshots_are_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp)
            archives(output)
            (output / "unrelated.zip").write_bytes(b"stale")
            with self.assertRaisesRegex(ValueError, "unexpected release artifact"):
                release.finalize(output)
            (output / "unrelated.zip").unlink()
            files = sources(); files["README.md"] += b"changed"
            path = output / f"purepy-{release.versions(files)['verifier']}-source.tar.gz"
            release.write_archive(path, files, {**metadata(files), "kind": "source"})
            with self.assertRaisesRegex(ValueError, "disagree on source snapshot"):
                release.finalize(output)


if __name__ == "__main__":
    unittest.main()
