# Tasks

各组按顺序执行。`verification.md` 由各任务共同追加。

## 1. 帧与流量控制

写集：`wire`、`schema/session.schema.json`、`contract` 的协议常量，以及 `docs/protocol.md`。

- [x] 1.1 增加数据项帧与额度帧、请求的流式标记与窗口、按“被调用方持有额度时的静默时间”计时的期限、不设期限上限的请求种类，以及 `streams`、`chains` 特性的协商。验证：`wire` 新增测试覆盖额度用尽时发送阻塞、额度帧恢复、消费者暂停不超时、被调用方静默超时、取消终止流、已取消请求上迟到的数据项被丢弃、越额或未知编号的数据项使会话失败、流结束后的额度帧被忽略、单项过大只失败该流、流占用与释放在途名额；`wire` 的全部测试通过；`python3 conformance/validate-schemas.py` 通过。
- [x] 1.2 在 `docs/protocol.md` 说明新帧、额度、期限规则和特性协商。验证：`bun run docs:build` 通过。

## 2. 流式契约

写集：`contract` 的端点接口、`schema` 的声明解析与生成器、`schema/contract.schema.json`、`docs/generated-service.md`、`docs/contracts.md`。

- [x] 2.1 `contract.Endpoint` 增加流式调用；作者声明支持方法的流式标记；生成器产出返回 `iter.Seq2` 的接口方法、客户端和服务端适配（含同进程直连）。验证：`go test ./schema/... ./contract/...` 通过；规格“同进程”场景有通过的测试；声明了流式方法的包其 manifest 把 `streams` 列为必需特性。
- [x] 2.2 在 `docs/generated-service.md` 与 `docs/contracts.md` 说明流式方法的声明与生成结果。验证：`bun run docs:examples` 与 `bun run docs:build` 通过。

## 3. 流的桥接

写集：`sdk`、`hosting` 的桥接部分。

- [x] 3.1 两侧桥接支持流式调用：转发端点、额度发放、取消、实例停止或进程结束时终止。验证：规格“流式服务方法”的全部场景在内存会话上各有通过的测试；规格“流与链的特性协商”中关于流的场景有通过的测试。

## 4. 中间件链

写集：`contract` 的链键、`wire` 的负载类型、`sdk`、`hosting` 的桥接部分。

- [x] 4.1 增加 `contract.ChainKey[In, Out]`（本进程路径）和链的协议方法；两侧桥接实现登记、带令牌的执行与 `next`、最终处理和取消登记。验证：规格“跨进程中间件链”的全部场景和“宿主不支持中间件链”场景在内存会话上各有通过的测试；与内核 `Chain` 相同的次序和选项有对照测试。

## 5. 示例、验收与作者文档

写集：`examples/plugins`、`conformance`、`cmd/cordis` 的集成测试、`docs/plugin-author.md`、`docs/composition.md`、`README.md`。

- [x] 5.1 增加一个提供流式方法的示例插件和一个登记跨进程中间件的示例插件，纳入 conformance 脚本。验证：`go test -tags=integration -race ./...` 通过；`bash conformance/run.sh` 在本机通过并记录平台。
- [x] 5.2 更新 `docs/plugin-author.md` 和 `docs/composition.md` 的对照表，说明流的额度、期限、在途名额、心跳项，以及中间件链每跳的开销和不设期限上限的含义。验证：规格 `framework-host-integration` 的“区分进程内与 native 的能力”场景可对照文档逐项核对；`bun run docs:examples` 与 `bun run docs:build` 通过。

## 6. 集成验收

- [x] 6.1 由非作者独立审查帧层的顺序规则、额度记账、迟到帧的处理和链的令牌关联。验证：结论与处理结果写入 `verification.md`，没有未处理的阻塞项。
- [x] 6.2 运行 `go vet -tags=integration ./...`、`bash conformance/run.sh`、`bun run docs:build`、`openspec validate add-wire-streams-and-chains --strict`，并在 CI 的五个平台实际运行 conformance。验证：`verification.md` 记录精确提交、本机平台输出和 CI 链接；未执行的平台明确写出。

## Workflow follow-up

- 同步 `framework-execution` 与 `framework-host-integration` 主规格后归档本 change。
- 下一个 change：`add-config-loader`。
