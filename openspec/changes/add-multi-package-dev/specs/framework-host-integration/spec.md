# Spec Delta

## ADDED Requirements

### Requirement: 可执行的多包开发教程
文档 SHALL 提供两个独立 native 作者包组成开发环境的可执行示例，覆盖显式清单、跨包服务依赖、配置修改、编译失败和整组重启。明确单包入口仍可用，project 仅用于开发，不是生产部署或远程依赖下载功能。

#### Scenario: 联合开发完整循环
- **WHEN** 用户按教程启动 provider 与 consumer，再修改 provider 的行为
- **THEN** consumer 在新组中使用更新后的 provider；编译错误时旧组仍工作，退出后两个进程均回收
