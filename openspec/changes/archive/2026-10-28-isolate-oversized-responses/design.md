# Design

## Context

动机见 [proposal.md](proposal.md)。诊断基线为 `0fa22bc6ba8210c8c9ae24d15be5a3855686041a` 中的通用 wire/SDK；本次规划不修改并行文档工作，也不将诊断记为修复完成。

- `wire/frame.go` 在 `json.Marshal(Frame)` 后以 `len(data)+1` 检查上限；超限时尚未进入 `out` 或 `Write`。
- `Endpoint.Call` 对本地请求编码失败删除 pending 并直接返回，但 `Endpoint.handle` 对所有 response send 错误调用 shutdown。
- `writeLoop` 与 `readLoop` 已分别承担实际写入失败和接收协议失败；复用它们，不创建另一条 IO 通道。
- 2026-10-27、darwin/arm64、Go 1.27.3 的临时真实 net.Pipe/SDK 测试确认：默认 1 MiB 下两个响应方向都会令其他调用收到 EOF；超限响应写出字节为 0。期望存活测试以 race 重复三次均失败；现有 wire/race 测试通过。
- 同一诊断以 1 KiB 预算验证 1024/1025 字节边界、202 字节原始 JSON 转义成 1320 字节完整帧、超大错误 message、128 字节可配置下限不足以承载测试中的 186 字节小错误。数字只描述该 fixture，不是通用信封预留值。

`build-go-plugin-framework` 的“有界且可验证的消息”保护接收端；本 delta 保留它并区分未发送的本地结果。基线尚在 changes 中，实施前重读最新 requirement；同步主规格时按基线、本 change、预算协商 change 的顺序处理，不用新 delta 覆盖其他新增场景。

## Goals / Non-Goals

**Goals:**

- 只隔离可以证明未改变字节流的尺寸失败；同一响应关联至多有一个交付结果。
- 在现有有界队列、deadline 和取消监督下工作，错误和清理仍可观察。
- 复用当前帧和合适的错误码完成第一步修复；直接改变未发布契约，不引入版本分支或兼容旧行为的开关。

**Non-Goals:**

- 不在本步骤协商限额；不把任意 malformed payload、第三方 Session 或生命周期失败转为可恢复。
- 不改变 handler 的串行规则、资源归属、任务终态和副作用语义。
- 不宣称 `json.Marshal` 只使用 FrameBytes 大小的内存；本修复不是任意大对象的内存配额系统。

## Decisions

### 1. 尺寸分类留在唯一编码路径

在 `wire` 内部为 encodeFrame 的实际长度拒绝提供可识别的错误类型或 sentinel；保留对外本地请求错误的既有 `invalid_message` 语义。只有该本地编码标记可触发 response 替换，不能把远端 `contract.Invalid`、handler 自报错误或字符串相同的 I/O 错误当作证据。

encodeFrame 继续先完整编码，再检查包括 LF 的长度；send 继续只把已经通过检查的字节入队。writer 不认识业务 fallback，不对部分帧重新编码。

否决：捕获所有 send 错误后继续，因为它会隐藏已损坏的流；比较 `err.Error()`，因为同文案不能证明失败阶段；提高默认上限，因为任意上限仍有边界且扩大内存风险。

### 2. 普通响应只替换一次

普通响应定义为本端已经完成 initialize 的会话上，method 不为 initialize、health、shutdown、activation.publish、activation.retire 的响应。后两项生命周期控制由并行 `build-go-plugin-framework` 定义，本 change 不重定义发布时点。成功 payload 或 coded error message 编码超限后，用原 activation/ID/method 构造无 payload 的错误：

```json
{"code":"plugin_failed","message":"response exceeds frame byte limit"}
```

线上不新增错误码或 details。该固定说明用于人类定位，不是稳定的机器判别接口；调用方按失败处理，不解析 message 自动重试。其本地原因可交给并行诊断设施记录安全分类和字节计数，但不要求本 change 新建 sink，也不附原 payload/message。

替代错误复用同一个 delivery context 和 out 队列，仅尝试一次；编码或准入失败走原 shutdown。原响应从未入队，所以不存在双响应；响应成功入队后，真正交付失败仍由 writeLoop 报告。正常替代成功不设置 Endpoint.Err、不撤销激活、不触发 supervisor。

### 3. 取消和生命周期不由 fallback 重定义

保留 handler 后的取消观察，并在可能耗时的编码之后、最终发送准入之前复查请求取消：已取消则选择现有短 canceled/timeout 错误，不交付尺寸错误覆盖取消。调用者的结果提交规则仍是最终保障，迟到错误按既有 canceled 表确认，不能留下无回应监督计时器。

