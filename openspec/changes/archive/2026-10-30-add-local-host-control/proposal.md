# Proposal

## Why

离线 CLI 与运行中的 Host 不能同时取得 Store 锁，用户无法在 Host 运行时检查实时状态或启停实例。需要一个显式开启、仅本机可访问的控制入口，把命令交给持有 Store 的 Host 执行。

## What Changes

- `host -control-file FILE` 显式开启本机控制入口；默认继续无控制端口。
- 新增 `cordis control -connection FILE status|enable|disable|stop|resume|retry ENTRY`，只管理已有持久意图；status 可省略 ENTRY 并包含动态子实例。
- 使用每次 Host 启动生成的随机凭据和受操作系统权限保护的连接文件；客户端不读取 catalog、不自动回退离线操作。
- 实时状态明确标识当前 Host 会话；控制通过现有 Host API 执行，错误、提交状态与超时如实返回，不自动重发。
- 不做远程监听、Web UI、多人角色、在线安装/替换包、任意业务 RPC、远程配置编辑或 daemon 管理。

## Capabilities

### New Capabilities

- `framework-control`：受认证的本机 Host 控制、实时查询及会话生命周期。

### Modified Capabilities

- `framework-cli`：前台宿主可显式开放本机控制。
- `framework-composition`：配置更新仍使用 Update，允许独立的显式 Failed 重试。
- `framework-host-integration`：区分离线管理与本机在线控制，更新公开能力边界。

## Impact

新增 CLI 私有控制传输及少量按平台处理的凭据文件权限代码；Host 增加可供控制和恢复共用的显式失败重试入口。沿用现有配置、信任、子树所有权和生命周期。新入口不赋予插件新的权限；native 仍拥有用户权限。

这是为当前目录锁模型添加的管理能力，不是官方 Cordis 的控制协议。实施在发布交付和多包开发之后，恢复策略通过后续 change 接入。
