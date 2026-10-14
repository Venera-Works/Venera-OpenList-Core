# Venera OpenList Core

[English](README.en.md)

Venera OpenList Core 是 VeneraPlus 使用的嵌入式 OpenList 存储内核，不会启动 OpenList HTTP 服务。

源码基于 OpenList，并按 [AGPL-3.0 许可证](LICENSE) 分发。嵌入接口只暴露 VeneraPlus JSON RPC ABI，且只注册百度网盘、夸克、115、123、OneDrive 和 Google Drive。

## 发布

`VERSION` 是内核版本。对应的 `core-v<version>` tag 构建六个不可覆盖的发布资产：

| 目标 | 资产 |
| --- | --- |
| Android | `openlist-core-android.zip` |
| iOS | `openlist-core-ios.zip` |
| Windows x64 | `openlist-core-windows-x64.zip` |
| macOS universal | `openlist-core-macos-universal.zip` |
| Linux x64 | `openlist-core-linux-x64.zip` |
| Linux arm64 | `openlist-core-linux-arm64.zip` |

每个资产包含平台内核、`LICENSE.OpenList.txt` 和 `metadata.json`。发布顶层的 `manifest.json` 记录版本、源码提交、RPC ABI、资产名与 SHA256。VeneraPlus 固定该 manifest，并在使用资产前验证摘要。

内核要求 Android API 24 及以上、macOS 13 及以上。VeneraPlus 应用仍支持 macOS 12；该系统版本不提供此功能。

## 开发

CI 直接构建 `native/openlist_core` 下的源码，使用 Go 1.27.1、gomobile `v0.0.0-20260908204917-8b95e45f8d3e`、JDK 17、Android NDK `28.0.13004108` 与 Android API 24。PR、版本 tag 和手动调度均构建六个目标；只有全部成功后才发布 tag 对应的 Release。

Android 使用项目已有的 CGO SQLite 驱动，避免旧版纯 Go libc 发起 seccomp 禁止的系统调用；其他平台继续使用原 SQLite 驱动。

不得覆盖已发布 tag 的资产。需要重新构建时应发布新的内核版本。
