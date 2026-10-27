---
title: 上线与维护
description: 准备可信产物，轮换签名密钥，升级代码和数据，并在故障后核对真实状态。
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
codePaths:
  - host.go
  - instance.go
  - cascade.go
  - packagehost
  - access
  - runtime
  - cmd/cordis
---

# 上线与维护

本页给出宿主维护流程。Cordis 管理包、激活和资源；发布审批、密钥托管、备份、业务迁移与补偿仍由应用负责。CLI 的 `run -dev-unsigned` 是本地调试入口，不是生产插件管理服务。

## 发布前检查

- 固定 Cordis 代码版本、Go 工具链和插件依赖。按[快速开始](getting-started.md)构建 Native 或 WASI command，Manifest 与实际定义、服务、能力和配置 schema 保持一致。
- 用 `cordis pack`、`sign`、`validate` 检查发布产物，保存包 ID、SemVer、digest、publisher 和 key ID。`validate` 不替代目标宿主的信任检查。
- 在实际目标 OS / Arch 上执行 Native / WASM 安装、调用、取消、停止和恢复。交叉编译、另一个修订通过或仿真执行不能冒充目标硬件认证。
- 将 Store、namespace、编译缓存和私钥放在不同的宿主管理位置；目录不得由插件写入。WASM 不 preopen 宿主目录，Native 只向受信任代码开放。
- 默认不发能力 grant。为每个 Broker handler 实现声明范围和 grant 范围检查，只返回批准的数据投影；先注册贡献 validator，再启用插件。
- 应用关闭时先停止接收业务，再用独立、有期限的 context 关闭 Host，并处理错误。需要操作系统沙箱或持久任务恢复时，在宿主部署层另行提供。

## 打包和签名交接

工具从核对过的 Cordis 检出构建。下面命令使用已有包目录 `release-package`；密钥文件路径仅为占位，替换为仓库外受控位置：

```sh
./bin/cordis pack ./release-package ./release.zip
./bin/cordis keygen /secure/offline/author.private
./bin/cordis sign ./release.zip author-2026 /secure/offline/author.private
./bin/cordis validate ./release.zip
```

`keygen` 仅在首次创建时执行，拒绝覆盖已有文件；输出的公钥通过可信渠道交给宿主，不从待安装包取得。私钥不得提交、打进插件包或写到 CI 输出。若用 CI 签名，由仓库密钥设施或外部签名服务注入，不在文档里存真实值。

TrustPolicy 至少绑定 key → publisher，再将 publisher 授权到精确包 ID。签名本身不允许执行，Native 也不会因为签名而获得隔离。

## 轮换或吊销密钥

正常轮换：

1. 从可信渠道分发新公钥，把新 key ID 加入宿主 TrustPolicy，保留相同 publisher 和允许的包 ID。
2. 用新密钥签名发布包。相同内容重签不改变 digest；重新 Install 会刷新 Store 中的签名证明，不原地覆盖内容。
3. 验证当前宿主可以 Install / Snapshot 新证明，且重开后仍通过当前策略。保留发布记录，确认用户已取得新证明。
4. 按部署计划撤销旧 key，并持久化应用的信任配置。`host.Store().UpdateTrust(policy)` 只更新当前 Store 的内存策略，重启时仍需从应用配置提供。

密钥泄露时，不等待常规轮换：先停止或隔离可能受影响的激活，部署吊销策略，再用可信新密钥重发。更新信任策略不会自动杀死已经运行的插件；由宿主显式 Stop / Disable。恢复时重新验证包和数据，不因 digest 相同就跳过信任校验。

publisher 变更需要单独批准的 `PublisherChanges`，不是换一个 key ID 就能悄悄转移包所有权。完整签名规则见[包参考](packages.md)。

## 只更新代码

数据格式没有变化时：

1. 安装新包；Install 不切换选择、不停止旧实例。
2. 保存旧引用、启用项和配置，核对目标配置 schema、服务版本、runtime 及必需依赖。
3. 用 `Host.Replace(ctx, newRef, packagehost.ChangeOptions{})` 完成预检、停旧和重建。需要停用反向依赖时先 `PlanCascade`，审批后用显式级联入口。
4. 检查返回错误、每个目标实例 State / Desired / Applied，再运行应用自己的健康检查。

