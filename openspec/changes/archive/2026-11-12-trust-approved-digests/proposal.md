## Why

`TrustPolicy.DevDigests` 只在包没有 `signature.json` 时才被查看。宿主精确批准了一个第三方包的摘要，而这个包恰好带着宿主不认识的密钥签名，安装就被拒绝；删掉 `signature.json` 后，同一摘要的包却能装上。签名文件不在摘要覆盖范围内，拒绝带签名的副本保护不了任何东西。不用签名的宿主（例如由用户逐个确认第三方包）需要精确摘要批准本身就足以建立信任。

## What Changes

- **行为变化**：摘要被批准时，信任不再验证 `signature.json` 中的密钥与签名：签名缺失、密钥未知或已撤销、属于别的发布者、签名值无法解码或验证失败都不影响结果。包仍先通过格式检查，`signature.json` 不是合法 JSON、有重复或未知字段时照旧拒绝。摘要未被批准时规则不变，仍需要受信任、未撤销、属于该发布者的密钥的有效签名。
- **BREAKING**：`TrustPolicy.DevDigests` 改名为 `Digests`，与 `Keys` 并列，不保留旧名；它不再只用于开发。内部 `verify` 删去不再使用的 `development` 参数。
- 发布者规则不变：发布者被撤销，或未获授权提供该包 ID 时，即使摘要被批准也拒绝；Store 已记录的发布者改变时仍需 `PublisherChanges`。
- 选择预检和每次执行快照照旧按当时的信任策略重新校验；更新策略后，精确摘要批准与有效的受信任签名都不成立时，下一次快照被拒绝。
- 包格式、签名格式和 CLI 信任文件格式不变（CLI 信任文件没有摘要字段，不受改名影响）。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-packages`: 精确批准的摘要单独足以建立信任，签名文件不影响它；发布者规则照旧。

## Impact

- `packagehost`（`TrustPolicy` 的字段与校验）、`docs/packages.md`、`docs/cli.md`、`docs/getting-started.md`、`CHANGELOG.md`。
- 改名的调用方：`cmd/cordis/run.go`、`cmd/cordis/dev.go`、`plugintest/harness.go`、`packagehost/store.go` 与 `packagehost`、`hosting` 的测试夹具。
- `cordis run -dev-unsigned` 与 `cordis dev` 用同一字段批准本次产物的摘要，带有未知密钥签名的本地产物现在也能运行；`docs/cli.md` 里"已签名但未被信任的包不会由 `-dev-unsigned` 绕过"一句随之改写。
- 兼容边界：包格式与签名输入不变；字段改名与行为变化属于 `docs/stability.md` 中可在 v0 次版本间调整的 Go API，使用 `DevDigests` 的宿主代码需要改为 `Digests`；按 v0.2.0 写的包照常安装。
- 非目标：不改发布者授权与 `PublisherChanges`，不新增字段。
