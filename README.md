# Venera OpenList Core

[中文](README.zh.md) | [English](README.en.md)

Venera OpenList Core packages the embedded OpenList storage kernel used by VeneraPlus. It does not start an OpenList HTTP service.

The source is based on OpenList and is distributed under the [AGPL-3.0 license](LICENSE). The embedded interface exposes only the VeneraPlus JSON RPC ABI and registers Baidu Netdisk, Quark, 115, 123, OneDrive, and Google Drive.

## Releases

`VERSION` is the kernel version. A matching `core-v<version>` tag builds six immutable release assets:

| Target | Asset |
| --- | --- |
| Android | `openlist-core-android.zip` |
| iOS | `openlist-core-ios.zip` |
| Windows x64 | `openlist-core-windows-x64.zip` |
| macOS universal | `openlist-core-macos-universal.zip` |
| Linux x64 | `openlist-core-linux-x64.zip` |
| Linux arm64 | `openlist-core-linux-arm64.zip` |

Every asset contains the platform binary, `LICENSE.OpenList.txt`, and `metadata.json`. The release-level `manifest.json` records the version, source commit, RPC ABI, asset names, and SHA256 values. VeneraPlus pins this manifest and verifies an asset before use.

The core requires Android API 24 or later and macOS 13 or later. VeneraPlus itself continues to support macOS 12, where this feature is unavailable.

## Development

The CI builds the sources under `native/openlist_core` directly. It uses Go 1.27.1, gomobile `v0.0.0-20260908204917-8b95e45f8d3e`, JDK 17, Android NDK `28.0.13004108`, and Android API 24. Pull requests, version tags, and manual dispatch build all six release targets; a tag release is published only after every target succeeds.

Do not publish or replace assets under an existing tag. Release a new core version instead.
