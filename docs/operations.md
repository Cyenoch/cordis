---
title: 运行与维护
description: 上线前的检查、版本替换、关闭与备份、诊断日志和验证。
---

# 运行与维护

## 运行前

确认插件来源与目标平台，配置所需的签名公钥。native 程序拥有启动用户的权限，是否运行由应用决定。

## 更新

先 `Install` 新包，再 `Replace` 当前版本。配置与依赖检查失败时保留旧实例；新代码启动失败时保留失败状态，应用显式决定下一次 `Update`、替换或停用。不要自动重放先前的业务调用。

运行中的前台 Host 可用 `-control-file` 开放[本机在线控制](local-control.md)，从另一个终端查询、启停或人工 retry。需要有限自动重试时另加 `-recovery` 策略；没有策略的实例保持人工恢复，见[恢复](recovery.md)。

## 退出与备份

用独立的有限 context 调用 `Close`。超时只结束本次等待，不表示不合作的进程内插件已被终止。`Host.Done()` 才表示排空和目录释放完成；`Close` 即使返回了已有的失败，也可能已经完成，应同时保留错误并检查完成信号。备份持久目录应在 Host 完成关闭后进行，避免复制未完成的写入；业务数据按应用自己的方式备份。

## 诊断

给 `Options.Logger` 设置 `slog.New(slog.NewJSONHandler(writer, nil))` 即可输出结构化诊断。先按 entry 查最近一次激活，再用 activation 关联状态、失败调用和退出记录；同一个包的多个实例各有独立身份。handler 同步执行，应及时返回，不得重入 Host 的管理方法。

包操作的 outcome 分为 `rejected`、`committed`、`committed_error` 和没有变更的 `unchanged`。提交后失败仍须处理返回的错误，不应直接重试。运行历史保留最近 256 条终态，含退出与清理类别；它不是实时进程探测，也不是完整审计日志。

原始 stderr 需显式配置 `Options.Stderr`，进程内插件的 panic 栈需显式开启 `CapturePanic` 并配置 Logger。错误文本、原始输出和栈可能含业务数据，配置及调用内容不会被框架自动写进诊断。native 进程被强制终止时可能没有栈。

## 验证

改动后运行相关的行为测试，再执行 `bash conformance/run.sh`（Windows 用 `conformance/run.ps1`）。只有实际执行过的平台才算验证过，本地编译通过不代表其他平台已运行；步骤见[生命周期验收](lifecycle-acceptance.md)。
