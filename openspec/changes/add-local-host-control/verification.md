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
- Windows/amd64 测试二进制及 Darwin/amd64 测试二进制跨编译通过。Windows 创建 DACL 测试已编入 conformance，实际运行等待 CI，不能以跨编译替代。
- 最终 `bash conformance/run.sh`、vet、module verify、文档示例/测试/25 页构建、严格 OpenSpec、schema 和 actionlint 全部通过；diff 无空白错误。

## Independent reviews and fixes

Bacon 发现 Darwin 继承 ACL 可绕过 mode 0600，以及 Store 执行快照长锁阻塞查询。Laplace 进一步指出创建后检查/清 ACL 无法撤回已经打开的读句柄。最终改为 Darwin open_extended 携带当前 uid/gid、0600 和空 NO_INHERIT ACL 原子创建；Windows 从 CreateFile 起使用受保护 DACL。

Store.Entries 改为提交后不可变 map 的原子读取，返回副本；Snapshot 复制/hash 移出长锁，交付前重新验证选择、签名、当前信任和依赖。Laplace 审查这些 Store 边界未发现可确认回归；新增测试验证待提交写入不阻塞旧快照查询、复制期间撤销信任不会放行执行。所有修正包含在最终本机 conformance 中。

## Pending delivery

五平台实际实现 SHA、Windows 实际 DACL、CI 运行、归档和最终远端一致性尚待交付。
