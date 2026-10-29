---
title: 生命周期验收
description: 独立原生构建和真实依赖清理链。
---

# 生命周期验收

从仓库根执行：

```sh
bash conformance/run.sh
```

Windows 执行：

```powershell
./conformance/run.ps1
```

脚本运行 integration 与 race 测试，独立构建 counter 和 chain 插件，再运行两个真实 Host 程序。

- counter：不同 Host 中的两个原生实例、配置更新、停用不影响另一实例。
- chain：C → B → A 服务依赖、消费者先清理、更新后重新绑定、调用取消、重开后恢复。
- 父子与事件：原生父实例先发布，可选子实例等待 provider，父停用等待子资源释放；First 分发保留明确处理的零值结果。
- 版本恢复：单独构建会拒绝初始化的二进制，验证替换失败可观察，以及显式换回正常代码。
- runtime 测试：双进程状态独立、协议错误、帧预算、超时、stderr 排空、进程树回收。
- 文档测试：从教程提取程序，在独立 module 中构建、运行并比对输出。

验收只声明实际执行的平台。只读参考 submodule 不参与构建，宿主程序不导入插件业务代码。
