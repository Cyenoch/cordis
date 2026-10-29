---
title: 故障排查
description: 从状态和错误定位依赖、启动、调用与清理问题。
---

# 故障排查

| 现象 | 检查 |
|---|---|
| Waiting | Status.Waiting 的服务版本或父发布状态；包依赖与服务依赖分开看 |
| Starting 不结束 | 初始化代码是否等待自己的子实例或循环调用；检查 context 期限 |
| Failed | Status.Error 与 Wait 返回错误；修正原因后显式 Update |
| 重复 provider | 一个 Host 的同 key 服务不能被两个 activation 同时提供 |
| 配置更新被取代 | 后续有效更新已接管期望 revision，检查 UpdateResult.Superseded |
| Quiescing 不结束 | 是否有未退出的回调、后台工作或清理；取消等待不会释放它们 |
| 包已安装但不能启用 | 是否显式 Select，平台是否匹配，必需包依赖是否选中 |
| 单次结果超限 | 缩小或分页业务返回值；完整 JSON 编码帧必须符合 frameBytes |
| 协议会话关闭 | stdout 是否混入日志，消息格式、身份与方向是否正确 |

排查 native 启动可通过 Options.Stderr 收集插件日志。日志转发失败会返回错误，不应通过吞错伪造成功。Go 错误使用 `errors.Is` / `errors.As` 分类。
