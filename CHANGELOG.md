# 变更记录

版本与承诺范围见 [稳定性](docs/stability.md)。

## v0.5.0

- **行为变化**：包清单中各 definition 的 `configSchema` 改为共用一份引用展开预算（1 MiB 字节、10000 节点），与一份契约中的文档相同。此前每个配置 schema 各有一份：一个 1 MiB 的清单把一个约 2 KB 的放大 schema 重复 457 次，检查这个不受信任的包在 v0.4.0 上约 50 秒、累计分配约 88 GiB，现在约 0.1 秒后被拒绝（Apple M5 Pro 实测）。这比 v0.3.0 和 v0.4.0 都严格：每个配置 schema 单独都在预算内、合计超出的清单现在被拒绝，打包和安装时都会检查。改法：合计超出时缩小配置 schema，或不要让多个 definition 各自展开同一个大的 `$defs`。新增 `schema.NewBudget` 与 `Budget.Check`，供宿主让同一来源的多个 schema 共用一份预算。
- 修复：`$defs` 中的条目不再既随所在文档计一次、又在每次引用时再计。v0.4.0 因此拒绝了一些 v0.3.0 接受的 schema，例如 `$defs` 中有一个由 10 万个 `<` 组成的描述、展开后只有几十字节的 schema。
- **行为变化**：引用展开超出字节预算、深度或节点上限时，`schema.FieldError` 的 `Keyword` 由 `$ref` 改为 `limit`；不含 `$ref` 的 schema 也会超限，原来的关键字有误导。`Message` 不变，`Error()` 中的关键字随之变为 `/schema: limit: …`。改法：判断超限改用 `Keyword == "limit"`。

## v0.4.0

- 修复：契约的引用展开预算按复制进结果的紧凑字节计，每个字节只计一次，不再随嵌套深度和缩进重复计费。此前紧凑后只有约 92 KB 的契约会因超出 1 MiB 被拒绝，导入它的包无法 `cordis generate`。上限值与按契约文件计的范围不变；超限错误改为 `expanded schemas exceed the cumulative byte limit of 1048576`。
- `cordis generate` 写出前用 `schema.Parse` 读回 `cordis_gen.json`，读不回时报出该文件路径并且两个文件都不写；`gocontract.Package.Contract` 同样会因此失败。
- `session.schema.json` 中响应的 `error.code` 放宽为任意字符串，与运行时一致：处理函数返回的应用错误码（如 `game_running`）原样送达，此前按 schema 校验的实现会拒绝这些响应。只放宽，按旧 schema 有效的消息仍然有效。
- `cordis init` 在 go.mod 写入 SDK 所需的 Go 版本，不再写构建 CLI 的更新版本。
- `plugintest.Start` 收到不配对的 spec 与定义时，错误同时说出两者，不再只报 unknown plugin definition。
- `Host.Store()` 返回只读的 `hosting.Catalog`（`Installed`、`Get`、`Selected`、`Entries`、`Runs`、`Preflight`、`PendingRemovals`）；修改须经 Host，以免绕过它对运行实例的协调。迁移：`host.Store().UpdateTrust` 改为 `host.UpdateTrust`，`RetryRemovals` 同理；安装、选择、启用与卸载用 Host 的同名方法。
- `cordis uninstall` 的输出字段改为小写 `removed`、`pending`、`reason`，与其他命令一致（`packagehost.Removal` 增加 json 标签）。
- `native.New` 改为接收一个 `native.Options`；原来的可变参数会静默忽略第一个之后的选项。无选项时写 `native.New(native.Options{})`。
- CLI 信任文件新增 `digests`，批准精确摘要的包而无需签名，与 `TrustPolicy.Digests` 相同；拒绝错误提到的“approved exact digest”现在可以配置。
- `cordis run` 只在启动确有等待原因时输出 `startup` 诊断，正常启动不再打印空的等待行。
- 各命令的 flag 写在参数之后时，报出该 flag 并提示放到参数前，不再误报缺少参数。
- `cordis run -service` 缺少或写错 `-input` 时，在启动插件前报错。
- `cordis run` 的 `-service` 或 `-definition` 写错时，错误列出该定义提供的服务或包内的定义。
- 插件在调用中退出时，`cordis run` 只报告一次原因，不再在关闭时重复打印。
- 错误码按原因区分，不再一律是 `plugin_failed`：信任校验与签名失败为 `denied`，配置不符合 schema 为 `invalid_message`，包未选定为 `not_ready`，存储写入失败为 `internal`；消息带上相应前缀。新增 `contract.Wrap(code, err)`，给错误加上错误码且保留原错误，`errors.Is` / `errors.As`（如 `ErrNotSelected`、`*schema.FieldError`）照常可用。
- Host 的 Logger 为每次激活写 `activation started` 与 `activation stopped`/`canceled`/`failed`，带 package、entry、activation、digest（子 entry 另带 parent）、启动耗时与退出结果，能与 `Status` 和运行历史对应。`Outcome.StartupDuration` 此前从未填写，现在记录启动耗时。
- 插件在调用中退出时，调用方的错误带上退出码（`activation exited without shutdown: exit status 7`），不再只是 `read frame: EOF`；插件往 stdout 写了非协议内容时，错误指出 stdout 只传协议帧。新增 `wire.ErrRead`，标记因对端发来的内容无法读成帧而结束的会话。
- `cordis run` 与 `cordis dev` 把插件写到 stderr 的内容转发出来，每行带 `[entry]` 前缀，插件崩溃时能看到 panic 栈；`cordis host` 用 `-plugin-stderr` 开启。
- `run`、`dev`、`host` 的默认关闭期限由 5 秒改为 25 秒，覆盖一次用尽各段宽限的 native 停止；到期仍未清理完时报错，`run` 与 `dev` 保留仍可能被使用的临时目录并报告路径，不再提前删除。
- CI 在推送、PR 和每周定时运行 govulncheck（仓库与示例插件）；Release 在它通过后才发布。

