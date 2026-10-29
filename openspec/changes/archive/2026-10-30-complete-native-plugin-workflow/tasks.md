# Tasks

实施按 1–2、3–4、5–6 三批交付；每批通过自身测试后继续，7 验证整体集成。规范为 `specs/`，技术取舍为 `design.md`，实际测试与审查证据见 `verification.md`。

Integrator 独占 `contract/`、schema、go.mod/go.sum、SDK 能力声明及 OpenSpec。以下模块组按顺序移交，根 Host 的共享文件不并行修改；若使用其他 Agent，只分配当时无交叉的模块写集。各组文档拥有者同样顺序移交，不创建第二套架构记录。

## 1. 归属诊断与终态记录

- [x] 1.1 Integrator 固定退出摘要、身份化 stderr、有限历史字段和可选 slog 出口的公共契约，更新 `contract/`、持久记录格式及对应文档；由非作者确认默认无业务 payload、退出原因与清理结果分离，产出具体审查结论。
- [x] 1.2 修改 `runtime/internal/process/` 和 `runtime/native/` 在实际退出/终止处采集摘要；用真实 native 测试验证意外 exit 0、非零退出、主动停止、宽限期强杀、平台无 signal、stderr 截断及持续排空。
- [x] 1.3 在 `internal/core/` 和根 Host 记录状态变化、启动耗时、失败调用并保留最近 activation；验证两个同包实例错误归属、未发布失败、正常取消与失败区分、可选 builtin panic 栈，测试 handler 不在内部锁内执行。
- [x] 1.4 在 `packagehost/` 与 Host 终态桥接中完善 RunRecord 和管理结果诊断；验证重开后退出摘要可读、清理失败分类不丢失、多次大错误仍可在既有字节预算内重开、提交后错误不被报告为未提交；不新增日志历史表。
- [x] 1.5 更新 `docs/operations.md`、`docs/troubleshooting.md`、`docs/api.md` 的诊断入口和内容边界；通过一个实际初始化失败/崩溃示例验证读者可从 entry 定位 activation。

## 2. 动态子实例与关闭完成

- [x] 2.1 Integrator 增补实例来源、父激活归属和 Host 关闭完成信号的公共契约；更新 API 文档并由非作者检查旧句柄与持久意图边界。
- [x] 2.2 修改 `children.go`、`management.go`、`host.go` 的统一查找及动态句柄；验证多级 child 枚举/状态、Stop 仅影响目标子树、Update 不写 catalog、父重建后旧句柄不能控制新实例。
- [x] 2.3 调整退役查询元数据的移除时机；用阻塞清理场景验证 Quiescing child 仍可查询，完成后才消失，正常父子清理及并发创建规则保持。
- [x] 2.4 复用 core 排空事实实现 Host 完成信号；验证 Close 带历史错误但完成、超时未完成、后续继续关闭和目录锁释放，不能只用 Close 返回值决定完成。
- [x] 2.5 更新 `docs/host-guide.md`、`docs/recovery.md` 的动态子树查询、控制和关闭示例；执行示例并验证重开恢复与同 Host 重复 Restore 的差异。

## 3. 离线包管理与前台 Host

- [x] 3.1 Integrator 与 `packagehost/` 写集实现最小无执行意图编辑接口，复用现有验证与原子提交；验证非法配置不提交、父子图拒绝、子树原子停用、只移除已停用叶 entry、启用反向依赖不能被空 InUse 绕过。
- [x] 3.2 在 `cmd/cordis/` 添加明确 store/trust/ref 解析及 list/install/select/enable/disable/remove/uninstall/status；复用标准库 flags 和配置读取，用真实目录验证锁冲突、未授权签名、精确包引用、enable 保留未指定字段/显式清空、不兼容 schema 经 remove 切换、输出失败后的提交状态及 0/1/2 退出码。
- [x] 3.3 实现前台 `host`，使用现有 Restore/Close 和新诊断；验证持续运行、部分实例 Waiting/Failed、信号关闭后无遗留 native 进程、保留启用意图、Restore 条目错误实际出现在输出。
- [x] 3.4 在 `docs/cli.md`、`docs/getting-started.md` 和现有教程验收中加入签名→离线安装启用→前台运行→退出查询流程；证明无需 Go 宿主代码，且 enable/status 不谎报当前 Active。

## 4. 单包开发循环

