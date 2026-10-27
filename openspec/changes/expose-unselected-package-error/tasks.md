# Tasks

## 1. Package owner

- [x] 1.1 核对公开查询、错误惯例与持久读取边界，确认无已有结构化缺省入口，冻结最小方案。
- [x] 1.2 用真实签名包回归验证安装未选择/重开、选择保留 disabled 状态及失败不误报，再实现单一 sentinel 分支与 API 文档。
- [x] 1.3 执行相关 integration/race/vet、全仓验证与严格规格检查，独立 Review 公开分类。
- [x] 1.4 只提交本写集并发布，提供不可变 commit、版本和 changelog；不修改消费者或 VM。

## 验证与发布

仅记录实际执行结果；消费者的生产启动恢复策略及 module 升级不属于本 change 的实现。

### 本机验证

darwin/arm64，Go 1.27.3：

- 新回归在仅声明 sentinel、尚未修改 Selected 分支时，实际于空存储与真实安装未选择场景失败；接入 `%w` 后，安装/重开/显式选择/disabled entry/关闭优先级与两类真实 Open 读取失败的 targeted integration/race 连续 10 次通过。
- `bash conformance/run.sh` 通过：全仓 integration/race、独立 native/WASI 作者包构建、签名双实例/回调/事件/配置/版本切换 smoke，以及 native/WASI lifecycle smoke。部分未改包命中 Go 测试缓存，不把缓存重复当作新执行。
- 随后另行执行 `go test -race -count=1 -timeout=3m ./...` 与 `go test -tags=integration -race -count=1 -timeout=5m ./...`，全部通过且不复用测试缓存。
- `go vet -tags=integration ./...`、`go mod verify`、`go mod tidy -diff` 通过，module 无变化。
- 默认 Python 缺少外部 jsonschema，未把首次运行记为成功；改用 `uv run --no-project --with jsonschema==4.26.0 python conformance/validate-schemas.py` 后，4 个 metaschema、70 个结构向量和 1 个签名字节/hash 向量通过，代码级向量与 Ed25519 验签仍由上述 Go 测试执行。
- `openspec validate --all --strict`：5 个 change 通过；另两个既有 change 的归档来源 INFO 仍在，不是本 change 的失败。
- `npm run docs:test`、`npm run docs:build`、`npm run docs:sync` 通过。广义 packagehost 路径触发 9 页基线提醒：已核对 API/概念/入门/CLI/Host/运维/恢复/故障处理的原声明不变，只更新 packages 的查询语义；packages 的 codeCommit 在实现提交产生后固定到包含新 API 的提交。

没有运行 Windows/VM 或消费者回归，不扩展已有平台认证结论。

### 独立 Review

非作者只读审查固定实现提交 `063f92ae95335e8579ca5056a900ecac2a7451b6` 相对 `eee6f6be8565901d92a66ce61c230d4c954ba647`，未发现可确认缺陷或阻塞项。确认没有遗漏已有公开 typed getter，Selected 关闭优先级和 get 路径未变，新的分类仅来自缺映射分支。

审查者在 darwin/arm64、Go 1.27.3 独立运行 5 项 packagehost integration/race 10 次，含安装/选择/重开、关闭、真实读取失败、返回值独立复制和 Snapshot 篡改/信任检查；另运行 5 项 Host integration/race 回归及 `go vet -mod=readonly -tags=integration ./packagehost .`，全部通过。Host 测试使用 fixture runtime，不作为该审查实际运行 native/WASI 或 Windows 的证据。审查没有修改仓库或消费者，未重复作者全仓门禁；公开下载验证另行记录。

## Changelog

- 新增 `packagehost.ErrNotSelected`：`Store.Selected(id)` 仅在该 ID 没有显式选择映射时包装该 sentinel，宿主可用 `errors.Is` 分流。
- 无选择不证明已安装；Store 关闭等原错误保持，Open/Snapshot 的持久化与内容失败不被伪装成首次启动。
- 现有 getter 签名、安装记录、已选择版本和 disabled entry 行为不变；不添加自动选择/恢复或新的状态读取策略。
- 无权限、生命周期、worker、schema、签名字节、持久格式和依赖更改；消费者 module 升级由其 owner 负责。

## Public Publication

实现提交为 `063f92ae95335e8579ca5056a900ecac2a7451b6`（8 个文件，仅本写集）。随后文档提交 `c3385310882c0b0b4929bb9d688b39274ff4fb13` 将 packages 基线绑定到该实现并记录独立 Review；两提交均已推送公开 `Cyenoch/cordis` main。可消费的固定版本为：

```text
github.com/Cyenoch/cordis v0.0.0-20261027133504-c3385310882c
```

- Origin Hash：`c3385310882c0b0b4929bb9d688b39274ff4fb13`
- Module sum：`h1:X9dlTcaXssTUxHqxATy7SrrKei2kkfHhv3O5Z2xXHDQ=`
- go.mod sum：`h1:91iVpKZWCSLKhAQegi8uFDqoVt2qyHnvyyy5s45eXbU=`

使用独立临时消费者目录和全新 GOMODCACHE，明确设置 `GOWORK=off`、`GOPROXY=https://proxy.golang.org,direct`、`GOSUMDB=sum.golang.org`（未禁用校验）实际解析、下载并通过 `go mod verify`、`go mod tidy -diff`。没有 replace、宿主目录依赖或全局配置更改。外部消费者直接导入公开 `packagehost.ErrNotSelected`，实际以 race 验证无映射分类经过外层包装仍可匹配，关闭错误优先且不匹配缺省。

在下载的只读 module 中，另执行本次 3 项真实 Store integration/race 回归与 packagehost integration vet，全部通过；新语义可由公开版本消费，不只是工作树编译成功。最终 packages 基线调整后，文档测试、构建/链接检查和全部严格规格检查再次通过。

本记录自身为发布后的纯文档回填，不改变上述已经公开验证的版本。未代替消费者升级或运行消费者/Windows/VM；未将远程 CI、后续消费者结果或产品认证预填为通过。
