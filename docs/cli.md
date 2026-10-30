---
title: CLI 命令
description: 初始化、构建、签名、离线管理、前台宿主与编辑重启。
---

# CLI 命令

从当前检出构建工具：

```sh
go build -o ./bin/cordis ./cmd/cordis
./bin/cordis help
./bin/cordis version
```

也可在仓库根使用 `go run ./cmd/cordis`，Windows 输出文件改为 `.exe`。采用标准库 flags，标志放在路径等位置参数之前；`--` 可结束标志解析。所有命令支持 `-h`、`--help` 和 `-version`，也可使用 `cordis help COMMAND`、`cordis --version`。

`version` 显示实际 Go build/module 信息，包括可用的 VCS revision、修改状态与 module replace。工作区构建可能显示 `(devel)`，不会伪装成已发布版本。

## 命令一览

| 命令 | 作用 |
| --- | --- |
| `init -module MODULE [FLAGS] DIRECTORY` | 创建作者源、业务入口、业务测试与独立 go.mod，不覆盖已有文件 |
| `build -go-package PACKAGE -goos OS -goarch ARCH [FLAGS] DIRECTORY` | 从作者源生成代码、构建明确目标，写入 build.json |
| `pack DIRECTORY ARCHIVE.zip` | 校验声明和构建描述，生成 ZIP 和完整性表 |
| `keygen PRIVATE_KEY_FILE` | 创建 Ed25519 私钥文件，只向 stdout 输出公钥 |
| `sign ARCHIVE.zip KEY_ID PRIVATE_KEY_FILE` | 为归档签名 |
| `inspect ARCHIVE.zip` | 查看包引用和 Manifest，不授权执行 |
| `validate ARCHIVE.zip` | 检查归档格式与完整性，不评估宿主信任 |
| `generate -output FILE\|- [FLAGS] CONTRACT...` | 合并检查多个契约，生成已格式化的 Go 源码 |
| `run -dev-unsigned [FLAGS] ARCHIVE.zip` | 在临时宿主内运行自己的本地开发产物 |
| `list -store DIRECTORY` | 列出已安装包、选中版本和持久 entry |
| `install -store DIRECTORY -trust FILE ARCHIVE.zip` | 验证并安装签名包 |
| `select -store DIRECTORY -trust FILE ID@VERSION#SHA256` | 选择精确版本 |
| `enable -store DIRECTORY -trust FILE [FLAGS] ENTRY` | 保存校验后的启用意图，不启动插件 |
| `disable -store DIRECTORY ENTRY` | 原子停用该持久 entry 及其子树 |
| `remove -store DIRECTORY ENTRY` | 删除已停用、没有持久子项的 entry 意图 |
| `uninstall -store DIRECTORY ID@VERSION#SHA256` | 卸载精确包，保留依赖与使用中保护 |
| `status -store DIRECTORY [ENTRY]` | 查看离线意图及最近运行记录 |
| `host -store DIRECTORY -trust FILE` | 在前台恢复启用实例，直到取消 |
| `control -connection FILE ACTION [ENTRY]` | 查询或启停显式开放本机控制的 Host |
| `dev -dev-unsigned -go-package PACKAGE [FLAGS] DIRECTORY` | 监视单包输入，构建并重启本机插件 |
| `version` | 显示实际构建信息 |

## 初始化与构建

```sh
./bin/cordis init -module example.net/my-plugin ./my-plugin
```

工作区 CLI 没有可用的发布 module 版本时，须用 `-sdk-version` 指定实际要依赖的 Cordis 版本，不会回退到 `latest`。`-id` 默认 `example.plugin`，`-publisher` 默认 `example`；发行版本只写在 `plugin.toml`，不复制到业务 Go。

默认创建 `plugin.toml`、`main.go`、`main_test.go` 和 `go.mod`。作者源是对象型 TOML，内联最小 `main` definition，未声明的配置是严格空对象。业务入口使用生成的 `NewMainDefinition` 与 SDK 的标准进程入口，测试使用 `plugintest`。不会生成另一套构建脚本，也不写入开发信任或 native 执行许可。

