# Proposal

## Why

2026-10-28 对提交 164340f 做了全仓只读审查：按包分给四个只读 reviewer，作者逐项复核关键结论。目标是清理过度冗余、过度兜底与过度防御的代码和低价值测试，补充关键测试，找出缺陷、设计问题与未充分利用 Go 能力的地方。审查确认以下缺陷会产生错误结果：

- 一批调用超时后，guest 接收端仍把尚未退出的 handler 计入在途数，下一次合法调用被判为 `peer exceeded inflight limit`，健康会话被关闭并归咎插件。临时探针测试已复现。
- `Replace` 借用持久停用路径退役旧实例；切换提交失败或进程中断时，目标包的 entry 被持久写为停用，重开后 `Restore` 跳过它们。
- 命名空间 `Configure` 按含 `DataVersion` 的完整配置判等；一次 `Migrate` 后，宿主按清单重开并 `Configure` 得到冲突，配额也无法再调整，唯一出路是删除数据。
- YAML 纯量 `+-0x10` 被解析为数字 16，而 yaml.v3 视其为字符串，`-+0x10` 在本实现中也仍是字符串。
- 每个复制文件都 fsync：每次 activation 快照在持有 Store 锁时逐文件全盘刷写，安装对同一批文件同步两次。
- `Select` 持锁以 `context.Background()` 重新哈希整个包，取消 `Replace` 要等哈希结束才生效。
- `Sink.PublicError` 在默认的 nil Sink 上被调用，只因方法未读取接收者才不崩溃。

此外有三项需要决定的行为（stderr 终身限额、发布者变更规则、双方在途上限不协商）和约 600 行可删除的冗余代码与测试。

## What Changes

- 修复上述缺陷，每项先以在旧实现上失败的回归确认。
- 删除跨层重复校验、不可能失败的错误返回、无人读取的事务日志、死代码与重复测试；用标准库替换手写辅助函数；以 `testing/synctest` 替换依赖 `time.Sleep` 或内部调用次数的时序测试。
- 补充破坏性或安全相关路径的缺失测试：`DeleteData` 卸载、`Policy.Revoke` 取消在途租约、级联卸载活跃依赖方、外部作者子实例、Supervise 等待期间停用、Close 后的管理准入、压缩后保留数据版本。
- Integrator 已根据用户授权明确 stderr、发布者、在途预算、公开 API 与清理并发决策，先更新本 change 的规格再实现；详见 design.md。

非目标：改变会话帧格式、`cordis-go.*` v1 schema 标识、签名字节或 go.mod/go.sum；未经 Windows 实测的行为改动只记录为待验证项。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-execution`：接收端在途计数只含仍有效的请求。
- `framework-packages`：替换不改变持久启用意图；包选择校验响应取消；快照不做逐文件持久同步。
- `framework-access`：命名空间在迁移后可按清单重复配置，配额可调整。
- `framework-host-integration`：YAML 数字识别不接受双重符号。

## Impact

写集按 tasks 分组：`wire`；根包与 `internal/core`；`packagehost`；`access`；`config`、`schema` 与 `cmd/cordis`；`runtime`、`cmd/cordis-worker` 与 `sdk`；以及各组对应的 docs 与测试。公开 Go 签名的精简按下述未发布前提直接调整。

公开契约变化经独立审查：使用包级 `access.PublicError`，`Store.CheckSelect` 直接增加 ctx，删除无用途 API 和不可能的 error 返回，不留转调包装。用户明确项目尚未正式使用，不保留旧 catalog transaction 字段或升级胶水。`contract.Error` 支持按 Code 匹配，未知字段错误提供类型信息。通用 Runtime 启动前拒绝与 SDK 不一致的在途预算，不新增协议字段。
