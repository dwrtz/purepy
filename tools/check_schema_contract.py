"""Validate real verifier outputs against every frozen JSON/TOML shape.

Run with: uv run --no-project --with jsonschema==4.23.0 python
          tools/check_schema_contract.py --verifier bin/purepy
"""

import argparse
import json
from pathlib import Path
import subprocess
import tempfile
import tomllib

from jsonschema import Draft202012Validator
from referencing import Registry, Resource

from package_binary import FROZEN_SCHEMAS, ROOT, SCHEMA_LOCK, check_schema_lock


def validators(root):
    documents = {name: json.loads((root / name).read_bytes()) for name in set(FROZEN_SCHEMAS) | {f"docs/schema/{kind}-v2.json" for kind in ("diagnostics", "capabilities", "explain")}}
    registry = Registry().with_resources((root.joinpath(name).as_uri(), Resource.from_contents(schema))
                                         for name, schema in documents.items())
    result = {}
    for name, schema in documents.items():
        Draft202012Validator.check_schema(schema)
        # The outer absolute reference gives relative adjacent-schema refs a base.
        result[name] = Draft202012Validator({"$ref": root.joinpath(name).as_uri()}, registry=registry)
    return result


def check(verifier, root=ROOT):
    names = set(FROZEN_SCHEMAS) | {SCHEMA_LOCK, "internal/app/check.go", "internal/manifest/manifest.go"}
    check_schema_lock({name: (root / name).read_bytes() for name in names})
    schemas = validators(root)
    count = 0

    def run(arguments, schema, status=0):
        nonlocal count
        process = subprocess.run([str(verifier), *arguments, "--format", "json", "--no-cache"],
                                 text=True, capture_output=True, timeout=30)
        if process.returncode != status:
            raise ValueError(f"unexpected status {process.returncode} for {arguments}: {process.stderr}")
        report = json.loads(process.stdout)
        schemas[f"docs/schema/{schema}-v{report["schema"]}.json"].validate(report)
        count += 1
        return report

    config = root / "examples/reference_service/purepy.toml"
    run(["check", "--config", str(config)], "diagnostics")
    capabilities = run(["capabilities", "--config", str(config)], "capabilities")
    if not capabilities["functions"]:
        raise ValueError("reference capability report unexpectedly empty")
    run(["explain", str(root / "examples/reference_service/src/app/server.py") + ":13",
         "--config", str(config)], "explain")
    functional = root / "examples/functional_core/purepy.toml"
    run(["check", "--config", str(functional)], "diagnostics")
    run(["capabilities", "core.compose", "--config", str(functional)], "capabilities")
    run(["explain", str(root / "examples/functional_core/src/core.py") + ":33", "--config", str(functional)], "explain")
    for name in root.glob("examples/**/manifests/*.toml"):
        schemas["manifests/schema/v1.json"].validate(tomllib.loads(name.read_text()))
        count += 1
    with tempfile.TemporaryDirectory(prefix="purepy-schema-") as directory:
        project = Path(directory)
        (project / "src").mkdir()
        source = project / "src/app.py"
        source.write_text('def convert(number: int) -> int:\n    return number\n\ndef bad() -> int:\n    return convert(True)\n')
        local_config = project / "purepy.toml"
        local_config.write_text('[tool.purepy]\nlanguage = "0.2"\npython_syntax = "3.14"\nsource_root = "src"\nentrypoints = []\nmanifests = []\n')
        rejected = run(["check", "--config", str(local_config)], "diagnostics", status=1)
        if not any("types" in item for item in rejected["diagnostics"]):
            raise ValueError("rejection fixture failed to exercise structured diagnostic types")
        run(["explain", str(source) + ":5", "--config", str(local_config)], "explain")
        local_config.write_text(local_config.read_text() + 'unknown = true\n')
        run(["check", "--config", str(local_config)], "diagnostics", status=2)
    return count


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verifier", type=Path, default=ROOT / "bin/purepy")
    args = parser.parse_args()
    count = check(args.verifier.resolve())
    print(f"Validated {count} live reports/manifests against the current schemas")


if __name__ == "__main__":
    main()
