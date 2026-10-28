# Proposal

## Why

生产宿主必须区分“安装后尚未显式选择”与存储失败，才能显式恢复 Install → Select 之间的中断，并保留已有选择和停用状态。目前 `Store.Selected` 在缺少选择映射时只产生普通字符串错误，没有可公开匹配的缺省分类；消费者不得解析错误字符串或把任意错误当作首次启动。

## What Changes

- 新增 `packagehost.ErrNotSelected`，仅在打开的 Store 没有所查询 ID 的显式 selection 映射时由 `Selected` 包装返回，可通过 `errors.Is` 匹配。
- 保留 `Selected(id) (Package, error)` 签名、选中结果、锁与无副作用语义；不自动选择最新版本、安装、启用或恢复。
- 关闭的 Store、现有选择解析失败以及 Open/Snapshot 的持久化或内容失败不转换为未选择。
- 用真实签名安装、重开、选择、关闭及损坏存储验证分类；更新包 API 说明。

非目标：新增第二套 getter、Host 缺省策略、自动选择/恢复、权限或生命周期更改、磁盘格式和依赖升级。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-packages`：选择查询的结构化缺省语义，不混淆存储失败与未选择。

## Impact

写集为 `packagehost/store.go`、既有 Store 集成测试、`docs/packages.md` 和本 change。不修改消费者、go.mod/go.sum、worker 或其他并行开发。
