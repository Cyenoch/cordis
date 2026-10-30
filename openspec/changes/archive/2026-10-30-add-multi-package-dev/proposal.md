# Proposal

## Why

现有 dev 只能运行一个包，开发两个相互依赖的本地插件时，需要自行写宿主并管理重建。需要一个显式的开发清单，把多个本地包和实例一起放进已有的开发循环。

## What Changes

- `cordis dev -dev-unsigned -project FILE` 加载严格的 JSON/TOML/YAML 清单，显式列出本地作者目录、Go 包和实例配置。
- 全组串行构建、打包和预检，共享一个隔离候选 Store；成功后关闭旧 Host 并启动最新一组。
- 监视所有包的模块输入、清单和配置；任一失败保留旧组，输入再次变化才重试。
- 保留单包命令；两种输入归一到同一循环，不产生两套切换状态机。
- 不做远程拉取、依赖自动求解、按依赖图局部重启、HMR、配置回写、生产部署清单或任意 hooks。每个作者声明仍只允许一个本机 target。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-cli`：增加本地多包开发清单及整组切换。
- `framework-host-integration`：增加可执行的两个独立包联合开发教程。

## Impact

主要修改 `cmd/cordis/dev*.go`、CLI 参数、集成测试与示例文档；复用 packagehost、Host 和配置 loader。依赖 `prepare-native-plugin-release` 的发行文件边界，避免开发清单或运行文件被隐式发布。

此处的整组重启是项目自身的开发体验选择，不承诺官方 Cordis HMR 或进程内状态保留。
