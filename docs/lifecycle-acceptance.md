# 外部插件生命周期验收

`examples/plugins/chain` 是独立作者 module，同一产物通过 `sdk.ServeDefinitions` 提供三个定义：provider A、依赖 A 的 middle B、依赖 B 的 consumer C。每个服务提供 major 1、minor 2，每个 requirement 接受 minor 闭区间 `[1,3]`，刻意验证非零 minor 的范围匹配，而不是只验证精确版本。

验收使用真实签名 ZIP 和独立进程会话，不用 mock Runtime、宿主、guest 或 WASI worker 代替。native 是受信任可执行代码，不是操作系统权限沙箱。

## 执行

从 Cordis module 目录使用满足 `go.mod` 的 Go 工具链执行；作者 module 独立构建。WASM 是 `wasip1/wasm` command，不是把 native 产物改名。

```sh
mkdir -p /tmp/cordis-lifecycle-bin
(cd examples/plugins/chain && go build -o /tmp/cordis-lifecycle-bin/chain .)
(cd examples/plugins/chain && GOOS=wasip1 GOARCH=wasm go build -o /tmp/cordis-lifecycle-bin/chain.wasm .)
(cd examples/plugins/chain && go build -ldflags '-X main.rejectInitialization=true' -o /tmp/cordis-lifecycle-bin/chain-bad .)
(cd examples/plugins/chain && GOOS=wasip1 GOARCH=wasm go build -ldflags '-X main.rejectInitialization=true' -o /tmp/cordis-lifecycle-bin/chain-bad.wasm .)
go build -o /tmp/cordis-lifecycle-bin/cordis-worker ./cmd/cordis-worker
go run ./conformance/cmd/lifecycle-smoke -runtime native -guest /tmp/cordis-lifecycle-bin/chain -bad-guest /tmp/cordis-lifecycle-bin/chain-bad
go run ./conformance/cmd/lifecycle-smoke -runtime wasm -guest /tmp/cordis-lifecycle-bin/chain.wasm -bad-guest /tmp/cordis-lifecycle-bin/chain-bad.wasm -worker /tmp/cordis-lifecycle-bin/cordis-worker
```

这些是复现命令；集成负责人已经执行的 native/WASM 链、单测及 race 结果统一见 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md)，按记录的源码修订和实际平台判断。POSIX 示例不代表 Windows 运行证据；Windows 使用本机 `.exe` 和对应路径。最终五目标矩阵完成前，不把旧修订、交叉编译或未执行目标标为通过。

每次调用有三分钟总期限。命令创建临时 catalog、随机 Ed25519 密钥及 `example.chain` 的三个签名版本；1.2.0 使用独立构建且明确拒绝初始化的产物，trust 绑定精确 publisher/package ID。退出时清理临时包与 catalog。

## 断言与可观察证据

任何未满足断言都导致非零退出。只有全部场景和 Host 关闭完成后才输出一条 JSON：`assertions` 列出完成项，`observations` 保留成功的真实协议往返，`faultClose` 记录故意崩溃的 Host 在关闭时返回的预期故障。

- **晚到 provider**：先启用 C，再 B，最后 A。A 到达前 B/C 必须等待；完整 C → B → A 的统计调用必须显示两个存活资源。
- **版本范围**：启动资源获取及后续链式调用必须在提供 minor 2、要求 `[1,3]` 时成功。包 SemVer comparator range 与此服务 minor 闭区间不同，见[包管理](packages.md)。
- **First 的有效零值**：真实外部事件的首个监听者返回 `Handled:true, Value:0`，First 必须立即停止并保留零值；Sequential 必须继续到第二个监听者并观察其错误，不能以零值当作未处理。
- **父子图拒绝不落盘**：自指/成环 entry 在保存 catalog 前拒绝，拒绝的 Enable 不得毒化持久父子图。
- **调用中取消与控制推进**：C → B → A 的 `wait` 在 A 以 activation context 创建 SDK task；宿主观察到任务 running 后才取消业务调用。创建确认不依赖即将取消的业务 parent；即使取消先于 guest 收到确认，A 仍须通过独立 `task.finish` 控制消息提交 `{"canceled":true}`。调用者取得取消，不靠固定 sleep 猜测调用是否开始。
- **传递清理**：B 在 A 中取得资源，C 经 B 在 A 中再取得资源。C 的 Defer 通过 B → A 释放，B 原样传递 cleanup context。C 校验释放后的计数，B 确认消费者资源清完后才释放自身资源，A 的 Defer 拒绝未匹配获取。每个 callback 都返回自己的错误，不吞掉失败。
- **可选依赖诊断**：可选包缺失不阻止启用，预检诊断通过公开 `Status.Diagnostics` 可观察；不能因为非致命诊断而丢失运行状态，也不能静默丢弃诊断。
- **释放响应与 shutdown 顺序**：观察 session wrapper 将全部 transport 与方法处理委托给真实 `wire.Endpoint`，只记录成功 release 和 shutdown 响应，不授权、不伪造结果。必须依次观察到 C release、C shutdown 确认、B release、B shutdown 确认、A shutdown 确认。清理阶段不用被有意拒绝的宿主 capability callback 绕过绑定。
- **替换**：C 先登记时，将已选签名包替换为 `1.1.0`。C 必须取得新的 activation 和 selected digest，旧 activation 权限撤销，服务调用仍正常。
- **父子实例**：独立隔离 realm 中 A 是 B 的父 entry，B 是 C 的父 entry，ID 排序与启动依赖顺序相反。initialize 确认必须 A → B → C，成功清理证据必须逆序；该 realm 同时参与替换和恢复。
- **整棵子树持久停用**：Disable 父 entry 后，所有后代持久 entry 都必须 disabled，重开 Restore 不得意外恢复子树。
- **builtin 子实例**：外部父实例下添加 builtin，替换父包后 builtin 必须以新 activation 重建；停用父实例必须真正移除 builtin 注册，且不把 builtin 写入包 catalog。
- **配置持久化**：将根 A 的 label 更新为 `persisted`，替换包，关闭 Host，重开同一 catalog 后显式 Restore。C → B → A 仍返回该 label，选中包仍为 `1.1.0` 且 digest 与安装时一致。
- **停止后拒绝更新**：停用独立外部 provider 后更新必须拒绝，catalog 配置保持原值。
- **有限监督**：独立 catalog 中 A 以退出码 7 崩溃，只允许一次监督重启，再次崩溃必须耗尽预算并稳定在 failed，旧 identity 失去权限。监督不会重放业务调用。
- **意外正常退出**：另一个独立 provider 在调用中以退出码 0 退出，也必须 failed、撤权并拒绝后续服务；OS 正常退出不等于插件服务调用成功。
- **失败新代码恢复旧版本**：保持同一宿主和 entry 配置，1.0.0 可调用，替换为独立失败产物 1.2.0 后撤权并保留 failed 选择；显式 Replace 回 1.0.0 保留历史错误，同时恢复 Active、新 activation、原 digest 和可用服务，不重启宿主、不确认数据迁移。
- **初始化失败后同 ID 恢复**：真实 guest 初始化失败，Wait 返回错误，Disable 保留历史启动错误；但完成清理后用同 entry ID 和有效配置重新 Enable 必须能启动和调用，不能被残留实例阻塞。

故障 catalog 有意保留历史失败，最终 Close 必须报告它们，并在关闭期限内排空；健康 catalog 必须无错误关闭。场景覆盖组合、持久化和生命周期，不代表 OS 隔离边界、资源耗尽、性能、真实产品 UI 或游戏部署认证。宿主专属协议、授权投影和领域 provider 的验收由宿主仓库独立维护。
