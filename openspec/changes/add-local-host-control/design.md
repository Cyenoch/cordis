# Design

## Context

基线 `1d640b0`；Store 是独占文件锁，Host 已提供 Enable/Disable/Instance/Resume/Instances，但 Failed 重试目前依赖 Update。前台 host 在 Restore 期间持续输出状态。控制传输属于 CLI，不修改插件 wire 协议。

## Goals / Non-Goals

同用户本机管理现有持久 entry，默认不开端点。不自动探测端口、偷读 catalog 或根据连接失败回退到离线管理。不提供安装/换包/配置更新等扩展操作；直接 Go Host API 仍可承担这些操作。

## Decisions

### 1. 回环 HTTP 与连接文件

标准库 HTTP 绑定 `127.0.0.1:0`，无需平台 pipe 库。`host -control-file FILE` 创建随机 256-bit token、随机 session ID，写入严格 JSON `{version:1,address,session,token}`；端点只接受这个会话的 Bearer token 和 session。请求路径固定，Host/Origin 不接受浏览器跨站调用，不开放 CORS。

连接文件以独占创建方式写入。POSIX 用 0600 并确认普通文件；Windows 用现有 x/sys/windows 在创建时设置仅当前用户及 SYSTEM 可读的受保护 DACL，不能先写明文再改权限。文件存在直接失败，避免抢占或删除别人使用的端点。客户端验证文件/字段，地址必须是精确 IPv4 loopback 加端口；不解析域名、不使用环境代理、不跟随重定向。

macOS 使用 open_extended、当前 uid/gid、0600 和空的 NO_INHERIT ACL 在创建时阻止继承；创建后再清 ACL 无法撤回别人已取得的读句柄。读取使用句柄检查 owner、链接与 ACL，拒绝 symlink/reparse point；Unix 非阻塞打开避免路径被换成 FIFO 后挂住。

此措施防止其他普通本机用户读取凭据，不防同用户恶意 native、管理员或已泄漏 token。连接文件只用于授权控制，不进入插件快照。无需配置 TLS、证书、远程地址或 token 管理服务。

### 2. 小型控制协议与 CLI

`cordis control -connection FILE [-timeout DURATION] ACTION [ENTRY]`。status 可无 ENTRY，其余要求现有持久 entry。请求/响应带协议版本和 session；使用固定 POST JSON 路径和有限消息大小（沿用框架消息预算）。未知字段/动作拒绝。默认请求期限 30s，server 也限制等待；header 和 body 读取有期限。

status 合并 Store.Entries 与 Host.Instances，保留 disabled 记录以及 dynamic 来源。输出含 online=true 和会话身份，不含 token。可查询单一 entry，避免大结果静默截断；超出消息预算明确报错。输出只在命令执行线程写 stdout，server 的诊断使用 slog stderr。

Store.Entries 从提交后不可变 catalog map 的原子快照读取并复制返回值，不等待文件 I/O。执行 Snapshot 的复制与哈希移到 Store 长锁之外，返回前重新确认选择、签名、当前信任、依赖和关闭/取消状态；查询检查请求期限。

enable 只读取已有保存 entry 并调用 Host.Enable，之后按请求期限 Wait；不接受远程配置。disable 调 Host.Disable；stop 调当前 Instance.Stop；resume 调 Host.Resume；retry 调新 Host.Retry。不存在/动态写入/错误状态均拒绝。服务端并发状态查询，修改继续使用 Host 既有子树预约，不另设全局长锁。

### 3. 显式失败重试原语

`Host.Retry(ctx, entryID) error` 面向当前已登记的静态 Failed 实例，不写 config 或 enabled。Host 在取得操作权后定位精确 wrapper，核心条件重试在同一锁边界验证实例仍登记、状态 Failed、原 activation 已移除及 desired revision 未变化，再以当前配置创建新 revision。返回等待可被 context 取消；提交后超时不撤销新 revision。

为后续恢复复用内部条件重试，不用“Status 之后 Update(oldConfig)”实现，因为后者可能覆盖并发新配置或重启非失败实例。人工 retry 可以在清理结束但曾报错时明确尝试，自动恢复随后加更严格的清理结果条件。

### 4. 结果不确定与关闭

返回机器可读错误类别及结果阶段。只有服务端在调用 Host 前确定拒绝的请求报告 rejected，CommitError 报告 committed_with_error；普通 Host 错误没有统一的提交标记，报告 unknown，不能按 Conflict/Invalid 等错误码猜测未提交。子树控制可能部分完成，查询应覆盖整个子树；不将其描述为原子操作。传输失败/超时的写操作也报告 unknown，建议 status，不重发。无需幂等请求数据库。

控制服务生命周期归前台 host：Open 成功后开启控制，再 Restore；关闭先关闭准入、取消 handler 等待并等待 handler 返回，再用独立期限 Host.Close。信号不借用已取消请求 context 做清理。只删除仍含自身 session/token 的连接文件；被用户替换的文件保留。异常终止遗留文件由用户明确删除，不凭 PID 自动抢占。

### 5. 写集与验收

CLI 协议/服务/client 在 `cmd/cordis/control*.go`，安全文件创建在 CLI 内按平台文件；Host.Retry/core 原语为公共语义写集，独立审查。真实集成测试用两个 CLI 进程验证目录锁仍在、在线查询、父子停启、Failed retry、坏凭据/版本、超时后查询和正常退出删除。Windows 验证实际 DACL，五平台均运行本机端点；不以 Unix chmod 代替 Windows 文件权限。

## Risks / Trade-offs

- 连接凭据泄漏即获得本机管理权 → 私有文件、禁止代理/重定向、不写日志；同用户 native 已拥有用户权限。
- 超时无法证明操作未提交 → 显式不确定结果，不自动重试；现有 API 语义不改成虚假的事务回滚。
- localhost HTTP 增加输入边界 → 固定动作/路径、认证前不返回状态、有限请求/响应和期限，不构建任意 RPC 路由器。
