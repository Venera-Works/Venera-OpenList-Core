# Venera OpenList Core

Venera OpenList Core is the embedded OpenList storage kernel for VeneraPlus. It does not run an OpenList HTTP service.

The source is based on OpenList and distributed under the [AGPL-3.0 license](LICENSE). Its embedded interface exposes only the VeneraPlus JSON RPC ABI and registers Baidu Netdisk, Quark, 115, 123, OneDrive, and Google Drive.

## Releases

`VERSION` defines the kernel version. The matching `core-v<version>` tag produces six immutable release assets: Android, iOS, Windows x64, macOS universal, Linux x64, and Linux arm64. The exact filenames are listed in [README.md](README.md).

Each asset contains its platform binary, `LICENSE.OpenList.txt`, and `metadata.json`. The release `manifest.json` records the version, source commit, RPC ABI, asset names, and SHA256 hashes. VeneraPlus pins the manifest and verifies each downloaded asset.

The core requires Android API 24 or later and macOS 13 or later. VeneraPlus itself still supports macOS 12 without this feature.

## Development

CI builds `native/openlist_core` directly with Go 1.27.1, gomobile `v0.0.0-20260908204917-8b95e45f8d3e`, JDK 17, Android NDK `28.0.13004108`, and Android API 24. Pull requests, version tags, and manual dispatch build all six targets. A tag release is published only after all targets succeed.

Published assets are immutable. Publish a new core version to rebuild an asset.
