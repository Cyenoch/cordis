---
title: 稳定性
description: 哪些接口承诺兼容，哪些还会变，以及变化如何公布。
---

# 稳定性

Cordis 处于 v0。线上协议和文件格式已经稳定；Go API 在 v0 的次版本之间还会调整。

## 稳定的部分

下面这些由 `schema/` 下的 schema 文件定义，从 v0.1.0 起只做向后兼容的修改：

| 对象 | 标识 |
| --- | --- |
| [会话协议](protocol.md) | `cordis-go.session/v1`，协议 major 1 |
| [包格式](packages.md)：清单、完整性、签名 | `cordis-go.package/v1` 及同目录的 integrity、signature schema |
| [契约](contracts.md) | `cordis-go.contract/v1` |

向后兼容的修改指：增加可选字段，增加经 `features` 协商的会话特性，提高协议的 minor。按 v0.1.0 写的插件和包，在之后的 Host 上照常运行；按之后版本写的插件遇到较旧的 Host 时，在初始化协商阶段以 `incompatible` 明确失败，而不是运行到一半出错。

删除或改变已有字段的含义、增加必填字段，都是破坏性修改，必须换新的标识（`/v2`、协议 major 2）。

## 还会变的部分

- **Go API**：`cordis`、`contract`、`hosting`、`sdk`、`loader` 等包的导出标识符，在 v0.x 的次版本之间可能改名、改签名或移除。修订版本（v0.1.x）不做破坏性修改。
- **生成的代码**：`cordis_gen.go` 的内容随生成器变化；升级 Cordis 后重新运行 `cordis generate`。推导出的契约 `cordis_gen.json` 属于上面稳定的契约格式。
- **CLI**：命令、标志与输出在次版本之间可能调整。
- **Host 的存储目录**：由同一版本或更新版本的 Cordis 打开；不承诺用旧版本打开新版本写过的目录。

## 变化如何公布

每个版本的变化记在仓库根目录的 `CHANGELOG.md`，破坏性修改单独列出并给出改法。版本号遵循语义化版本在 v0 阶段的惯例：次版本可以有破坏性修改，修订版本只有修复。
