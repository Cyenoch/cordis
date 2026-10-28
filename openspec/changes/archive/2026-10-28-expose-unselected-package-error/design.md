# Design

## Context

当前 `Store.Selected` 在互斥锁内先检查关闭状态，再查询内存 `catalog.Selected`；缺映射时返回 `fmt.Errorf("package %s has no explicit selection", id)`，有映射则调用既有 `get`。公开 Host.Store 可访问同一 Store，但不存在结构化缺省 getter 或 sentinel。持久 catalog 在 Open 读取/校验，已提交 content 目录在 recover 检查；Selected 不承诺刷新磁盘或重新验签，Snapshot 仍拥有执行前内容/信任核验。

## Decisions

沿用已有 `ErrNamespaceMissing` 等 sentinel 的公开错误惯例，在 packagehost 增加 `ErrNotSelected = errors.New("package has no explicit selection")`。仅将 Selected 的缺映射分支改为带 ID 上下文的 `%w` 包装，保持函数签名与其他返回路径。宿主用 errors.Is 决定是否进入其显式初选/恢复流程；若一个从未安装过的 ID 没有选择，同样匹配，此错误不证明安装存在。

不将 get、Open、Snapshot 的错误统一包装为该 sentinel，不改变 Store.check 的关闭优先级。不给 SaveEntry、依赖预检等其他错误附加未选择分类；本接口只解决公开 getter 的确定性分支。

## Alternatives

- 新增 `(ref, found, err)` getter：会引入第二查询入口和调用迁移，当前单 sentinel 足够。
- 解析旧错误文本：不稳定且迫使宿主复制框架实现知识，拒绝。
- 任意 error 当缺省：会掩盖关闭/损坏/内容丢失并覆盖用户选择，拒绝。
- 查询时读盘并自动修复：改变既有快照与恢复边界，不属于本需求。

## Validation

复用既有 integration 真实签名包与 Store helper：安装未选择后关闭/重开，断言 sentinel 与安装内容保留；显式选择并保存 disabled entry 后重开，断言原 ref 和 enabled=false。另以关闭后查询以及真实 catalog 读取错误/committed content 丢失的 Open 失败，验证 errors.Is 不误报缺省。测试只使用自有临时目录，不依赖权限 chmod 或用户存储。

先运行新回归证明原实现缺分类失败，再实现；执行 packagehost integration/race、相关 Host 回归、全仓门禁与 OpenSpec 严格验证。公开错误语义由非作者 Review，修复后发布固定版本；消费者自己升级，不添加本地 replace。