`Replace` 不是零停顿升级，也不承诺失败时全部实例自动回到原状。预检失败不应先停旧；进入停用后的取消或清理失败可能留下部分停用。查实际 Store 选择和实例状态后再恢复，不把整体返回错误解释成没有任何变化。

未迁移数据且信任与依赖仍允许时，可以显式 Replace 回保留的旧引用。恢复旧代码仍是一次新的激活，不复用旧 activation、会话或请求结果。

## 同时迁移数据

先在应用层停止相关写入并建立一致备份，再执行经批准的迁移。`NamespaceStore.Migrate` 支持版本和 revision 冲突检查，但不会替你暂停所有业务，也不把其他数据库和文件放进同一事务。

业务迁移一旦提交，立即执行：

```go
if err := host.Store().ConfirmDataVersion(ctx, packageID, targetDataVersion); err != nil {
    return fmt.Errorf("record committed migration: %w", err)
}
```

这是记录已发生的事实，有意不因 ctx 取消而撤销。记录成功后，再选择或 Replace dataVersion 匹配的新代码；不能先尝试版本切换，失败后才决定是否登记迁移。`MigrationCommitted` 选项支持同样的独立确认顺序，但显式紧跟迁移调用更清楚。

确认失败要停止版本操作并修复持久化，不能启动旧代码。确认后即使新代码选择失败，旧引用可能仍显示 selected，但旧 Snapshot 会被数据版本下界拒绝。需要回退数据时必须执行宿主授权的备份恢复或逆向迁移流程；不手改 catalog 降低版本。

namespace 的确认回调必须来自宿主真实维护决策，不能仅把 guest 的 `confirmed:true` 透传进去。[访问实操](access-tutorial.md)展示 API 顺序，生产应用还需审批、停写和业务备份。

## 备份与恢复

Store 的 catalog、内容、证明和运行记录不是可任意拆开的备份；namespace、应用业务数据库与信任配置也不在 Store 的联合事务中。优先在相关写入停止、Host 正常关闭后建立完整备份，并记录 Cordis 修订和数据版本。若使用在线快照，必须由部署层保证一致性。

故障后：

1. 保留原目录和诊断，不手工把 catalog.next 改名，不删除异常日志尾部掩盖错误。
2. 用当前信任配置 Open；Store 校验唯一已提交根，遗留 running 变为 interrupted，不假报旧进程仍在工作。
3. 重建应用 realm、builtin、Broker、validator 与其他访问设施，再 Restore 外部 entry。
4. 检查启用意图、实际 State、Waiting 和健康请求；业务任务从宿主自己的 journal 恢复，不从 runs.json 猜测已完成。

Open 失败时按[恢复指南](recovery.md)处理，从可信包或一致备份显式恢复；不退回空 Store 或内存 namespace 假装启动成功。升级过持久格式后，旧二进制可能拒绝新字段，不能直接降级打开。

## 卸载与日常维护

先确认影响并停止目标树，再通过 Host 卸载。数据默认保留；删除数据是单独的破坏性授权，多版本共享数据时不可随意删除。Windows 文件占用可能返回 `Removal.Pending`，通过 `PendingRemovals` / `RetryRemovals` 继续处理，不立即宣称磁盘已清空。

namespace 按实际日志量显式 `Compact`，不在每次写入后重写全库。接管任务结束并保存业务记录后 `Release`，避免 manager 长期占用结果额度。WASM 编译缓存只由可信宿主用户写，不能作为包的信任来源。

监测实例状态、等待依赖、有限重试次数、任务和结果配额、未结束的宿主调用、待删除包及存储错误。默认 Runtime 只计数、排空并限制 stderr，不保存内容，也不会自动转发到 Sink。应用自行采集或输出诊断时，应把插件日志视为未受信任输入，经 Sink 脱敏并设置限额；不要把“没有 stderr 输出”当作健康证明。

