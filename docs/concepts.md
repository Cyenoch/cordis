---
title: 核心概念
description: 插件、实例、作用域、服务、激活与清理顺序，以及它们在进程内和 native 之间的对应。
---

# 核心概念

读完本页，你能分清 Cordis 里的几个词各指什么，以及停止一个实例时发生什么。示例和 API 见[进程内内核](kernel.md)与[编写插件](plugin-author.md)。

## 词汇

| 词 | 含义 |
|---|---|
| 插件 | `cordis.Plugin[C]`：名称、`Requires`、配置校验和 `Start`。插件只有这一种形状 |
| 实例 | 插件加载到一个作用域后的运行体，用 `Instance[C]` 控制。同一插件可以有多个实例 |
| 作用域 | `*cordis.Scope`：插件登记自己拥有的东西的地方，登记随实例停止而撤销 |
| 激活 | 实例的一次启动。重启、更新配置都会产生新的激活，资源与服务绑定属于这一次激活 |
| 服务 | 以名称（native 还带 major）标识的能力，provider 发布，consumer 在 `Requires` 里声明 |
| entry | Host 持久保存的一条实例配置：哪个包、哪个定义、什么配置、父是谁。见[接入宿主](host-guide.md) |
| 作用域边界 | `Isolate` 为某个服务划出的子树，树内使用自己的 provider |

## 依赖与服务

实例在全部 `Requires` 就绪后调用 `Start`，其中发布的服务在 `Start` 成功后才对消费者可见。缺少服务时实例保持 Waiting，`Status` 说明缺什么；服务出现后自动启动，消失后实例先清理再回到 Waiting。

服务的解析沿作用域树向上，取最近的 provider。同一个空间里的重复 provider 是冲突，不是覆盖。父子关系本身只表达生命周期所有权，不限制谁能依赖谁。

跨进程的服务以 `名称@major` 标识，消费者声明可接受的 minor 范围，Host 解析后把绑定交给激活；进程内的服务是类型化的 Go 值。两者的键类型与差异见[API 导航](api.md#跨进程能力)。

包依赖解决“所需代码版本是否安装并选中”，服务依赖解决“所需 provider 是否已经发布”，两者分开报告。

## 隔离

`Isolate` 为一个服务划出边界：边界内的 provider 与消费者使用自己的这一份，未隔离的服务继续共享。每个服务取最近的显式边界，边界内缺 provider 时保持 Waiting，不回退到边界外。隔离不改变事件的广播范围，也不是权限边界。规则与示例见[组合与生命周期](composition.md#按服务隔离)。

## 生命周期

状态依次为 Waiting、Starting、Active、Quiescing，最终是 Stopped 或 Failed。`Start` 失败、后台工作失败或配置校验失败都进入 Failed；Failed 不自动重试，只有显式 `Update` 或 `Restart` 能恢复。

停止一个实例的顺序：依赖它的实例先清理，子实例停止，然后取消它的 `ctx`、等待在途回调与后台工作，最后按登记逆序执行它自己的 `Defer`。因此消费者的清理仍然可以调用 provider。清理是释放资源，不是业务事务回滚。

`Start` 收到的 `ctx` 在实例停止时取消；资源用 `Defer`，需要随激活取消并等待的后台工作用 `Go`。

## native 与进程内

同一个 `cordis.Plugin` 可以在应用进程里 `Load`，也可以用 `sdk.Define` 作为 native 包独占一个进程。native 插件拥有启动用户的权限，签名证明来源与完整性，不是沙箱。两者在一个 Host 内核里共享同一个服务空间；差异见[编写插件](plugin-author.md#native-运行与进程内运行的差异)。