默认声明当前系统的 native 目标。构建必须显式指定 `-goos` 和 `-goarch`，值须与作者源中的 artifact 一致。`build` 调用 `packagehost.Build`，不运行产物探测能力。

| build 标志 | 说明 |
| --- | --- |
| `-go-package PACKAGE` | 必填，明确的 Go 构建包，例如 `.` |
| `-goos OS` / `-goarch ARCH` | 必填，原生 Go 构建目标 |
| `-output PATH` | 相对作者目录的产物路径；默认取声明目标，显式值必须与声明一致 |
| `-generated-package NAME` | 生成代码包名，默认 `main` |
| `-generated-output PATH` | 相对作者目录的生成文件，默认 `cordis_gen.go` |

`build.json` 记录声明指纹、生成摘要、实际选用的 SDK module 版本/能力、目标和产物 hash，并作为 JSON 结果输出。它由工具维护，不是第三份手写 Manifest。打包会检查声明漂移、旧生成代码及产物不一致；外部流水线使用 `packagehost.PrepareBuild` 和 `packagehost.WriteBuildDescriptor` 显式提供同样证据，不能用 CLI 版本代替实际 SDK 能力。描述能防止意外漂移，不证明恶意二进制行为。

## 打包与签名

```sh
./bin/cordis pack ./my-package ./my-package.zip
./bin/cordis keygen ./author.private
./bin/cordis sign ./my-package.zip author-key ./author.private
./bin/cordis validate ./my-package.zip
./bin/cordis inspect ./my-package.zip
```

包目录只能有一个作者源候选：`plugin.toml`、`plugin.yaml`、`plugin.yml` 或 `plugin.json`。格式或声明错误直接失败，不按其他格式重试。作者源不被回写；打包在私有 staging 生成完整机器 `plugin.json`，再计算完整性。

