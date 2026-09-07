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

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import package_binary as release


def sources():
    files = {name: (release.ROOT / name).read_bytes() for name in release.REQUIRED_SOURCES}
    for name in ("internal/manifest/manifest.go",
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
        versions = release.versions(files)
        self.assertEqual(versions["verifier"], "3.2.1-dev")
        self.assertEqual(versions["supported_languages"], ["0.2"])
        self.assertNotIn("python_distribution", versions)

    def test_schema_pin_detects_drift_and_missing_entries(self):
        files = sources()
        release.check_schema_lock(files)
        files["docs/schema/diagnostics-v2.json"] += b" "
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
            result = release.finalize(output)
            self.assertTrue(result["complete"])
            before = {p.name: p.read_bytes() for p in output.iterdir()}
            self.assertEqual(release.verify_release(output), result)
            self.assertEqual(before, {p.name: p.read_bytes() for p in output.iterdir()})
            self.assertEqual(len(result["artifacts"]), 2)

    def test_python_distributions_are_rejected(self):
        for filename in ("purepy-0.1-py3-none-any.whl", "purepy-0.1.tar.gz"):
            with self.subTest(filename=filename), tempfile.TemporaryDirectory() as temp:
                output = Path(temp)
                archives(output)
                (output / filename).write_bytes(b"runtime distribution")
                with self.assertRaisesRegex(ValueError, "no Python distribution"):
                    release.finalize(output)

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
