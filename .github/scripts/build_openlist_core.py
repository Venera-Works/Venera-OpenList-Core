#!/usr/bin/env python3
"""Build and package one Venera OpenList Core release asset in CI."""

from __future__ import annotations

import argparse
import json
import os
import plistlib
import re
import shutil
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path


GOMOBILE_VERSION = "v0.0.0-20260908204917-8b95e45f8d3e"
GO_VERSION = "1.27.1"
ANDROID_API = "24"
RPC_ABI = 1
CORE_BUILD_FLAGS = ("-tags=venera_core", "-trimpath", "-ldflags=-s -w")
ROOT = Path(__file__).resolve().parents[2]
CORE = ROOT / "native" / "openlist_core"
DIST = ROOT / "dist"
LICENSE = ROOT / "LICENSE"
ASSETS = {
    "android": "openlist-core-android.zip",
    "ios": "openlist-core-ios.zip",
    "windows-x64": "openlist-core-windows-x64.zip",
    "macos-universal": "openlist-core-macos-universal.zip",
    "linux-x64": "openlist-core-linux-x64.zip",
    "linux-arm64": "openlist-core-linux-arm64.zip",
}


def run(*args: str, cwd: Path = ROOT, env: dict[str, str] | None = None) -> None:
    subprocess.run(args, cwd=cwd, check=True, env=env)


def output(*args: str, cwd: Path = ROOT) -> str:
    return subprocess.check_output(args, cwd=cwd, text=True).strip()


def version() -> str:
    return (ROOT / "VERSION").read_text(encoding="utf-8").strip()


def source_commit() -> str:
    return output("git", "rev-parse", "HEAD")


def install_gomobile() -> None:
    run("go", "install", f"golang.org/x/mobile/cmd/gomobile@{GOMOBILE_VERSION}", cwd=CORE)
    run("go", "install", f"golang.org/x/mobile/cmd/gobind@{GOMOBILE_VERSION}", cwd=CORE)
    run("gomobile", "init", cwd=CORE)


def verify_android_binding(archive: Path) -> None:
    with zipfile.ZipFile(archive) as bundle:
        names = set(bundle.namelist())
        if "classes.jar" not in names:
            raise RuntimeError("gomobile Android binding did not contain classes.jar")
        expected = {f"jni/{abi}/libgojni.so" for abi in ("armeabi-v7a", "arm64-v8a", "x86_64")}
        if not expected <= names:
            raise RuntimeError("gomobile Android binding did not contain every requested JNI ABI")
        with tempfile.TemporaryDirectory(prefix="openlist-aar-") as directory:
            classes = Path(directory) / "classes.jar"
            classes.write_bytes(bundle.read("classes.jar"))
            binding = output("javap", "-classpath", str(classes), "openlistcore.Openlistcore")
    if "public static native java.lang.String invoke(java.lang.String);" not in binding:
        raise RuntimeError("gomobile Android binding did not export Openlistcore.invoke")


def verify_ios_binding(framework: Path) -> list[str]:
    headers = list(framework.rglob("*.h"))
    if not any("OpenlistcoreInvoke" in item.read_text(encoding="utf-8") for item in headers):
        raise RuntimeError("gomobile iOS binding did not export OpenlistcoreInvoke")
    binaries = [item for item in framework.rglob("Openlistcore") if item.is_file()]
    if not binaries:
        raise RuntimeError("gomobile iOS binding did not contain static libraries")
    for binary in binaries:
        if "current ar archive" not in output("file", str(binary)):
            raise RuntimeError(f"gomobile iOS binding is not static: {binary}")
    info = plistlib.loads((framework / "Info.plist").read_bytes())
    libraries = info.get("AvailableLibraries", [])
    device = next((item for item in libraries if item.get("SupportedPlatform") == "ios" and "SupportedPlatformVariant" not in item), None)
    simulator = next((item for item in libraries if item.get("SupportedPlatform") == "ios" and item.get("SupportedPlatformVariant") == "simulator"), None)
    if not device or not simulator:
        raise RuntimeError("gomobile iOS binding did not contain device and simulator slices")
    if "arm64" not in device.get("SupportedArchitectures", []) or not {"arm64", "x86_64"} <= set(simulator.get("SupportedArchitectures", [])):
        raise RuntimeError("gomobile iOS binding did not contain required arm64 and simulator architectures")
    return ["arm64", "arm64-simulator", "x86_64-simulator"]


def verify_macos_architectures(library: Path) -> None:
    architectures = set(output("lipo", "-archs", str(library)).split())
    if architectures != {"arm64", "x86_64"}:
        raise RuntimeError(f"unexpected macOS architectures: {sorted(architectures)}")
    for architecture in architectures:
        commands = output("otool", "-arch", architecture, "-l", str(library))
        if not re.search(r"cmd LC_BUILD_VERSION.*?minos 13\.", commands, re.DOTALL):
            raise RuntimeError(f"OpenList {architecture} library must require macOS 13 or later")


