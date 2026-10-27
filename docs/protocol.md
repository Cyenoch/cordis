# 会话协议

`contract` 是宿主、Runtime 与 SDK 共用的数据契约；`wire` 只处理有界双向会话，不负责领域授权。

- UTF-8 NDJSON，stdout 仅协议，stderr 日志；默认每帧 1 MiB、32 层、数组 10,000、64 个在途请求。
- `request`/`response`/`cancel`；每个会话绑定一个 activation，宿主请求 ID `h:`、来宾请求 ID `g:`，方向内单调递增且不可重用。
- `initialize` 在业务调用前完成。业务调用使用显式 service/version 和 JSON 数据，不传宿主对象或 Go 指针。
- `parent` 是当前 endpoint 中的请求引用，由 endpoint 和宿主绑定；不接受插件自造的身份链。跨插件调用的等待环由组合内核检测。
- 每次调用都有 deadline；取消先拒绝结果提交并发送 cancel。宽限期内没有回应会关闭会话，Runtime 负责终止和回收执行进程。
- 成功 shutdown 先完整写出响应，再关闭 guest 会话；不能等待下一次 stdin EOF 才退出。响应已匹配后连接关闭仍返回该响应，但调用取消继续优先拒绝提交。
- 一次取消不撤回已发生的外部副作用，也不自动重试业务调用。旧 activation 的消息不能成为新实例状态。
- 协议错误关闭对应会话，错误输出不默认包含原始载荷。宿主 handler 的错误还须经过 `access.Sink.PublicError` 投影。

宿主若已有公开协议，使用 `contract.Launch.OpenSession` 提供会话构造器，不在原协议外再包一层通用 envelope。Facade 的 `Options.SessionFactory` 按已经验证的 Manifest 选择构造器，`Options.HandleGuest` 接收经过 Scope 生命周期与 policy 准入的 guest 请求。返回的 session 若实现 `contract.PluginSession`，Facade 和 Runtime wrapper 保留其 Initialize/Invoke/Deliver/Shutdown 语义。专有协议的 codec、领域字段与兼容性验收由宿主维护，不随 Cordis 发布。

SDK 的 `Serve`/`ServeTransport` 管理作者侧初始化、服务注册、事件与资源清理。作者的普通处理器串行准入，控制/取消独立读取。自行创建的裸 goroutine 不是受管任务，不能依赖框架替作者回收。

SDK 使用标准 Go goroutine 处理业务、响应和独立控制读取，不要求作者调用 Cooperative/Pump。WASI worker 保留原始 `*os.File` stdin，使 wazero 的非阻塞读取与 `poll_oneoff` 能推进等待中的 goroutine；业务请求等待宿主响应时仍可接收取消和 shutdown。

## 取消与作者子实例

发送准入是可取消的；一个慢写者不使后来的独立 deadline 失效。未响应取消保留到应答或宽限期到期，不能被新取消淘汰；有界历史满且无法安全回收时会话失败关闭。已取消请求的在途反向请求沿原 parent context 被取消，不因合法 parent 的迟到而误杀健康会话；业务 handler 不会在已取消 context 下新准入。

通用 SDK 协商 `children`，通过 `child` 控制方法发送 create/status/wait/update/stop/retry。宿主只使用当前 Scope 身份解析局部 child ID，不信任 guest 自报 owner。子实例控制和服务调用共用等待环检测。专有 Options.HandleGuest 接管全部方法时，须自行明确是否提供该能力，不由框架在专有 ABI 外隐式加 envelope。