## v0.3.0

- `cordis pack` 先核对 `cordis_gen.go`、`cordis_gen.json` 与 Go 源码一致：改了契约没有重新生成时报错，不再打包旧契约。新增 `gocontract.Check`。
- 插件以带错误码的错误失败时，Host 报告的错误只带一次错误码（此前为 `invalid_message: invalid_message: …`）。
- `cordis run` 启动超时时若插件仍在 Starting，错误说明这一点并提示放宽 `-startup-timeout`。
- `plugintest.Start` 在 ctx 的期限内（没有期限时 30 秒）等不到所需服务时返回它在等什么，不再一直等到 `go test` 超时。
- 除 `install` 外，store 命令（含 `host`）要求 `-store` 目录已存在：写错路径时报错，不再静默创建并操作一个空 store。
- store 被另一个进程占用时，错误带上 store 路径；新增 `packagehost.ErrInUse`。CLI 提示 Host 运行期间用 `cordis control`。
- `pack`、`sign`、`install` 以及 `inspect` 的 `ref` 增加 `exact`（`id@version#digest`），可直接交给 `select`、`uninstall`。新增 `contract.PackageRef.String`。
- 生成的 endpoint 收到不存在的方法时列出可用方法的线上名称；从 Go 推导的契约在把流式方法当普通调用（或反过来）时直接说明。升级后 `cordis pack` 会要求先重新运行 `cordis generate`。
- 从 Go 推导契约时，本包里带 `json` 或 `cordis` 标签的未导出字段报错，不再被静默略去；约束不适用的报错改为 `minLength does not apply to integer values`。
- 配置不符合 schema 时，`cordis run` 与 `cordis enable` 的错误指出配置来自哪个文件（或 `-config`、stdin）；未知属性的错误列出可接受的属性名。
- `cordis generate` 的报错给出改法：未知指令与约束列出可用项，无法描述的 chan、func、interface 字段提示 `json:"-"`，配置是指针时说明要用结构体，`requires`/`provides` 写成服务名时指出对应接口。
- 打错的命令提示最接近的命令（`unknown command: instal; did you mean install?`）；`cordis generate` 的目录不存在时直接这样说，不再报告为 go 无法启动。
- 安全：`golang.org/x/text` 升到 v0.41.0（GO-2026-5970，包路径校验在非法输入上死循环）；以 `toolchain go1.27.4` 构建（GO-2026-6859，`net/http`），`go` 最低版本仍为 1.27.3。
- **行为变化**：`TrustPolicy.Digests` 中的摘要单独足以信任该包，不再要求包没有签名。带有未知密钥、已撤销密钥或无效签名的包，摘要被批准时也能安装和运行（`signature.json` 不在摘要内，删掉它本来就能通过）；`signature.json` 本身格式错误仍拒绝。发布者授权、撤销与 `PublisherChanges` 照旧。`cordis run -dev-unsigned` 与 `cordis dev` 因此也运行带未知签名的本地产物。
- **破坏性变更**：`TrustPolicy.DevDigests` 改名为 `Digests`，与 `Keys` 并列，不保留旧名；它是生产与开发共用的信任来源。CLI 信任文件格式不变。
- `hosting.Caller` 新增 `Endpoint(required)`：返回 entry 视图里校验过版本的 endpoint，交给生成的 `NewXClient`，一元与流式方法都能调用，包括隔离在 entry 内、根作用域看不到的服务。
- 新增 `hosting.Requester(ctx)`：Host 提供给 native 插件的服务据此得知请求来自哪个激活（`contract.Identity`）；应用和进程内插件用不继承 native 请求的 ctx 发起的调用报告 `false`。它只是信息，Cordis 不据此授权。
- **行为变化**：entry 停用、移除或被替换后，之前取得的 `Caller` 以 `not_ready` 失败，不再改按根空间解析未隔离的服务；恢复重启不影响它。
- 文档更正：`parent` 只把请求关联到正在处理的请求、传递取消与期限，不检测调用环；A 调 B、B 再回调 A 照常并发处理。

## v0.2.0

- **行为变化**：`hosting.Options.Grace` 的默认值从 1 秒改为 5 秒（`hosting.DefaultGrace`）。它是插件应答取消、响应关闭和退出进程的期限；1 秒在清理较慢或机器很忙时会把健康的插件当作卡住。需要原来的行为时设置 `Grace: time.Second`。
- `cordis generate` 先把两个生成文件都写好，再让它们各自就位：写入中途失败时保留原来的一对。

## v0.1.1

- 修复：Linux 上进程树在结束进程组之后、回收领头进程之前，等组内进程全部退出。v0.1.0 里这一步被跳过，取消构建或停止插件后，它的子进程可能还短暂存活。

## v0.1.0

第一个带稳定性承诺的版本：会话协议 `cordis-go.session/v1`、包格式 `cordis-go.package/v1`、契约 `cordis-go.contract/v1` 从这里起只做向后兼容的修改。

- 进程内内核：实例、服务、事件、查询、中间件链、设置、隔离。
- Host 与 native 运行时：包的安装、选择、启用、替换、恢复；流式方法与跨进程中间件链。
- 用 Go 声明契约：`//cordis:service`、`//cordis:plugin`，`cordis generate` 推导 schema 并生成绑定。
- 配置文件驱动的插件树（`loader`），可编辑并写回 YAML。
- 日志与定时辅助。
- Host 选项：`Inflight`（会话的在途上限）、`Grace`（应答取消、关闭与退出的宽限）、`OnCall`（调用观测）。
