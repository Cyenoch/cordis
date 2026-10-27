---
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - wire
  - sdk
  - host_test.go
  - conformance/cmd/lifecycle-smoke
---

# 生命周期回归覆盖

这些测试检查生命周期行为：协议场景使用 `net.Pipe`，SDK 场景运行真实协议循环，宿主场景使用公开 builtin 接口和包存储。它们不启动真实外部进程，Native/WASM 执行另由[生命周期验收](lifecycle-acceptance.md)覆盖。

## 会话响应竞态

`wire/lifecycle_test.go` 使用 `testing/synctest` 覆盖两个时序：

- 调用者观察到取消时，响应已经匹配。该调用返回取消，但不能给已响应请求新增取消 tombstone；取消宽限期后，同会话后续 health 请求仍成功。
- initialize 响应后立即到达 guest 请求，而初始化调用者 goroutine 尚未恢复。宿主必须接收并响应 guest 请求，不能等待初始化调用者恢复才推进。

第一个场景只用包私有响应 channel 的调度强制原本不确定的 select 时序。两者都通过 `net.Pipe` 发送真实协议帧并断言可观察响应，不使用生产测试 hook 或源码文本断言。

VortexGo 专有会话还区分退役与清理确认：只有 pending ID、方法及 ABI 已匹配的 `plugin.shutdown` 响应可在退役后完成；未知、重复、错配响应及其他旧激活消息仍拒绝。这部分由宿主仓库维护，不是通用 SDK 的方法名，也不扩大普通请求的授权。

## SDK 启动展开与清理

`sdk/lifecycle_test.go` 取消在途初始化，在 `Start` 仍处理取消、尚未返回时请求 shutdown；另一路径先让 shutdown 达到 deadline，再释放 Start。

- 资源清理不能与 Start 重叠，即使 shutdown 已超时。
- 展开期间迟到取得的资源仍可登记 cleanup。
- 全部登记资源只按逆序释放一次。
- Start 迟到成功不能重新激活已关闭 client、重新注册初始化或执行服务 handler。

transport 保持存活足够长，以区分“资源已排空”和“成功发布”。SDK 使用标准 Go goroutine，不要求手动 Pump；真实 WASI 中等待宿主响应时取消/控制仍能推进，需要 worker 保留支持非阻塞读取和 `poll_oneoff` 的原始文件 stdin。

## 公开 Host 生命周期

`host_test.go` 约束：

- builtin cleanup 失败时，`Host.Close` 仍排空清理并释放 Store 独占锁，再重开同一目录验证锁已经释放；Close 保留错误，不伪报成功。
- builtin handler 在调用 context 取消后迟到返回成功，不能向调用者提交该成功；同一 activation 上的新调用仍可成功。

停止后外部实例更新不可落盘，由真实签名包 lifecycle smoke 验证，而非 fake Runtime 或手工组装 facade。它对比拒绝更新前后的 durable catalog 配置。该真实链还覆盖 First 的 handled 零值、整棵父子子树 Disable 后的持久状态、父子环在保存前拒绝、初始化失败后 Disable 报错但同 ID 能重新启用、替换/重开 Restore 和有限监督。普通单测不替代这些 native/WASM 实际进程场景。

## 执行证据与边界

集成负责人统一执行单测、集成、race 和真实外部 smoke；实际结果、对应源码修订及目标平台输出见 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md)。本文描述测试职责和复现范围，不另外宣称所有最新修改或五目标矩阵已通过。旧修订结果、交叉编译和未执行目标不能提升为当前通过；这些回归也不代表产品 UI、游戏事务或发行认证。


## 单源配置与管理边界

`config/*_test.go` 约束严格格式选择、来源映射、精确数字及合法/冲突 TOML 表；`schema/references_test.go` 约束同一声明图的引用展开预算，防止小输入反复复制大描述。生成代码由独立 module 编译并实际编码，包含空 `json.Number` 拒绝、两次 CLI 生成同包、consumer 方法身份与显式 minor 范围。

Host 的管理回归覆盖更新取代旧 revision、Stop 不被更新等待阻塞、cascade 替换预留完整目标树、提交前取消 Close 仍可接纳新操作，以及 Disable 已持久提交后等待取消仍保留 CommitError 且不能被 Resume 撤销。`host_lifecycle_test.go` 的 custom PluginSession 验证发布确认门槛和退役清理映射；`host_dx_integration_test.go` 则真实运行 Native/WASI，约束预检早于持久/退役、managed work 不占业务位且先退出再清理，以及 opt-in stderr 的脱敏与可观察失败。

这些当前测试与执行结果对应 [OpenSpec 第 15 组](../openspec/changes/build-go-plugin-framework/tasks.md)，不能用旧文档中的历史平台记录代替本轮验证。
