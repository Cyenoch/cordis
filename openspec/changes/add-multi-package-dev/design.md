# Design

## Context

基线 `1d640b0`；现有 `devCandidate` 已表示整台 Host，切换有 generation 两次复核、关闭完成信号和旧候选清理。限制集中在 `devOptions`、watcher 的单一 source/configFile 与 `prepareDevCandidate`。复用这些机制，不更改 runtime/SDK。

## Goals / Non-Goals

一次 dev 管理一组显式本地包，每次重建整个组。使用 packagehost 的包依赖规则及 Host 的服务等待规则，不建立第二个服务依赖图。生产持久 Store、签名策略和单包 CLI 保持原契约。

## Decisions

### 1. 开发清单

`dev -dev-unsigned -project FILE` 与目录、go-package、definition、config/config-file 参数互斥。超时 flags 继续作用整个候选。清单由已有 config loader 严格解析，结构为：

```toml
watch = ["../shared"]

[[packages]]
directory = "./provider"
goPackage = "."

[[packages]]
directory = "./consumer"
goPackage = "."

[[entries]]
id = "provider"
package = "example.provider"
definition = "main"
config = {}

[[entries]]
id = "consumer"
package = "example.consumer"
configFile = "./consumer.toml"
```

entry 同时支持 parent/isolate；config 与 configFile 互斥，省略规范化为 `{}`。package 引用作者声明 ID，不另造别名；重复 ID、相同真实作者目录和重复 entry 拒绝。entry 默认且始终启用，不加 enabled/生产部署语义。路径基于 project 所在目录，额外 `-watch` 可继续显式追加。

清单不自动解析 Go replace；未列出的模块外输入仍需 watch。单包输入归一为一个包和 ID 为 dev 的 entry，复用相同候选准备与切换循环。

### 2. 有效计划与监视

保持一个串行扫描/构建协调器。初始必须能解析出有效清单并建立监视；循环开始后错误清单成为 build_failed，旧监视继续跟踪清单直到修复。每个有效清单刷新整个监视集合：所有最近模块、配置文件、显式 roots；作者明确列出的资源像配置文件一样强制监视，不因 `.cordis-` 或 `~` 等文件名被略过。所有真实生成输出与声明产物仍按精确路径统一排除，避免同模块多包互相触发重建。

devInput 保存不可变有效计划及解析该计划时实际读取的清单字节摘要，generation 将此摘要与其他输入指纹一起计算；候选只消费这一份计划，不重复读取 project。不能将计划 A 配上随后扫描到的清单 B 摘要。构建后及旧 Host 关闭后重新读取并扫描比较；修改清单增删文件时更新监视集合，不丢掉清单自身的监视。测试以同步点在解析后、扫描前修改 inline config，验证不能启动旧配置。

文件指纹包括修改时间，避免普通保存 B 又撤回 A 时遗漏中间编译输入。轮询不冻结整个源码树；保留原时间戳且在两次扫描间完全恢复的外部改写不可观察，不承诺检测这种写法。

### 3. 全组候选

按稳定顺序串行 Build/Pack，收集每包精确摘要后打开一个临时 Store，一次安装全部包。按必需包依赖的拓扑顺序 Select；缺失依赖/不兼容版本/环通过已有校验或预检错误拒绝，optional 不构造强制环。所有 entry 先按父子顺序 EnableEntry，不启动进程。全部成功后才有资格关闭旧组。

Restore 由现有 Host 同时登记所有启用 entry 后等待，不按包的服务依赖逐一阻塞。服务缺失保留 Waiting；包依赖不满足属于预检失败。包或配置失败清理候选，旧组不受影响；关闭未完成继续保持目录并退出，不能强行切换。

### 4. 验证和所有权

实现集中于 `cmd/cordis/dev*.go`，必要的 watcher 泛化属于 CLI 私有类型。示例为两个独立 Go module；集成测试通过可观察输出确认 provider 改动到达 consumer，故障时两个旧 activation 均保留，修复和连续编辑启动最新组。

文档 CI 暴露全组停机时的既有 Host bridge 竞争：核心先取消 activation，native 进程尚未接收退役通知就收到反向调用错误，受管任务将正常停机当成失败。普通 guest 请求在返回前，通过核心锁确认退役状态，再等待既有 `retireNotified` 通知屏障或原请求取消；不等待任务结束/资源清理，清理调用继续保留原有通行权限。该顺序修复不改 runtime/SDK 协议，也不吞掉真实业务错误。

## Risks / Trade-offs

- 全组构建和重启可能较慢 → 首版换取简单一致的行为，不引入缓存/增量切换。
- 同模块多个包生成文件相互影响 → 统一排除集合并在重建完成后复核 generation。
- 初始化失败发生在旧组停止之后 → 保留新组真实状态，下一次编辑恢复；不伪称无缝升级。
