#!/usr/bin/env python3
"""Create the release manifest only after every Core asset is available."""

from __future__ import annotations

import hashlib
import json
import subprocess
import zipfile
from pathlib import Path

from build_openlist_core import ASSETS, ROOT, RPC_ABI, version


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify_asset(path: Path, target: str, commit: str) -> None:
    with zipfile.ZipFile(path) as bundle:
        try:
            metadata = json.loads(bundle.read("metadata.json"))
        except KeyError as error:
            raise RuntimeError(f"missing metadata.json in {path.name}") from error
    expected = {"version": version(), "sourceCommit": commit, "rpcAbi": RPC_ABI, "platform": target}
    if any(metadata.get(key) != value for key, value in expected.items()):
        raise RuntimeError(f"invalid metadata for {path.name}: {metadata}")


def main() -> None:
    dist = ROOT / "dist"
    files = {target: dist / name for target, name in ASSETS.items()}
    missing = [str(path) for path in files.values() if not path.is_file()]
    if missing:
        raise SystemExit("missing release assets: " + ", ".join(missing))
    commit = subprocess.check_output(("git", "rev-parse", "HEAD"), cwd=ROOT, text=True).strip()
    for target, path in files.items():
        verify_asset(path, target, commit)
    manifest = {
        "repository": "Venera-Works/Venera-OpenList-Core",
        "version": version(),
        "tag": f"core-v{version()}",
        "sourceCommit": commit,
        "rpcAbi": RPC_ABI,
        "assets": {target: {"name": path.name, "sha256": sha256(path)} for target, path in files.items()},
    }
    (dist / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
