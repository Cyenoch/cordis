## 1. 推导与生成

- [x] 1.1 `schema.Codec[T]` 与端点、客户端用的泛型调用函数
- [x] 1.2 `schema/gocontract`：加载包、解析指令、类型到 schema
- [x] 1.3 `schema/gocontract`：生成 `cordis_gen.go` 与 `cordis_gen.json`
- [x] 1.4 `sdk.Define` 的配置解码不再做数据类型检查
- [x] 1.5 schema 语言与两个生成器支持映射

## 2. 接入工具链

- [x] 2.1 `packagehost`：没有契约键的作者声明读 `cordis_gen.json`；构建先生成
- [x] 2.2 `cordis generate [目录]`、`cordis init` 脚手架、`cordis dev`
- [x] 2.3 声明式生成器补上 `Provide<Service>`、`Get<Service>`

## 3. 示例、文档与验证

- [x] 3.1 示例插件改为 Go 写契约
- [x] 3.2 文档
- [x] 3.3 独立审查、`verification.md`
- [x] 3.4 五平台 CI
