## MODIFIED Requirements

### Requirement: 推导出的契约随源码落盘

生成 SHALL 同时写出 `cordis_gen.go` 与 `cordis_gen.json`。没有 `contract`、`contracts` 的作者声明 SHALL 从 `cordis_gen.json` 读取契约，缺少该文件时 MUST 失败并提示先生成。构建 MUST 先重新生成再读取。对同一源码重复生成 SHALL 得到逐字节相同的两个文件。命令行打包 MUST 先按包声明的目标分析 Go 源码，两个生成文件与分析结果不一致时失败并提示重新生成；库的打包入口只读生成文件，不运行 Go 分析。

#### Scenario: 源码改动后未重建就打包

- **WHEN** 作者改了服务接口并重新生成，但没有重新构建
- **THEN** 打包失败，指出声明已变化

#### Scenario: 源码改动后未重新生成就打包

- **WHEN** 作者改了服务接口或配置类型，没有重新生成就运行 `cordis pack`
- **THEN** 打包失败，指出生成文件与源码不一致并提示运行 `cordis generate`，不写出归档

#### Scenario: 为其他平台构建的包

- **WHEN** 包只声明另一个操作系统的产物，源码只在那里编译，并已为它构建
- **THEN** 在本机运行 `cordis pack` 按声明的目标分析源码并成功打包
