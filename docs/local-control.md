---
title: 本机在线控制
description: 从另一个终端查询和管理运行中的 Host。
---

# 本机在线控制

先用离线命令安装、选择包并保存 entry，再显式开放控制入口：

```sh
cordis host -store ./plugins -trust ./trust.toml -control-file ./host-connection.json
```

另一个终端使用同一连接文件：

```sh
cordis control -connection ./host-connection.json status
cordis control -connection ./host-connection.json status counter
cordis control -connection ./host-connection.json stop counter
cordis control -connection ./host-connection.json resume counter
cordis control -connection ./host-connection.json disable counter
cordis control -connection ./host-connection.json enable counter
cordis control -connection ./host-connection.json retry counter
```

| 操作 | 作用 |
|---|---|
| status | 返回当前会话、持久 Entries、实际 Instances 和 Recovery；可指定一个 entry |
| stop | 临时停止目标树，保留启用意图 |
| resume | 恢复一个已完全停止的静态登记；子项需要分别恢复 |
| disable | 持久停用目标树并移除运行登记 |
| enable | 使用既有保存记录的 definition、config、parent、isolate 启用 |
| retry | 仅对已完成清理的 Failed 静态实例，以当前配置重试 |

所有写操作都要求已有持久 entry。动态子实例可查询 Origin/Owner，控制仍由父插件或 Go 宿主句柄负责。入口不提供安装、换包、配置编辑或业务调用。

在线状态带 `online=true` 与本次 session。disabled 记录仍出现在 Entries，Instances 只描述实际登记；它不是运行历史。运行中执行离线 `status -store ...` 仍会被目录锁拒绝，不自动切换管理方式。

## 超时与结果

默认等待 30 秒，`-timeout` 可设为 1ms 到 5m。响应的 phase 有四种：

| phase | 含义 |
|---|---|
| completed | 操作返回，附当前状态；仍须检查响应中的 error |
| rejected | 在调用 Host 前已确定拒绝 |
| committed_with_error | 已有提交证据，但后续持久化、清理或状态查询失败 |
| unknown | 无法确认完整结果；可能已经改变了部分状态 |

客户端断开或超时只取消等待。Stop 也可能已经成功，却返回历史失败；子树操作可能部分完成。遇到 unknown，查询整个受影响子树再决定下一步，不自动重复写命令。清理较慢时仍可并发查询状态。

## 连接文件

默认 Host 不开放端点。启用后只监听随机 `127.0.0.1` 端口，连接文件保存该会话的随机凭据。POSIX 使用私有权限；macOS 创建时阻止 ACL 继承；Windows 创建时设置受保护 DACL。客户端拒绝远程地址、代理、重定向和不安全文件。

连接文件不要提交到仓库或转发。拥有它就能管理这个 Host；同用户 native 已拥有用户权限，这不是插件沙箱。正常关闭删除自己的未被替换文件；异常终止留下的旧文件须先确认原 Host 已结束，再由用户删除。新 Host 不自动覆盖或抢占。

`TestLocalControlAcrossProcesses` 实际从两个进程执行 host/control，验证目录锁、动态归属、父子启停和退出清理；`TestControlSlowCleanupRemainsQueryableAndCancellationDoesNotUndoStop` 验证慢清理期间的查询与取消。
