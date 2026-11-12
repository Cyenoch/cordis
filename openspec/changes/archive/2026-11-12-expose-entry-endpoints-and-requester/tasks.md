## 1. entry 视图的端点

- [x] 1.1 `hosting.Caller.Endpoint(required)` 解析并校验版本，`Caller.Call` 经由它；`Caller` 绑定取得时的登记，停用、移除、替换后以 `not_ready` 失败。验证：`hosting` 测试中同一包两个隔离 entry 的流式端点各自产生本 entry 的结果、停止读取取消 provider 的流、根作用域看不到它们；版本范围不符得到 incompatible；移除后重新启用，旧视图对隔离与未隔离服务都失败、新视图可用
- [x] 1.2 `docs/host-guide.md`、`docs/composition.md` 写明视图取端点交给生成的客户端及旧视图失效。验证：文档示例与测试中的调用方式一致

## 2. 请求方身份

- [x] 2.1 `hosting.Requester(ctx)`；`run.handle`、`run.stream` 在处理请求前标出激活身份。验证：`hosting` 测试中插件一元与流式调用宿主服务时处理者读到的身份与 entry 状态一致；同包两个隔离 entry 可区分；嵌套请求以直接发出者为请求方；应用自己的调用报告没有请求方
- [x] 2.2 `docs/host-guide.md`、`docs/kernel.md` 说明请求方只是信息、进程内调用报告没有请求方。验证：文档与测试行为一致

## 3. `parent` 与回调

- [x] 3.1 测试固定回调语义：宿主处理插件调用时用请求 ctx 回调该插件成功；改用无关 ctx 也成功；取消最外层调用时插件中的回调看到取消，会话继续。验证：`go test -race ./hosting`
- [x] 3.2 更正 `docs/protocol.md`（`parent` 段落与特性表）和 `docs/composition.md` 的环检测描述。验证：文档不再声称跨进程调用环检测或 `reentrant_call`

## 4. 集成验证

- [x] 4.1 `openspec validate expose-entry-endpoints-and-requester --strict`、`go vet ./...`、`go test -race ./hosting`、`go test ./...`、`bash conformance/run.sh`、`GOOS=windows GOARCH=amd64|arm64 go vet ./hosting`
- [x] 4.2 `CHANGELOG.md` 的“未发布”记录新增 API 与旧视图的行为更正
- [x] 4.3 独立审查
