---
title: 组合与生命周期
description: 依赖激活、配置更新、父子所有权与清理顺序。
---

# 组合与生命周期

## 依赖激活

实例声明 `Requires`，全部兼容 provider 发布后执行 Start。等待原因可通过 Status 查看，包括缺少服务、版本不兼容和父尚未发布。同一解析空间中的重复 service key 明确返回冲突。

消费者只使用本次激活的绑定。provider 退役会撤销普通准入、通知取消并使消费者退役；C → B → A 的清理按 C、B、A 完成。清理期间仍可调用原 provider；不会把清理请求发送给替换后的新实例。

## 按服务隔离

在父 entry 上设置 `Isolate: []string{"db@1"}`，这棵子树中的 provider 与消费者便使用自己的 db@1；未列出的服务继续共享。嵌套子实例可单独声明同一 key，再建立一层边界。db@1 和 db@2 独立，不能只写 `db`。

缺少本组 provider 时保持 Waiting，不选择组外的同名服务。provider 更新、停止与恢复只沿实际绑定影响消费者，清理仍访问旧绑定；另一组不因同名服务而被一起重启。

宿主通过 `view, err := host.Caller(entryID)` 获取该登记的视图，再将 view 交给生成的客户端；默认 `host.Call` 只查询根。视图不是插件身份，不能获得清理特权。边界在登记时固定，Update 不移动边界；静态声明持久保存，动态声明随父激活重建。事件依然广播到整个 Host。

## 配置更新

Update 在提交前校验配置。无效配置不改变运行实例和持久记录。有效更新增加期望 revision，清理旧 activation，再尝试启动；并发更新收敛到最新配置，较旧等待者返回 Superseded。

业务初始化失败默认保持 Failed。再次提交有效配置，包括相同配置，会显式重新启动；静态 Failed 实例完成清理后也可用 `Host.Retry` 保持配置重试。持久 native 实例可显式启用[有限自动恢复](recovery.md)，所有路径都不回滚副作用或重放业务调用。

## 父子所有权

builtin 父定义通过 `Children` 携带可选定义；native 子定义来自父 activation 对应的同包内容。CreateChild 绑定父 activation，父重新启动后旧 Scope 不能创建新子实例。

父 Start 可以登记子实例，但不能同步等待子启动。父退役时，包括仍在 Waiting 的子实例也会移除。子失败不自动停止父。

## 并发与取消

服务和事件回调按实例串行；显式 `contract.Go` 工作是并发例外。后台工作在最终发布确认后启动，退役时先取消，资源清理等待其退出。

普通调用与生命周期等待都参与环检测。A 调 B、B 再等 A，或父 Start 等子发布，会返回可诊断错误。全局管理准入只覆盖短提交，不等待插件初始化和清理。

## 与官方 Cordis 的对应

以下对照使用只读参考提交 `35fdc969e4b6c48c48ed44717c7a018324e4dd88`，不是对官方最新版本的兼容承诺。本项目按 Go/native 的执行方式实现对应场景，不能直接加载官方 TypeScript 插件。

| 场景 | 固定参考版本 | 本项目 |
|---|---|---|
| 两组插件使用同名服务 | `Context.isolate` 创建带服务隔离信息的上下文 | Entry/Child 声明完整 service key，边界在登记时固定，由子树继承 |
| 取消监听、只听一次、优先监听 | `on` 返回撤销函数，`once` 与 prepend | `On` / typed `Subscribe` 返回 Cancel 句柄，options 提供 Once/Prepend |
| 取首个处理结果 | serial/bail 按返回值决定是否结束 | First 按显式 Handled 决定，允许明确处理 false |
| 过滤事件范围 | 发射对象可携带 filter，global 监听跳过过滤；普通事件不自动按祖先传播 | Host 广播，不提供 filter/global，也不随服务隔离改变范围 |
| 围绕后续监听执行逻辑 | waterfall 传递 next | 不提供跨进程 waterfall；本地 `contract.Chain` 只包装一个业务 handler |
| 按上下文覆盖服务配置 | `Context.intercept` | 本轮不提供 intercept |
| 改代码后重新加载 | HMR 插件跟踪模块并重载 | dev 构建、预检后关闭旧 Host，再创建新 activation，不保留进程内存 |
| 组合配置及写回 | include/patch 管理配置树和变更写回 | 静态 entry 保存配置及父子关系；动态 child 绑定父 activation，不独立持久化，不提供 include/patch/源配置写回 |

官方参考实现可定位到 [context](https://github.com/cordiverse/cordis/blob/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/core/src/context.ts)、[events](https://github.com/cordiverse/cordis/blob/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/core/src/events.ts)、[HMR](https://github.com/cordiverse/cordis/tree/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/hmr) 和 [include](https://github.com/cordiverse/cordis/tree/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/include)。本项目的 Replace 会清理并重建 activation，旧句柄不控制新实例；离线 CLI 不控制正在运行的 Host，[本机在线控制](local-control.md) 须显式开启，[有限恢复](recovery.md) 须按实例配置。服务隔离也不限制进程权限。

[完整组合程序](composition-tutorial.md) · [资源释放](resources.md)