Pack 仅发布元数据、全部声明产物和顶层 `resources` 的精确文件清单，例如 `resources = ["assets/message.txt"]`。不接受目录或 glob；未列出的开发文件不进入 ZIP。打包仍校验 build.json 和生成代码。inspect 的 `files` 返回最终文件名、大小和 SHA-256，完整规则见[外部插件包](packages.md#发行文件)。

把公钥交给宿主维护者；私钥存放在仓库外或受控密钥设施，不提交到版本库。`keygen` 拒绝覆盖现有文件。`inspect` / `validate` 不会因为签名存在就信任作者；执行许可由宿主 TrustPolicy 决定。

## 离线管理与前台运行

信任文件采用 JSON、TOML 或 YAML，扩展名决定格式。公钥是 `keygen` 输出的 64 位十六进制文本。下面示例授权 `author-key` 为 `example.author` 签署 `example.hello`：

```json title="trust.json"
{
  "keys": {
    "author-key": {
      "publicKey": "替换为 keygen 输出的 publicKey",
      "publisher": "example.author",
      "revoked": false
    }
  },
  "publishers": {"example.author": ["example.hello"]}
}
```

还可提供 `revokedPublishers` 和 `publisherChanges`，语义与 `TrustPolicy` 相同。生产管理没有跳过验签开关；签名信息本身不能建立信任。

```sh
./bin/cordis install -store ./state -trust ./trust.json ./hello.zip
./bin/cordis select -store ./state -trust ./trust.json 'example.hello@0.1.0#完整摘要'
./bin/cordis enable -store ./state -trust ./trust.json -package example.hello -config-file ./config.toml hello
./bin/cordis status -store ./state
./bin/cordis host -store ./state -trust ./trust.json
```

`select` 的 ID、version、digest 取自 install/sign 输出，替换示例中的完整引用。`enable` 只保存意图，`host` 才执行插件。前台输出每个实例的状态、等待原因和恢复错误；部分实例失败或缺依赖时继续运行，默认不自动重试。Ctrl+C 使用独立的关闭期限回收进程，默认 `5s`，可用 `-shutdown-timeout` 调整。

`host -control-file FILE` 显式开启[本机在线控制](local-control.md)，供另一个终端通过 connection 文件执行 status、enable、disable、stop、resume、retry。它只管理已有持久 entry，动态子项只查询。请求默认 30s，可用 `control -timeout` 调整至 1ms–5m；超时后的写入结果可能不确定，先查询，不自动重发。

`host -recovery FILE` 按已有 native entry 加载[有限恢复策略](recovery.md)，前台及在线状态都附带恢复计数；省略仍由人工处理 Failed。恢复策略不替代持久 entry，也不重放调用。

所有离线命令都必须取得 Store 独占锁，包括 list/status；运行中的 Host 持锁时直接拒绝。退出后再执行 status，输出带 `"offline": true`：保存的 `enabled` 和历史 Active 记录不代表当前有活进程。诊断写 stderr，结果写 stdout；输出失败发生在提交之后时会明确报告已提交，不自动撤销或重试。

已有 entry 的 `enable` 保留未指定的 package、definition、parent、config 和 isolate。`-parent ""` 清空父项，`-isolate ""` 清空隔离列表，`-config '{}'` 显式替换配置；新 entry 必须提供 `-package`。`-isolate db@1` 可重复使用，每项是完整服务 key。

不兼容 schema 的升级顺序是：停止前台 Host，disable 受影响子树，从叶到根 remove 旧意图，select 新引用，再用新配置 enable。只 disable 不会删除配置，也不会绕过新版本校验。list/status/disable/remove/uninstall 不要求信任文件；install/select/enable/host 必须明确提供。

## 编辑后自动重启

单包和多包共用同一候选切换流程。多个本地作者包使用：

```sh
./bin/cordis dev -dev-unsigned -project ./development.toml
```

清单格式、相对路径和可运行的 provider/consumer 示例见[多插件联合开发](multi-package-dev.md)。project 与单包目录、go-package、definition、config/config-file/config-format 参数互斥；超时和额外 watch 参数仍可使用。

在作者目录已完成 `go mod tidy` 后：

```sh
./bin/cordis dev -dev-unsigned -go-package . -config-file ./development.toml ./my-plugin
```

每个作者包只接受一个匹配本机的 native target；单包的 `-definition` 选择一个定义，默认使用包的默认定义。运行范围包含它创建的同包 children。多目标声明明确报错，不会替你删改；`-startup-timeout` 默认 `30s`，`-shutdown-timeout` 默认 `5s`，project 模式对整个候选组计时。

监视最近 Go 模块目录中的源码、资源、作者声明、契约、go.mod/go.sum，以及显式配置文件。模块外依赖用可重复的 `-watch DIRECTORY` 加入；不自动遍历 `replace` 的外部目录。配置可用 `-config` 或 `-config-file`，不能来自 stdin。生成文件、声明产物、`.git` 和工具临时文件不触发自身重建。系统临时根不能位于受监视目录内；这种布局会明确报错，需将 `TMPDIR`（Windows 为 `TMP`）改到外部。

每次候选在作者目录外的临时工作目录中拥有独立 ZIP 和 Store，只许可该候选摘要，因此可以保持发行版本不变连续编辑。Build 仍会更新源码目录中的生成文件、二进制和 build.json；整个源码构建不是事务。Pack 只包含声明产物、resources 和元数据；显式资源绕过编辑器临时文件名过滤，真实生成输出仍不触发重建。

编译、Pack、配置预检失败时旧实例继续运行；输入改变后才重试。预检通过才关闭旧 Host，关闭完成后再核对输入 generation，仅启动最新候选。旧 Host 已排空但带历史错误时，输出错误并继续；未完成清理则停止切换、报告保留路径。新插件初始化失败后保持 Failed，等待下次编辑，不恢复旧进程、内存或业务请求。

stdout 输出带 generation 的 `build`、`build_failed`、`runtime` JSON；stderr 输出结构化诊断。退出取消监视与构建进程树，完成 Host 关闭后删除临时目录。

## 本地运行

```sh
./bin/cordis run -dev-unsigned -definition counter -service example.counter -major 1 -config-file ./counter.toml -input-file ./request.json ./bin/counter.zip
```

| 标志 | 说明 |
| --- | --- |
| `-dev-unsigned` | 必须显式给出，仅许可本次精确摘要，不持久化信任 |
| `-definition ID` | 包内定义；默认用 Manifest 的默认定义 |
| `-service NAME` | 启动后调用的服务；省略则打印实例状态 |
| `-major N` | 服务主版本；省略时必须能唯一匹配，不能猜测 |
| `-min-minor N` / `-max-minor N` | 兼容 minor 闭区间 |
| `-startup-timeout DURATION` | 默认 `30s`；必须大于零；只约束启动，不缩短后续业务调用 deadline |
| `-config JSON` / `-input JSON` | 内联 JSON，默认均为 `{}` |
| `-config-file PATH` / `-input-file PATH` | 从文件加载；`-` 显式选择 stdin |
| `-config-format FORMAT` / `-input-format FORMAT` | `toml`、`yaml` 或 `json`；省略时只按已知扩展名选择 |

显式内联值与对应的文件/格式 flags 冲突，即使值恰好为 `{}` 也拒绝。格式 flag 必须配文件 flag；两份输入只能有一份使用 stdin，且 stdin 必须显式指定格式：

```sh
printf '%s' '{"method":"execute","input":{"operation":"get","delta":0}}' | ./bin/cordis run -dev-unsigned -service example.counter -config-file ./counter.toml -input-file - -input-format json ./bin/counter.zip
```

配置、调用输入与契约文件共用 `config` loader。它拒绝重复键、尾随文档和不支持的语法，不猜类型、不合并环境变量、不失败后换格式。TOML 适合对象；显式 null、非对象根使用 YAML 或 JSON。错误保留输入来源和可获得的位置。

启动等待状态变化时，stderr 输出有界 `startup` JSON 诊断：definition、状态、实际 Requirement 范围、可见 provider 和不兼容原因；缺 provider 到期后非零退出。stdout 只输出最终状态或调用结果。启动超时不等于业务调用超时，取消也不回滚外部副作用。

应用服务可通过[宿主公开 API](host-guide.md)组合。已签名但未被信任的包不会由 `-dev-unsigned` 绕过坏信任；正式签名包由宿主 TrustPolicy 加载。Native 仍拥有用户权限，不是沙箱；初始化和构建不会额外授权执行。

## 生成 Go 契约

```sh
./bin/cordis generate -package pluginapi -output ./api.gen.go ./echo.schema.toml ./settings.schema.yaml
```

输入使用 Cordis 的[受限数据契约](contracts.md)，不接受任意 JSON Schema。所有输入由 `schema.GenerateMany` 一次检查输出符号；通过完整解析、冲突检查、生成和 gofmt 后，才用同目录临时文件替换目标。生成或写入失败保留旧文件；不会先截断目标，也不要求再次运行 gofmt。

`-output` 必填；只有显式 `-output -` 才向 stdout 写代码。文件格式由扩展名确定，`-format toml|yaml|json` 可显式指定全部输入的格式。输出含配置、定义包装、客户端、服务端注册和严格 codec，未知 schema 特性报错。

```sh
./bin/cordis generate -package pluginapi -output - ./echo.schema.json
```

不要用 shell 重定向覆盖已有生成文件：shell 在命令执行前就可能截断目标，CLI 无法保护这种截断。完整项目见[生成服务实操](generated-service.md)。

## 退出码

- `0`：命令完成，包括全局及子命令帮助、版本查询。
- `1`：格式/契约校验、信任、I/O、构建、启动或业务调用失败。
- `2`：参数使用错误，例如未知 flag、缺少 `-dev-unsigned`、输入冲突或服务版本匹配不唯一。

错误写 stderr，结构化结果写 stdout。命令不隐式重试失败业务调用。
