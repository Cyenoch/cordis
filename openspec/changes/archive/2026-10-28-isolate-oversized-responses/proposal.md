# Proposal

## Why

当前通用 wire 在本地编码 handler 响应时发现帧超限，尚未写出任何字节，却关闭整个 endpoint，使当前调用、无关在途调用及后续调用一起失败。应在稳定发布前把可定位的结果尺寸错误限制在当前调用，同时保留接收违规帧和传输故障的会话级终止行为。

## What Changes

- 区分本地完整帧编码超限与 JSON 编码失败、I/O 失败、背压超时；仅前者进入本 change 的响应替换路径，不按错误文本或宽泛 `invalid_message` 判定。
- 已初始化会话的普通请求返回过大成功结果或过大错误消息时，放弃原响应，最多发送一次同关联的固定小错误；其他在途调用、后续调用和 activation 不因该尺寸错误被终止。
- 本地请求超限仍不发送、不调用远端 handler；取消、初始化、shutdown、无法发送最小错误及真实违规帧保留明确的独立规则。
- 明确帧预算按最终 JSON 信封的 UTF-8 字节加末尾 LF 计算；补齐转义、单条结果超限、分页不等于字节限制及禁止隐式重试的作者说明与行为回归。
- 复用语义合适的 `plugin_failed` 错误码和固定安全说明；不增加版本、feature 或旧行为兼容路径。类型、schema、调用方及向量按当前契约同步，不为冻结旧 v1 字节阻碍修改。

项目尚未上线或稳定发布，本次按用户最新决定直接修改当前契约，覆盖此前“冻结旧 v1 字段语义”的规划约束。签名算法和输入布局不涉及此问题，保持不动；不承诺与旧二进制互操作。

本 change 仅承诺发送端在本地预算内可生成并交付替代错误的路径。双方预算不一致由后续 `negotiate-session-frame-budgets` 解决，不把本修复宣称为任意限额组合下的会话存活保证。

非目标：自动截断结果、自动分页/分块、流式 RPC、自动重试、业务回滚、改写宿主专有 Session、抑制传输故障，以及把所有本地编码错误都变成可恢复错误。该取舍来自本仓库已复现的失败，不是上游 TypeScript Cordis 的 wire 要求。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-execution`：补充发送前尺寸失败的调用级隔离、完整帧预算和错误/生命周期边界；澄清“有界且可验证的消息”及“执行失败与重试控制”，保留收到超限帧时关闭会话的既有场景。

当前主规格目录尚无已同步 capability；上述名称沿用 `build-go-plugin-framework/specs/framework-execution/spec.md`，不另建同义 capability。后续同步或归档须先建立该基线，再应用本 delta；本次不修改或归档前置 change。

## Impact

- 实现落点：`wire/frame.go`、`wire/session.go` 及 wire 行为测试；SDK/Host/Runtime 验收覆盖真实独立 Native/WASI 插件，但不引入另一套发送器。
- 文档落点：`docs/protocol.md`、`docs/plugin-author.md`、`docs/troubleshooting.md`，由后续实施者基于并行文档工作的最新内容增量修改；保留代码基线元数据，不提前写成已实现。
- 不新增依赖，不修改包格式、catalog、`go.mod`、`go.sum` 或只读参考 submodule。使用自有协议的宿主仍负责自己的编码和失败策略。
- 实施先于 `negotiate-session-frame-budgets`；两个 change 的 wire 写集不得并行合并。公开可观察的会话存活行为及错误语义须由非作者独立审查。
