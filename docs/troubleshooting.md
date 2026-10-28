---
title: 故障排查
description: 从等待依赖、启动失败、权限拒绝、取消与存储错误定位问题，避免重复提交和错误恢复。
codeCommit: b14906720f6c0d804483253039e09a8b343be9c4
codePaths:
  - host.go
  - instance.go
  - contract
  - sdk
  - packagehost
  - access
  - runtime
  - wire
---

# 故障排查

先记录实际依赖版本、包引用和实例状态，再判断出错阶段。不要一看到启动失败就重装包，也不要在提交错误后直接重放操作。

```sh
go version
go list -m github.com/Cyenoch/cordis
go env GOOS GOARCH
```

宿主侧保留 `PackageRef{ID, Version, Digest}`、entry ID、activation、状态、Desired / Applied、Waiting 和稳定错误码。面向用户的错误用脱敏投影；不要把原始请求、完整文件路径或插件 stderr 直接加入日志。

## 安装与构建

| 症状 | 检查 | 处理 |
| --- | --- | --- |
| 找不到 `contract.CreateChild` | `go list -m` 是否仍是首个公开版本，或编辑器是否使用另一 go.work | 固定包含该 API 的版本，或按[作者指南](plugin-author.md)用绝对 replace 指向当前检出 |
| `is replaced but not required` | 是否在保存任何 Go 文件前运行了 tidy | 保存 main.go 后重新添加 requirement，再 tidy、build |
| 包可 inspect，但 Install / Snapshot 失败 | inspect 不评估信任、本机 artifact 或必需依赖 | 检查宿主公钥、publisher → ID 授权、吊销、选定依赖和本机 OS/Arch |
| Manifest 或配置被拒绝 | 重复 key、未知字段、configSchema、definition ID 是否一致 | 用 validate 检查归档；配置错误通过 `schema.FieldError` 定位，不靠删除 schema 绕过 |
| Native 没有匹配产物 | `go env GOOS GOARCH` 与 runtime.artifacts 是否精确匹配 | 为该目标构建并重新打包；不能把另一架构产物作为默认兜底 |
| 存储目录无法打开 | 是否已有 Host 占用，权限和完整性是否有效 | 正常关闭原 Host；损坏时保留目录并按[恢复指南](recovery.md)处理，不删除 catalog 让它“自动修复” |

`cordis validate` 成功仅说明归档结构和完整性有效。签名验证来源，不能证明 Native 代码安全。

## 实例一直 waiting

先看 `instance.Status().Waiting`，再核对以下信息：

1. 依赖 provider 的 entry 是否已启用并 active。包已安装、已选择不表示服务已发布。
2. requirement 的 name / major 是否一致，provider minor 是否位于 MinMinor / MaxMinor 闭区间。
3. caller 与 provider 的 realm 是否可见，key 是否被隔离，是否误启用了同 realm 的第二个 provider。
4. Definition.Requires 和 Manifest 是否同步；服务依赖不同于包版本依赖。
5. 父实例是否已经发布。可选子实例等待迟到 provider 是允许的，父初始化不能等待自己的子树。

给 `Wait` 设置合理的 context deadline。取消等待不撤销已登记实例；决定不再启用时显式 Stop / Disable，而不是丢掉 handle。

## Native / WASM 启动失败

**`executable path must be absolute`**：WASM worker 路径必须是绝对路径。POSIX 使用 `"$(pwd)/bin/cordis-worker"`；PowerShell 使用 `(Resolve-Path ./bin/cordis-worker.exe).Path`。框架不从 PATH 查找 worker。

**WASM 无法初始化**：确认产物由 `GOOS=wasip1 GOARCH=wasm go build` 构建，存在 command `_start`，没有非 WASI import、imported memory 或 `sock_*`。Native 二进制改扩展名不是 WASM。模块大小、内存、编译和启动限额见[运行时参考](runtimes.md)。

**协议坏帧或启动后立即退出**：检查插件是否往 stdout 写日志、是否直接运行了非 SDK 程序、是否把诊断文本混入 JSON。stdout 只允许 NDJSON；日志写 stderr，不能在 handler 内 `os.Exit` 作为正常返回。

**Native 看不到环境或附带文件**：默认不继承宿主环境，cwd 是私有执行目录，只复制精确 artifact。不要依赖宿主 cwd 或包内旁路文件。确有需要的环境由宿主明确白名单传入；文件数据宜走能力代理。Native 本身仍可使用当前用户的系统权限，默认环境为空不等于沙箱。

## 响应过大，但插件仍可用

普通结果或错误消息的完整编码帧超限时，调用收到 `plugin_failed`，固定说明为 `response exceeds frame byte limit`；会话和其他调用继续。不要解析 message 自动重试：业务写入可能已经完成，先查询实际结果。

核对 Host 的显式 `FrameBytes` 与 Registration 确认值，范围是 4 KiB–1 MiB，缺字段和零值不回退到默认值。预算包含 JSON 信封、转义与 LF；例如 `<` 可以编码成六字节 `\u003c`。分页大小为 1 也不能保证单条大记录装得下，需使用业务投影、摘要或独立读取，而不是截断 JSON。

