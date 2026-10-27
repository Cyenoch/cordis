---
title: 核心概念
description: 理解 Cordis 的 Package、Definition、Entry、Activation、Scope、服务与运行时边界。
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - host.go
  - instance.go
  - children.go
  - ownership.go
  - contract
  - internal/core
  - packagehost
---

# 核心概念

Cordis 分开管理交付代码、实例配置和每次运行的资源：

```text
Package（已安装的代码与版本）
  └─ Definition（可启动的功能定义）
       ├─ Entry A（配置与期望状态）
       │    └─ Activation A（本次运行）→ Scope A
       └─ Entry B（另一份独立配置）
            └─ Activation B（独立进程）→ Scope B
```

## 五个名字

| 概念 | 负责什么 | 不代表什么 |
| --- | --- | --- |
| Package | 已校验的代码、Manifest、版本与 digest | 安装不等于选择，更不等于启动 |
| Definition | 启动函数、必需服务与可能提供的服务 | 不是正在运行的单例 |
| Entry | 实例 ID、定义、配置、realm 与启用意图 | 配置被接受不等于已成功应用 |
| Activation | 一次启动独有的身份 | 重启不复用旧权限或旧响应 |
| Scope | 当前激活的服务、订阅、子功能与资源 | 不是跨进程 Go 对象容器 |

同一份 Definition 可以启用多次。两份外部实例各有独立进程、配置与内存；停用一份不会清掉另一份资源。

## 包依赖与服务依赖

**包依赖**指定需要安装并选择的包 ID 和 SemVer 范围，由包存储在执行前预检。

**服务依赖**指定启动前需要就绪的 `name + major + minor 范围`，由组合内核管理。服务未出现时，实例处于 `waiting`，不会执行 `Start`。

```go
contract.Requirement{
    Name: "example.cache",
    Major: 1,
    MinMinor: 0,
    MaxMinor: 2,
}
```

服务版本中的 minor 是闭区间；不要与包 SemVer 的范围混用。可选功能可以放进同包子实例，让它独自等待，不阻塞父功能。

## Realm 控制可见性

Realm 是服务解析空间。多个实例提供同名服务时，把它们放在隔离 realm，消费者在自己的子树解析服务。它不等同于操作系统沙箱，也不会限制 Native 进程读取文件。

## 安装到退出

```text
Install → Select → Enable → Wait → Call / Update → Disable
                                                    └─ Uninstall
```

- `Install` 校验并保存不可变代码，不自动切换当前选择。
- `Select` 显式选择某个已安装版本。
- `Enable` 接受实例配置并开始协调启动；`Wait` 等到可用或失败。
- `Update` 记录新的期望配置，停止旧激活后重启收敛；取消等待不撤销已接受更新。
- `Disable` 停止实例并撤销所属资源。`Uninstall` 移除未被使用的包代码，默认保留持久数据。
- `Close` 关闭宿主，必须等待并处理清理错误；重启后按需显式 `Restore`。

失败不会自动无限重试。使用显式 `Retry` 或有限预算的 `Supervise`；业务请求不自动重放。

## 资源归激活，事务归宿主

用 `scope.Defer` 或 `contract.NewGroup(scope)` 登记清理。初始化中途失败、配置重启、停用与父作用域退役都会触发所属资源释放。

服务消费者先清理，再释放 provider；单个清理失败仍尝试其他清理并保留错误。已经发生的外部写入不会因此自动回滚。

## 选择运行形式

| 形式 | 适用情况 | 边界 |
| --- | --- | --- |
| WASM | 需要受限的插件执行环境 | WASI command，由独立 worker 执行；宿主能力经代理授权 |
| Native | 信任作者，需要完整 Go / 系统能力 | 受管独立进程，但拥有当前用户权限 |
| Builtin | 编译进宿主的可信模块 | 共享进程，无法强杀不合作的 Go 回调；不替代外部包交付 |

可运行的例子见[组合实操](composition-tutorial.md)。作者和宿主分别从[插件作者指南](plugin-author.md)、[宿主接入](host-guide.md)继续；状态与清理细节见[组合参考](composition.md)。
