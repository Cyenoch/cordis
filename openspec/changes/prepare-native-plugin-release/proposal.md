# Proposal

## Why

当前 Pack 将整个作者目录作为发行内容，发布者容易夹带源码、配置或私钥；仓库也没有带版本的 CLI 下载产物。需要把已可运行的 native 流程变成别人能够检查、下载和复用的交付流程。

## What Changes

- **BREAKING**：包只包含机器元数据、声明的 native 产物和显式列出的运行资源；作者声明与机器 Manifest 同步增加资源清单，不保留整目录打包模式。
- 保留打包前对声明、生成代码、SDK、全部目标产物的构建证据校验；inspect 输出最终文件清单、大小和摘要。
- 提供五平台带版本 CLI 归档、校验和与 GitHub Release 流程，交付首个预发布版本；不将预发布标为稳定认证。
- 提供可执行的多平台插件构建、签名和安装示例，私钥始终在发布目录之外。
- 不做插件市场、远程依赖求解、隐式 latest、自动升级、复杂文件匹配语言或任意构建 hooks。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-packages`：明确发行文件集合和构建证据边界。
- `framework-execution`：让 native 插件定位本激活已验证快照中的运行资源。
- `framework-host-integration`：提供可下载 CLI 和可执行发布示例。

## Impact

修改 `contract.Manifest`、`packagehost` 作者解析/打包/检查、package schema、CLI 输出、相关测试与中文文档；增加发布脚本和 CI。沿用 Go 标准库与现有依赖。构建证据仍仅用于检查意外漂移，不证明任意二进制行为。

这是本项目自己的交付决策，不是对官方 JavaScript Cordis 发布机制的兼容承诺。本次四项工作按发布交付、多包开发、本机管理、可选恢复的顺序实施，各 change 独立验证后归档。
