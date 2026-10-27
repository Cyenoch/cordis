# Spec Delta

## ADDED Requirements

### Requirement: 本地运行默认期限覆盖 WASM 冷启动
`cordis run` 的默认启动期限 SHALL 不短于 WASM runtime 的默认编译期限，使默认配置下合法的冷编译不会被本地运行提前判为启动失败。期限仍 MUST 有限、可通过 `-startup-timeout` 配置且必须为正。

#### Scenario: 较慢机器上首次运行 WASM 插件
- **WHEN** 作者未设置启动期限，在较慢机器上运行需要冷编译的 WASM 包
- **THEN** 冷编译与启动合计在默认期限内完成时调用成功，不因本地运行更短的默认值失败；缺少 provider 时仍在默认期限到达后有限失败