- [x] 4.1 在 `cmd/cordis/` 实现单入口、单一本机 target 的 dev 参数和输入监视，复用 Build；验证连续编辑合并、资源/config 文件触发、显式外部 watch、工具产物不自循环、多目标声明明确拒绝。
- [x] 4.2 实现源码目录外 ZIP/候选 Store、精确摘要许可及无执行静态预检；验证同发布版本连续修改可开发、正式 Store 仍拒绝同版本不同摘要、编译/Pack/配置失败保留旧 activation。
- [x] 4.3 实现串行 generation 切换和关闭完成判定；真实 native 测试覆盖关闭期间新编辑不启动过期候选、初始化失败后修复可继续、配置快照一致、关闭带错误但完成可继续、未排空不得启动下一候选。
- [x] 4.4 验证取消会结束监视、go build 和本次进程，完成关闭后清理临时目录；通过现有测试设施复用同步信号，不以固定 sleep 代替生命周期证据。
- [x] 4.5 更新 `docs/plugin-author.md`、`docs/cli.md` 的 dev 用法与限制，增加可执行编辑重启示例；说明源码构建不是整体事务、Pack 输入范围、临时目录布局和切换后的失败边界。

## 5. 按服务 key 隔离

- [x] 5.1 Integrator 添加 entry/child 隔离声明、Host 解析视图及 schema 契约，同步作者/生成边界；由非作者审查仅隔离指定 key、不自动隔离父子、无权限沙箱承诺与静态恢复语义。
- [x] 5.2 在 `internal/core/` 统一 provider 索引、绑定、冲突、Waiting 诊断及激活依赖环的作用域解析；验证两组同名服务共享公共服务、边界内缺失不外退、嵌套覆盖、major 分离及同域重复冲突。
- [x] 5.3 在根 Host、`children.go`、`packagehost/` 和标准会话 child 消息连接隔离声明；验证静态 Restore、动态同包继承、根/指定实例 Caller、旧视图失效及旧绑定清理，不引入任意 namespace 选择。
- [x] 5.4 扩展真实 native 组合示例和 `docs/composition-tutorial.md`、`docs/composition.md`、`docs/concepts.md`；执行隔离 provider 更新/停止只影响本组的回归，并在教程明确事件仍广播。

## 6. 可取消的动态事件订阅

- [x] 6.1 Integrator 固定唯一 On/typed Subscribe 契约、取消句柄/options、逐监听者投递、控制消息及能力/schema；非作者审查注册确认、取消失败、once 准入与实例退役的顺序，不保留旧包装。
- [x] 6.2 在 `internal/core/` 实现逐监听者动态登记、取消、Once/Prepend，保持三个 dispatch 和环检测；验证初始化失败不发布、发布后可登记、自取消不死锁、并发 once 最多一次、Handled=false 值不混淆。
- [x] 6.3 在 `sdk/`、`session.go`、Host 桥及 `wire/` 连接同一会话控制路径；真实 builtin/native 混合测试验证逐监听者结果、取消确认与已准入投递的发送/接收次序、once 与自取消竞争、重复取消及最终引用释放；覆盖退役后迟到登记、传输不确定时无残留订阅、活动上限及现有 frame/inflight 边界。
- [x] 6.4 同步调用者、测试 harness 和示例到新订阅契约，更新 `docs/api.md`、`docs/protocol.md`、`docs/resources.md`；执行取消句柄和退役清理的可运行示例，不把本地 Chain 标为跨进程 waterfall。

## 7. 整体验收与公开边界

- [x] 7.1 在现有 `conformance/` 中整合三批端到端场景，保持 POSIX/Windows 同范围；通过 `go test -tags=integration -race ./...`、`go vet -tags=integration ./...`、`go mod verify`，复核已存在的替换预检/取消/清理契约。
- [x] 7.2 对最终待交付提交执行既有五平台 workflow，检查每个平台真实 native 安装、退出、更新、child、事件与 dev 场景成功；在本 change 记录 SHA、run/job 链接、失败或未执行范围，不复用基线 run 充当本轮证据。
- [x] 7.3 由非作者审查公共契约和实现边界，尤其新取消准入、作用域环检测、已排空但带错误及静态/动态身份；修正具体发现后运行相关验证，保存审查与修正结论。
- [x] 7.4 汇总 README/文档站中的已实现能力及明确非目标，更新 ROADMAP 阶段入口；通过 `bun run docs:test`、`bun run docs:examples`、`bun run docs:build`、`bun run spec:validate`，确认 GitHub Pages 导航只宣传实际完成能力。

## Workflow follow-up

- 实施和审查完成后再同步主规格并归档本 change；不修改旧归档以掩盖取舍历史。
- 发布稳定版本、创建 tag、发布 CLI 二进制或插件市场仍需单独发布任务；本 change 不隐含外部发布授权。
