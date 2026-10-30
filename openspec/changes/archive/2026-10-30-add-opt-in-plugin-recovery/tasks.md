# Tasks

## 1. 策略与协调

- [x] 1.1 增加 Host recovery 策略、校验和状态快照，完成独立契约审查；测试默认关闭、非法/未知 entry 拒绝、复制输入和只作用指定持久 native 实例，补齐 API 文档。
- [x] 1.2 实现受管有限退避调度，复用条件重试原语；测试延迟封顶、计数不溢出、短暂 Active 不刷新预算、业务调用错误/Waiting 不重启和清理失败隔离。
- [x] 1.3 将人工 Update/Retry/Stop/Resume/Disable/Remove/Replace 与关闭接入计划失效；race 测试验证旧计划不能控制新登记或覆盖配置，关闭完成前恢复循环已退出。

## 2. 运维入口

- [x] 2.1 接入 host -recovery 严格配置和前台/在线状态，增加有限诊断事件及操作教程；测试策略加载、退避/耗尽/清理错误可观察、人工 retry 只刷新目标预算且不重放请求。
- [x] 2.2 增加真实签名 native 故障恢复验收，覆盖崩溃→清理→有限自动重试→耗尽→在线人工恢复及 Host 重开；验证另一实例不受影响。

## 3. 集成验收

- [x] 3.1 独立审查全部并发边界，完成 conformance/race、vet、文档/schema/OpenSpec 检查及五平台实际 native 恢复；记录精确提交与 CI 结果。

## Workflow follow-up

- 最后同步 framework-cli 的前台宿主最终约定及新增 recovery spec，完成归档。
- 与前三份 change 的完成记录一起提交推送，不改变既有发布资产。
