---
title: 恢复与持久意图
description: 区分代码选择、启用意图和当前运行状态。
---

# 恢复与持久意图

Host 关闭前会等待激活释放，持久启用记录保持不变。重开同一目录后，应用显式调用 `Restore(ctx)`，逐条返回登记、等待、失败或已有结果。

重复恢复复用当前登记，不重启正常 activation，也不自动重试 Failed。`Stop` 是本次 Host 的临时停止，`Disable` 是持久停用；重复 Restore 不会撤销当前临时停止。

关闭后重开则按 enabled 意图重新启动，包括上次失败的条目。动态子实例不独立持久化，由新的父 activation 再次创建，旧子句柄不能控制它。排障时可在 `Instances()` 中根据 Owner 找到动态子项；清理期间仍可看到 Quiescing。

Close 返回错误不必然表示仍有资源占用：同时检查 `Host.Done()`，仅在信号结束后重开或移除临时目录。信号尚未结束时可用新的 context 继续 Close，不可把等待超时当作完成。

包安装和选择以原子目录记录为准。中断恢复不会猜测尚未提交的选择；运行记录可报告中断，但不恢复或回放业务调用。

`packagehost.CommitError` 表示提交已经发生，只是后续同步或清理失败。应用应重新查询当前记录后再决定操作，不能直接当作“什么也没发生”。

没有业务数据迁移或兼容旧目录的自动修复层。项目未发布，目录格式随当前契约收敛。

## 人工重试

`host.Retry(ctx, entryID)` 只接受当前静态 Failed 登记，要求旧 activation 已完成清理。它保留当前配置和持久意图，提交一个新 revision；取消只结束等待，不撤销已提交的重试。对 Active、Waiting、Starting、Stopped 或动态 child 明确报错。

人工 retry 可在清理曾报错但已经结束后执行。先处理失败原因，再使用 [本机控制](local-control.md) 的 `retry` 或 Go API；改变配置继续使用 Update。

## 可选自动恢复

只给允许重复初始化的持久 native 实例配置策略。它可能再次执行初始化的外部副作用，不恢复内存，也不重放业务请求。

```toml
# recovery.toml，counter 必须已存在于 Store。
[counter]
maxRetries = 3
initialDelay = "1s"
maxDelay = "10s"
```

```sh
cordis host -store ./plugins -trust ./trust.toml \
  -recovery ./recovery.toml -control-file ./host-connection.json
cordis control -connection ./host-connection.json status counter
```

策略文件支持严格 JSON、TOML、YAML，只在启动时读取。Go 宿主使用 `Options.Recovery`：

```go
Recovery: map[string]cordis.RecoveryPolicy{
    "counter": {MaxRetries: 3, InitialDelay: time.Second, MaxDelay: 10 * time.Second},
},
```

Open 校验并复制策略；entry 必须已保存且属于 native 包，可以暂时 disabled。最大次数和初始延迟必须为正，最大延迟不得小于初始值。默认关闭，不按包 ID 扩散，不继承给 builtin 或动态 children。

第 n 次自动重试等待 `min(initialDelay * 2^(n-1), maxDelay)`，初次启动不计数。只重试当前 Failed activation，且必须清理完成、没有 CleanupCode。Waiting、依赖环诊断和单次业务调用错误不会触发恢复；短暂进入 Active 也不刷新预算。

`Host.RecoveryStatus()` 和在线/前台输出显示会话内的 used、maxRetries、identity、revision、state 和 nextRetryAt。scheduled 表示等待，retrying 表示已提交，exhausted 表示耗尽，cleanup_failed 表示需要人工处理；idle 表示没有待重试的失败，canceled 表示人工停止等操作取消了周期，missing 表示持久记录已删除。快照随协调循环更新，调度可能晚于计划时间，但不会提早。

接受的人工 Retry、Update、Resume 或重新登记开始新周期。非法配置、被拒绝的包替换保留原计划与额度。Stop、Disable、Remove、Replace 和 Close 阻止旧计划复活实例。重开 Host 刷新预算，仍要显式 Restore；运行历史保留，预算不持久化。

`TestNativeRecoveryAndOnlineManualRetry` 使用真实签名 native 包验证退出码、有限恢复、在线人工重试、初始化失败和重开；健康实例及 Waiting 实例不受影响。
