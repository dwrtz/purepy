"""Create a normalized local binary archive and SHA-256 checksum.

Run after `make build`. This prepares files; it does not publish a release.
"""

from hashlib import sha256
from pathlib import Path
import platform
import tarfile


def main():
    root = Path(__file__).resolve().parents[1]
    binary = root / "bin" / "purepy"
    if not binary.is_file():
        raise SystemExit("Run make build first")
    target = f"{platform.system().lower()}-{platform.machine().lower()}"
    output = root / "dist"
    output.mkdir(exist_ok=True)
    archive = output / f"purepy-0.1.0-dev-{target}.tar"
    entries = [
        (binary, "purepy"),
        (root / "README.md", "README.md"),
        (root / "docs" / "IMPLEMENTATION.md", "docs/IMPLEMENTATION.md"),
        (root / "docs" / "DIAGNOSTICS.md", "docs/DIAGNOSTICS.md"),
        (root / "docs" / "SYNTAX_MATRIX.md", "docs/SYNTAX_MATRIX.md"),
    ]
    with tarfile.open(archive, "w", format=tarfile.USTAR_FORMAT) as tar:
        for source, name in entries:
            info = tar.gettarinfo(str(source), arcname=name)
            info.mtime = 0
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            info.mode = 0o755 if source == binary else 0o644
            with source.open("rb") as stream:
                tar.addfile(info, stream)
    digest = sha256(archive.read_bytes()).hexdigest()
    checksum = archive.with_suffix(".tar.sha256")
    checksum.write_text(f"{digest}  {archive.name}\n", encoding="utf-8")
    print(archive)
    print(checksum)


if __name__ == "__main__":
    main()