initialize/health/shutdown/activation.publish/activation.retire 以及初始化前反向请求的超限保持终止。尤其不能在 initialize 注册表未交付时继续保持“可用”，不能把 shutdown 的失败替换成成功确认，也不能让退役失败静默继续受管后台工作。Host/SDK 仍通过当前启动失败、发布/退役和停止路径清理，不实现第二套生命周期状态机。

fatal 关闭后只能保证触发清理并报告实际结果：本地资源可按归属回收，依赖反向 RPC 的 disposer 可能因 transport 已断而失败。SDK/Host 汇总清理错误，不把“执行过 cleanup”写成远端释放成功或业务回滚；不得为掩盖该失败重开会话或重放释放请求。

### 4. 错误信封也受硬限制

不人为豁免小错误的大小。第一步仍需处理现实现允许的极小预算和过长关联字段：替代错误装不下时明确终止，不为了过渡状态增加兼容开关。下一 change 直接用当前契约的合法预算范围及关联字段上限替代旧配置规则；正常初始化完成后的合法请求不应再遇到这种尺寸不足。

接收侧仍在解码之前限长；不扫描/丢弃超大行再继续，因为无法可靠关联且可能被恶意无限输出占用。发送原始坏帧、连接中断、零进展写入和交付背压超时仍是 fatal。

### 5. 文档与行为回归一起交付

文档写清 `len(json.Marshal(Frame)) + 1 <= FrameBytes`，并区分业务对象、typed codec 输出与完整帧；嵌套 RawMessage 仍可能在外层编码时转义。分页由业务契约拥有；单条记录太大时使用摘要、字段选择或显式的独立读取/分块设计，不能默认认为存在通用分块 API。

回归使用真实 endpoint 对、真实 SDK 和独立 Native/WASI 作者包。Endpoint 层证明无关并发调用，SDK 层保留每实例业务串行，不为测试强行并发进入同实例。以 activation/进程身份未变化及后续调用成功验证没有被监督器偷偷重启掩盖。对于坏帧、背压和 partial write，用明确失败场景守住当前致命边界。

### 6. Ownership 与协调

| Owner | 写集与职责 | 依赖 |
|---|---|---|
| Integrator / wire | `wire/frame.go`、`wire/session.go`、wire tests；唯一发送分类和取消交付规则 | `contract`，不反向依赖 SDK/Host |
| SDK/Runtime 验收 owner | SDK tests、Runtime integration fixtures、独立作者/Host smoke | 使用公开入口；不改 wire/contract |
| 文档 owner | protocol、plugin-author、troubleshooting、必要的回归说明 | 以实现验收为准，保留并行 codeCommit/codePaths |
| Integrator | 本 change 的规格、任务、验证记录及最终集成 | 与后续预算 change 串行修改 wire |

并行的配置/DX 优化负责类型、宿主管理、公开窄接口及诊断出口；本 change 不改其 OpenSpec 或 API。诊断投影即使归一化 message，也不能导致本地尺寸分类依赖原始错误文本。并行文档版本不得被本规划回写覆盖。

## Risks / Trade-offs

- [只更新一端导致验收失真] -> Host、SDK 和测试产物一并重建，不支持旧二进制，也不建立旧行为测试矩阵。
- [接收方限制更小] -> 仍可能关闭，交给独立预算协商 change；本 change 不虚报跨端保证。
- [普通失败码不能精确识别超限] -> 当前需求是显式失败与会话隔离，不需要新的公开错误类型；调用方不得解析 message 自动重试。
- [过大错误或过小预算] -> 固定小错误也检查，只尝试一次，明确 fatal，防止递归 fallback。
- [业务已经提交] -> 失败不等于回滚；测试计数与文档禁止自动重放。
- [fatal 后远端 disposer 无法完成] -> 触发并汇总清理失败，区分本地回收与未证实的远端释放，不报告虚假成功。
- [大结果造成内存峰值] -> 记录 Marshal 的已知边界，不以该修复冒称内存安全预算；作者仍需限制业务数据。

## Migration Plan

1. 在本 delta 通过独立评审后，先加入能复现当前双向失败的行为测试，再改唯一 wire 路径。
2. 同步当前 schema、SDK、Native/WASI 调用方、向量及文档；无关的签名算法和输入布局不变，记录实际运行平台。
3. 本步骤可独立合并和验收，但最终消息预算交付须完成下一 change；中间状态不宣称任意宿主预算均已支持。
4. 如需撤回，整组撤回相应契约及调用方改动，不保留双轨运行开关；不得通过自动重启/重试冒充修复。
5. 此后实施 `negotiate-session-frame-budgets`，复用本步骤分类与测试，不再建一套响应 fallback。
