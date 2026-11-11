## ADDED Requirements

### Requirement: 从 Go 包生成契约

`cordis generate [目录]` SHALL 分析该目录的 Go 包并写出生成物，目录缺省为当前目录；带 `-output` 时 SHALL 按声明式契约文件生成。`cordis init` 的脚手架 SHALL 用 Go 指令声明插件，不含手写契约，并在生成后可以直接构建和测试。

#### Scenario: 新建的插件

- **WHEN** 作者依次运行 `cordis init`、`cordis generate`、`go test ./...`
- **THEN** 测试通过，目录里没有手写的 schema
