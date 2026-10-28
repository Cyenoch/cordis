# Cordis 教学资源

## Knowledge

- [仓库文档：`docs/contracts.md`](../docs/contracts.md)
  数据契约、受限 schema、生成器、严格编解码、typed client/server。用在哪：类型边界、生成期与编解码期闸门。
- [仓库文档：`docs/composition.md`](../docs/composition.md)
  宿主入口、realm、Requires/Provides 发布语义、更新收敛、父子所有权、事件与 middleware。用在哪：依赖三层、生命周期问题。
- [仓库文档：`docs/packages.md`](../docs/packages.md)
  manifest 字段、Pack/Sign、信任策略、版本区间（`[Min,Max)` 与比较器）、catalog 与快照。用在哪：包层依赖、安装/选择、信任。
- [仓库文档：`docs/lifecycle-acceptance.md`](../docs/lifecycle-acceptance.md)
  19 条断言的可观察含义与复现命令。用在哪：把行为预期变成验收清单。
- [源码即规范：`contract/`](../contract)
  `data.go`（CheckDataType）、`json.go`（Decode/Encode）、`types.go`（身份/状态/错误码）、`instance.go`。用在哪：任何“真的会失败吗”的问题。
- [可执行规范：`schema/testdata/echo.schema.json`](../schema/testdata/echo.schema.json) 与 `go test -tags=integration ./schema`
  生成器的端到端样本（nullable、optional、int64、base64、union、严格 envelope）。用在哪：类型安全的具体落地。
- [依赖链样本：`examples/plugins/chain/main.go`](../examples/plugins/chain/main.go)
  一个产物三个定义（provider/middle/consumer）+ 作者子实例。用在哪：服务依赖、晚到 provider、子实例。
- [宿主样本：`examples/basic-host/main.go`](../examples/basic-host/main.go)
  安装/选择/启用/回调/事件/换版本/卸载的完整顺序。用在哪：宿主侧所有课程的参照实现。
- [Go 规范：泛型](https://go.dev/ref/spec#Type_parameter_declarations)
  `sdk.Provide[In, Out]` 的类型参数语义。用在哪：解释“编译期类型”只覆盖哪一半。
- [`encoding/json` 文档](https://pkg.go.dev/encoding/json)
  `DisallowUnknownFields`、数字解码到 `interface{}` 变 `float64`。用在哪：为什么 int64 必须走字符串 codec。
- [JSON Schema 核心规范](https://json-schema.org/specification)
  Cordis 采用的受限子集与拒绝项（`$ref`、`allOf`、隐式默认值）的对照背景。

## Wisdom (Communities)

- [上游 cordiverse/cordis（GitHub）](https://github.com/cordiverse/cordis)
  组合语义的来源；适合对照“为什么要 scope/cascade”，不适合当成 Go 实现答案。
- [Go 官方论坛 golang-nuts](https://groups.google.com/g/golang-nuts)
  插件、进程边界、cgo/WASM 一类设计问题的真实工程反馈。
- [r/golang](https://reddit.com/r/golang)
  中等信号，提问“如何设计插件边界”时能拿到多种工程取舍；筛掉框架推荐类回答。
- 本仓库 issue / OpenSpec change（`openspec/changes/archive/2026-10-28-build-go-plugin-framework/`）
  唯一能拿到“设计者本人怎么想”的渠道；也是用户未来贡献断言的入口。

## Gaps

- 尚无第三方对 Cordis 的独立评测或生产案例；所有结论目前只能来自本仓库的代码与 conformance 证据。
- 没有现成的“宿主集成”教程视频或文章；课程中的宿主示例需以 `examples/basic-host` 为准。
- 尚未找到讨论“Go 插件框架类型边界”的高信号长文；后续可补充 Go 官方 blog 的插件相关文章。
