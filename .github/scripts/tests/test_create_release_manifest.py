import json
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch


SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))

import build_openlist_core as build
import create_release_manifest as manifest


class CreateReleaseManifestTest(unittest.TestCase):
    def test_manifest_covers_every_fixed_asset(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dist = root / "dist"
            dist.mkdir()
            (root / "VERSION").write_text("0.1.0-beta.1\n", encoding="utf-8")
            commit = "b" * 40
            for target, name in build.ASSETS.items():
                with zipfile.ZipFile(dist / name, "w") as bundle:
                    bundle.writestr("metadata.json", json.dumps({"version": "0.1.0-beta.1", "sourceCommit": commit, "rpcAbi": 1, "platform": target}))
            with patch.object(build, "ROOT", root), patch.object(manifest, "ROOT", root), patch.object(manifest.subprocess, "check_output", return_value=commit):
                manifest.main()
            actual = json.loads((dist / "manifest.json").read_text(encoding="utf-8"))
        self.assertEqual(actual["repository"], "Venera-Works/Venera-OpenList-Core")
        self.assertEqual(actual["tag"], "core-v0.1.0-beta.1")
        self.assertEqual(actual["sourceCommit"], "b" * 40)
        self.assertEqual(set(actual["assets"]), set(build.ASSETS))
        self.assertTrue(all(item["sha256"] for item in actual["assets"].values()))


if __name__ == "__main__":
    unittest.main()
