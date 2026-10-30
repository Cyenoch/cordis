# Verification

## Local implementation

日期 2026-10-30，基线 `1d640b02074db9981a076c483e97999b5b11ec87` 之后的工作树；darwin/arm64、Go 1.27.3。

- Host.Retry 原子检查当前登记、identity、desired revision、Failed 及清理完成状态，保留配置意图；取消只结束等待。
- 显式回环 HTTP、会话随机认证、严格有限请求/响应、私有连接文件、固定控制动作和独立查询已实现。客户端禁用代理/重定向，拒绝远程地址、错误权限及链接。
- 控制沿用 Host 和子树预约；普通 Host 错误/传输失败归为 unknown，CommitError 保留提交证据，不猜测回滚或重发。

## Actual checks

- `TestLocalControlAcrossProcesses` 从独立进程执行 host/control，使用真实签名 native 包验证动态归属、持久 disabled 记录、配置保留、父子启停、人工 retry、历史错误后的已停止状态、离线目录锁及正常退出删除连接文件。
- `TestControlSlowCleanupRemainsQueryableAndCancellationDoesNotUndoStop` 验证慢清理、并发冲突、断连不撤销已接受 Stop、关闭取消 handler 后独立等待 Host。
- core/root 的 retry 测试覆盖清理门槛、旧登记/revision、并发配置提交及取消后继续初始化。
- Darwin 实际检验创建时的空 NO_INHERIT ACL，以及已有继承 ACL 文件的拒绝；未创建第二个 OS 用户进行攻击演示。
- Windows/amd64 测试二进制及 Darwin/amd64 测试二进制跨编译通过。本机阶段仅有跨编译证据；Windows 创建时 DACL 的实际执行已由下方 Windows job 补齐。
- 最终 `bash conformance/run.sh`、vet、module verify、文档示例/测试/25 页构建、严格 OpenSpec、schema 和 actionlint 全部通过；diff 无空白错误。

## Independent reviews and fixes

Bacon 发现 Darwin 继承 ACL 可绕过 mode 0600，以及 Store 执行快照长锁阻塞查询。Laplace 进一步指出创建后检查/清 ACL 无法撤回已经打开的读句柄。最终改为 Darwin open_extended 携带当前 uid/gid、0600 和空 NO_INHERIT ACL 原子创建；Windows 从 CreateFile 起使用受保护 DACL。

Store.Entries 改为提交后不可变 map 的原子读取，返回副本；Snapshot 复制/hash 移出长锁，交付前重新验证选择、签名、当前信任和依赖。Laplace 审查这些 Store 边界未发现可确认回归；新增测试验证待提交写入不阻塞旧快照查询、复制期间撤销信任不会放行执行。所有修正包含在最终本机 conformance 中。

## Verified implementation

2026-10-30，最终实现提交 `e2181fab165b0571c83e85a1d6646060b18d9c3a`。[Conformance](https://github.com/Cyenoch/cordis/actions/runs/41012551756) 的五个平台均在对应原生 runner 上执行 integration/race、真实 native 验收、vet 和 module verify；包含多包开发、本机控制和有限恢复，不是交叉编译结论。Contracts job 的 schema 与严格 OpenSpec 检查通过。

| 平台 | CI job | 结果 |
|---|---|---|
| macOS amd64 | [123921460216](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460216) | 通过 |
| Linux arm64 | [123921460484](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460484) | 通过 |
| Linux amd64 | [123921460568](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460568) | 通过 |
| macOS arm64 | [123921460661](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460661) | 通过 |
| Windows amd64 | [123921460717](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460717) | 通过 |

同一提交的[Documentation](https://github.com/Cyenoch/cordis/actions/runs/41012551777) 通过文档测试、可执行示例和构建，并已部署 GitHub Pages。

Windows job 实际执行创建时受保护 DACL、连接文件权限及双 CLI 进程控制测试；两种 macOS runner 实际执行原子 NO_INHERIT ACL 与 native 控制。未以其他真实 OS 用户进行攻击演示。

## Release linkage

本项实现已包含在固定提交 `e2181fab165b0571c83e85a1d6646060b18d9c3a` 的 [v0.1.0-alpha.1](https://github.com/Cyenoch/cordis/releases/tag/v0.1.0-alpha.1) 中。[Release workflow](https://github.com/Cyenoch/cordis/actions/runs/41013139380) 再次通过五平台真实运行；下载资产核验记录见[发布验收](../2026-10-30-prepare-native-plugin-release/verification.md)。

## Archive

2026-10-30：6/6 项任务完成，主规格已同步并通过严格校验，按 release → dev → control → recovery 的依赖顺序归档。归档及证据通过后续文档提交交付；发行 tag 保持指向已验收的实现提交。
