import json
import plistlib
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import call, patch


SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))

import build_openlist_core as build


class BuildOpenListCoreTest(unittest.TestCase):
    @patch.object(build, "run")
    def test_installs_fixed_gomobile_tools(self, mock_run):
        build.install_gomobile()

        mock_run.assert_has_calls(
            [
                call("go", "install", f"golang.org/x/mobile/cmd/gomobile@{build.GOMOBILE_VERSION}", cwd=build.CORE),
                call("go", "install", f"golang.org/x/mobile/cmd/gobind@{build.GOMOBILE_VERSION}", cwd=build.CORE),
                call("gomobile", "init", cwd=build.CORE),
            ]
        )

    @patch.object(build, "verify_android_binding")
    @patch.object(build, "run")
    def test_android_binding_uses_api_24(self, mock_run, _verify):
        source = Path("output/openlistcore.aar")

        build.build_android(Path("output"))

        self.assertIn(
            call(
                "gomobile", "bind", "-target=android/arm,android/arm64,android/amd64",
                "-androidapi=24", "-o", str(source), "./native/openlist_core", cwd=build.ROOT,
            ),
            mock_run.call_args_list,
        )

    def test_package_contains_only_requested_binary_and_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / "venera_openlist.dll"
            binary.write_bytes(b"binary")
            license_file = root / "LICENSE"
            license_file.write_text("license", encoding="utf-8")
            (root / "VERSION").write_text("0.1.0-beta.1\n", encoding="utf-8")
            with patch.object(build, "ROOT", root), patch.object(build, "DIST", root / "dist"), patch.object(build, "LICENSE", license_file), patch.object(build, "source_commit", return_value="a" * 40):
                archive = build.package("windows-x64", [binary], ["x86_64"], None)
            with zipfile.ZipFile(archive) as bundle:
                self.assertEqual(set(bundle.namelist()), {"venera_openlist.dll", "LICENSE.OpenList.txt", "metadata.json"})
                metadata = json.loads(bundle.read("metadata.json"))
            self.assertEqual(metadata["platform"], "windows-x64")
            self.assertEqual(metadata["rpcAbi"], 1)
            self.assertNotIn("minOS", metadata)

    def test_android_binding_requires_all_requested_jni_abis(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / "openlistcore.aar"
            with zipfile.ZipFile(archive, "w") as bundle:
                bundle.writestr("classes.jar", b"jar")
                for abi in ("armeabi-v7a", "arm64-v8a", "x86_64"):
                    bundle.writestr(f"jni/{abi}/libgojni.so", b"so")
            with patch.object(build, "output", return_value="public static native java.lang.String invoke(java.lang.String);"):
                build.verify_android_binding(archive)

    def test_ios_binding_requires_device_and_simulator_slices(self):
        with tempfile.TemporaryDirectory() as directory:
            framework = Path(directory) / "Openlistcore.xcframework"
            framework.mkdir()
            (framework / "Openlistcore.h").write_text("OpenlistcoreInvoke", encoding="utf-8")
            for name in ("device/Openlistcore", "simulator/Openlistcore"):
                file = framework / name
                file.parent.mkdir(exist_ok=True)
                file.write_bytes(b"archive")
            (framework / "Info.plist").write_bytes(plistlib.dumps({"AvailableLibraries": [
                {"SupportedPlatform": "ios", "SupportedArchitectures": ["arm64"]},
                {"SupportedPlatform": "ios", "SupportedPlatformVariant": "simulator", "SupportedArchitectures": ["arm64", "x86_64"]},
            ]}))
            with patch.object(build, "output", return_value="current ar archive"):
                self.assertEqual(build.verify_ios_binding(framework), ["arm64", "arm64-simulator", "x86_64-simulator"])

    @patch.object(build, "output")
    def test_macos_minimum_version_uses_13(self, mock_output):
        mock_output.side_effect = ["arm64 x86_64", "cmd LC_BUILD_VERSION\nminos 13.0", "cmd LC_BUILD_VERSION\nminos 13.0"]
        build.verify_macos_architectures(Path("libvenera_openlist.dylib"))

    @patch.object(build, "output")
    def test_linux_architecture_accepts_readelf_vendor_prefixes(self, mock_output):
        mock_output.return_value = "  Machine:                           Advanced Micro Devices X86-64"
        build.verify_linux_architecture(Path("libvenera_openlist.so"), "linux-x64")
        mock_output.return_value = "  Machine:                           AArch64"
        build.verify_linux_architecture(Path("libvenera_openlist.so"), "linux-arm64")


if __name__ == "__main__":
    unittest.main()
