# Spec Delta

## ADDED Requirements

### Requirement: 显式选择缺失可分类
打开的 Store 对没有显式 selection 映射的 ID 执行 `Selected` 时，SHALL 返回可由 `errors.Is(err, packagehost.ErrNotSelected)` 匹配的错误，并保留 ID 定位信息。该分类 MUST 只表示缺少选择，不表示包已经安装、内容完整或允许启用；查询本身不得创建选择或改变 entry 状态。

#### Scenario: 安装后尚未选择并重开
- **WHEN** 宿主实际安装签名包，尚未执行 Select 即关闭并重开存储，然后查询该包的选择
- **THEN** 查询返回 ErrNotSelected 分类，已安装包仍可显式取得，宿主可自行决定是否恢复 Select，不发生隐式选择

#### Scenario: 保留已选择但停用的配置
- **WHEN** 宿主已显式选择包且存在 disabled entry，重开后查询选择
- **THEN** 查询返回原 PackageRef 而非缺省分类，entry 仍为 disabled

### Requirement: 选择缺省不掩盖失败
Store 关闭、catalog 读取/恢复失败以及受管内容读取失败 MUST 保留既有错误，不得标记为 ErrNotSelected 或让消费者误判为可执行初选。Selected SHALL 继续只查询当前 catalog，不新增磁盘读取或自动修复。

#### Scenario: 关闭后查询未知 ID
- **WHEN** Store 已关闭，宿主查询一个没有选择的 ID
- **THEN** 返回原关闭错误且不匹配 ErrNotSelected，关闭状态优先于缺省

#### Scenario: 持久存储不可读取
- **WHEN** 已安装存储的 catalog 无法读取或已提交内容丢失，宿主重新 Open
- **THEN** Open 失败且不匹配 ErrNotSelected，不返回一个假装没有选择的可用 Store