def smoke_desktop(library: Path) -> None:
    run(sys.executable, str(ROOT / ".github" / "scripts" / "check_openlist_core.py"), str(library))


def build_android(staging: Path) -> tuple[list[Path], list[str], str | None]:
    install_gomobile()
    archive = staging / "openlistcore.aar"
    run("gomobile", "bind", *CORE_BUILD_FLAGS, "-target=android/arm,android/arm64,android/amd64", f"-androidapi={ANDROID_API}", "-o", str(archive), "./native/openlist_core", cwd=ROOT)
    verify_android_binding(archive)
    return [archive], ["armeabi-v7a", "arm64-v8a", "x86_64"], f"Android API {ANDROID_API}"


def build_ios(staging: Path) -> tuple[list[Path], list[str], str | None]:
    install_gomobile()
    framework = staging / "Openlistcore.xcframework"
    run("gomobile", "bind", *CORE_BUILD_FLAGS, "-target=ios", "-o", str(framework), "./native/openlist_core", cwd=ROOT)
    architectures = verify_ios_binding(framework)
    return [framework], architectures, "iOS 13"


def verify_linux_architecture(library: Path, target: str) -> None:
    lines = output("readelf", "-h", str(library)).splitlines()
    machine = next((line.partition(":")[2].strip() for line in lines if line.lstrip().startswith("Machine:")), "")
    expected = "X86-64" if target == "linux-x64" else "AArch64"
    if expected not in machine:
        raise RuntimeError(f"unexpected Linux architecture for {target}: {machine}")


def build_desktop(target: str, staging: Path) -> tuple[list[Path], list[str], str | None]:
    filename = {
        "windows-x64": "venera_openlist.dll",
        "macos-universal": "libvenera_openlist.dylib",
        "linux-x64": "libvenera_openlist.so",
        "linux-arm64": "libvenera_openlist.so",
    }[target]
    destination = staging / filename
    if target == "macos-universal":
        slices = []
        for goarch, cgoarch in (("arm64", "arm64"), ("amd64", "x86_64")):
            slice_path = staging / f"{goarch}.dylib"
            environment = dict(os.environ)
            environment.update({
                "GOOS": "darwin", "GOARCH": goarch, "CGO_ENABLED": "1",
                "MACOSX_DEPLOYMENT_TARGET": "13.0",
                "CGO_CFLAGS": f"-arch {cgoarch} -mmacosx-version-min=13.0",
                "CGO_LDFLAGS": f"-arch {cgoarch} -mmacosx-version-min=13.0",
            })
            run("go", "build", *CORE_BUILD_FLAGS, "-buildmode=c-shared", "-o", str(slice_path), "./native/openlist_core/cabi", env=environment)
            slices.append(slice_path)
        run("lipo", "-create", "-output", str(destination), *(str(item) for item in slices))
        for item in slices:
            item.unlink()
            item.with_suffix(".h").unlink(missing_ok=True)
        verify_macos_architectures(destination)
        smoke_desktop(destination)
        return [destination], ["arm64", "x86_64"], "macOS 13"
    run("go", "build", *CORE_BUILD_FLAGS, "-buildmode=c-shared", "-o", str(destination), "./native/openlist_core/cabi")
    if target.startswith("linux-"):
        verify_linux_architecture(destination, target)
    smoke_desktop(destination)
    architecture = "x86_64" if target.endswith("x64") else "arm64"
    return [destination], [architecture], None


def package(target: str, files: list[Path], architectures: list[str], min_os: str | None) -> Path:
    metadata: dict[str, object] = {
        "version": version(), "sourceCommit": source_commit(), "rpcAbi": RPC_ABI,
        "platform": target, "architectures": architectures, "goVersion": GO_VERSION,
        "gomobileVersion": GOMOBILE_VERSION,
    }
    if min_os:
        metadata["minOS"] = min_os
    DIST.mkdir(exist_ok=True)
    archive = DIST / ASSETS[target]
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as bundle:
        for item in files:
            if item.is_dir():
                for child in item.rglob("*"):
                    if child.is_file():
                        bundle.write(child, child.relative_to(item.parent))
            else:
                bundle.write(item, item.name)
        bundle.write(LICENSE, "LICENSE.OpenList.txt")
        bundle.writestr("metadata.json", json.dumps(metadata, indent=2, sort_keys=True) + "\n")
    return archive


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("target", choices=ASSETS)
    args = parser.parse_args()
    if not CORE.is_dir():
        raise SystemExit(f"missing embedded core source: {CORE}")
    staging = DIST / "staging" / args.target
    if staging.exists():
        shutil.rmtree(staging)
    staging.mkdir(parents=True)
    if args.target == "android":
        files, architectures, min_os = build_android(staging)
    elif args.target == "ios":
        files, architectures, min_os = build_ios(staging)
    else:
        files, architectures, min_os = build_desktop(args.target, staging)
    print(package(args.target, files, architectures, min_os))


if __name__ == "__main__":
    main()
