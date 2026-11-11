---
title: 生命周期验收
description: 在真实平台上验证 native 构建、依赖清理链与流。
---

# 生命周期验收

验收用独立构建的真实插件和真实 Host 检查端到端行为。从仓库根执行：

```sh
bash conformance/run.sh
```

Windows 执行：

```powershell
./conformance/run.ps1
```

脚本依次做这些事：

1. 运行 `go test -tags=integration -race ./...`，包含文档教程的提取执行。
2. 独立构建 counter、chain、feed 三个插件（chain 另有一个会拒绝初始化的构建），和三个 Host 程序。
3. 运行三个 Host 程序：

| 程序 | 验证 |
|---|---|
| `basic-host` | 两个独立 Host 各运行一个 counter，配置更新，停用不影响另一个 |
| `lifecycle-smoke` | C → B → A 的服务依赖、消费者先清理、更新后重新绑定、调用取消、重开后恢复；单独构建的拒绝初始化版本：替换失败可观察，显式换回正常代码；同包可选子实例等待 provider、父停用时等待子实例释放资源；查询的零值结果保持“已处理” |
| `stream-smoke` | feed 插件的流式方法与跨进程中间件链 |

集成测试另外覆盖：运行时的双进程状态独立、协议错误、帧预算、超时、stderr 排空和进程树回收；签名包的离线安装、选择、启用、查询与前台退出；`dev` 的连续编辑与候选失败；同一个 Host 中两组隔离服务和动态子树枚举；动态事件的取消与已准入投递的竞争、自取消、并发 `once` 与 256 个活动订阅上限。

验收只声明实际执行过的平台。只读的参考 submodule 不参与构建，Host 程序也不导入插件的业务代码。
