---
title: 运行与维护
description: 显式选择包、处理退出和保留真实验证证据。
---

# 运行与维护

## 运行前

确认插件来源与目标平台，配置所需签名公钥。原生程序拥有启动用户权限，是否运行由应用决定。

## 更新

先 Install 新包，再 Replace 当前版本。配置与依赖检查失败时保留旧实例；新代码启动失败时保留失败状态，应用显式决定下一次 Update、替换或停用。不要自动重放先前业务调用。

## 退出与备份

使用独立的有限 context 调用 Close。超时结束本次等待，不表示 builtin 已强制退出。`Host.Done()` 才表示排空和目录释放完成；Close 即使返回已有失败，也可能已完成，应同时保留错误并检查完成信号。备份持久目录应在 Host 完成关闭后进行，避免复制未完成的写入；业务数据按应用自己的方式备份。

## 运行诊断

为 `Options.Logger` 设置 `slog.New(slog.NewJSONHandler(writer, nil))` 可输出结构化诊断。先按 entry 查最近 activation，再用 activation 关联状态、失败调用和退出记录；同包多实例也各有独立身份。日志 handler 同步执行，应及时返回，不重入 Host 管理方法。

包操作的 outcome 区分 `rejected`、`committed`、`committed_error` 和没有变更的 `unchanged`。提交后失败仍须处理返回错误，不应直接重试。运行历史沿用最近 256 条终态限制，保留退出及清理类别；它不是实时进程探测或完整审计日志。

原始 stderr 需显式配置 `Options.Stderr`，builtin panic 栈需显式开启 `CapturePanic` 并配置 Logger。错误文本、原始输出和栈可能包含业务数据；配置及调用正文不会由框架自动写进诊断。native 被强制终止时可能没有栈。

仓库的 `diagnostics_example_test.go` 提供可执行的初始化失败示例：从 entry 找到终态 activation，并确认 Close 返回失败时 Done 仍可表示已清理完成。执行 `go test . -run ExampleHost_Done`。

## 验证

每次改动运行相关行为测试，再执行 `bash conformance/run.sh` 或 Windows 的 `conformance/run.ps1`。实际结果以变更任务证据为准；本地编译通过不等于其他平台已运行。