若 initialize/注册表超限，初始化失败且不发布。若已收到坏帧或写入失败，仍会关闭会话；该路径仅保证触发清理并汇总失败，不能把断链后的远端释放报告为成功。Native/WASI 的预算与运行说明见[运行时参考](runtimes.md)。

## `denied` 与宿主调用失败

按 Manifest 声明 → `Options.Authorize` grant → Broker 注册 → scope handler 四层检查。四层缺一不可；grant 存在也不代表输入在允许范围内。

身份从当前 Scope / 会话绑定取得，不从请求中解析。实例更新会更换 activation，旧身份和旧 grant 不可复用。撤权期间 handler 可能已经执行了外部 I/O；框架拒绝迟到结果，不承诺撤销外部写入。

namespace 还要核对 package → namespace 绑定和当前 owner。缺 key 返回 `ErrNotFound`，不是授权失败；不要把两个错误都处理成空值。

## `reentrant_call`

常见来源有同步 A → B → A、初始化等待自己的子树、middleware 对同一调用执行了两次 next。等待图同时检查独立根调用形成的环，并非只有同一 goroutine 才会触发。

把可选依赖拆到子定义，或调整服务职责、调用方向。不要用新的 goroutine、Background context 或异步等待掩盖环；这些做法会丢失调用身份、取消或受管生命周期。

## 调用超时，但操作似乎完成了

context 取消只能阻止后续准入和结果提交，不能回滚已写入的数据库、文件或远端服务。先通过业务 idempotency key / 操作记录查询实际结果，再决定是否重试。框架不会自动重放业务请求。

`Instance.Update` 也有提交与等待之分。返回超时后看 `Status.Desired` / `Applied`，不要认为配置完全没变。较新更新可能取代尚未应用的旧 revision；检查 UpdateResult，而不只看是否返回 nil。

## 停止超时或实例停在 quiescing

进程内 Start、handler、cleanup 必须合作响应取消。Go 无法安全强杀任意 callback；blocked 错误不代表资源已经释放，provider 的依赖资源会继续保留。清理不共用固定全局槽位，无关实例可以继续清理，但 `Host.Close` 仍等待自身拥有的全部资源。检查自己创建的 goroutine 是否有退出条件、Defer 是否等待它们、清理是否复用了已取消的业务 ctx。

外部 Runtime 可以终止受管进程，但 Native 自行脱离进程组的后代不属于权限沙箱保证。用独立、有期限的清理 context，记录实际失败，不忽略 Close 错误后声称完成。

## 提交与恢复错误

| 错误类型 | 已知状态 | 不要做什么 |
| --- | --- | --- |
| `*packagehost.CommitError` | catalog 或 runs 根已替换，后续同步/清理失败 | 不当成“操作没发生”盲目重试或恢复旧代码 |
| `*access.CommittedError` | namespace 记录已经提交，后续持久化步骤失败 | 不无条件重复业务写入 |
| `*access.RecoveryError` | 当前 namespace store 无法安全继续 | 不切到内存模式，不忽略损坏继续写 |
| `access.ErrQuota` / `ErrTaskQuota` | 该操作被配额拒绝 | 不报告成功；查看值大小和保留结果，宿主终态任务可 Release |

用 `errors.As` 识别带包装的错误，不匹配英文字符串。确认业务数据迁移后必须立即 `ConfirmDataVersion`；即使后续选择失败，旧代码也可能已被数据版本下界禁止执行。

恢复时 `catalog.json` 是包状态依据，`runs.json` 记录执行历史。不要用 staging、catalog.next 或目录中存在的某版本猜测提交状态。具体步骤见[恢复指南](recovery.md)和[上线维护](operations.md)。

## 稳定错误码

| code | 常见含义 |
| --- | --- |
| `invalid_message` | JSON、字段、数据类型、schema 或请求参数无效 |
| `incompatible` | 协议或能力不支持，或 API 不匹配 |
| `not_ready` | 服务、实例或资源尚不可用 |
| `denied` | 身份、能力、范围或宿主确认拒绝 |
| `timeout` / `canceled` | deadline 到期或取消；不表示副作用已撤销 |
| `reentrant_call` | 等待形成环，或 middleware 重复 next |
| `conflict` | 重复发布、重复任务、终态重提、版本或维护状态冲突 |
| `superseded` | 请求被较新的目标取代 |
| `plugin_failed` | 插件失败或对外投影的业务失败 |
| `internal` | 宿主内部错误的安全投影；原始原因只留给宿主 |

## 提交问题报告

提供最小复现、实际 Go / OS / Arch、Cordis 版本或 commit、已脱敏 Manifest、包引用、稳定错误码及失败阶段。说明 Native 还是 WASM、是否用了自定义 ABI、是否可在新临时 Store 复现。不要上传私钥、完整用户数据、原始 credential 或未经脱敏的故障目录。
