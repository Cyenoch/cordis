# Verification

## Implementation batch: package contents and resources

日期：2026-10-30。平台：darwin/arm64，Go 1.27.3。下列结果来自以 `1d640b0` 为基线的本次修改工作区，不是该基线提交的 CI 结果；最终实现 SHA 和远端证据在交付阶段补入。

- `bash conformance/run.sh`：全部包 integration/race 通过；实际独立 native counter、依赖链、更新、取消、恢复和子树清理通过。
- `go vet -tags=integration ./...`、`go mod verify`：通过。
- `bun run docs:test`、`bun run docs:examples`、`bun run docs:build`：通过；23 页链接/锚点/资源检查成功。
- `/tmp/cordis-schema-jsonschema-venv/bin/python conformance/validate-schemas.py`：4 个 metaschema、96 个结构向量和固定签名字节/hash 通过。脚本不执行的代码向量由 Go 验收承担，不把 Python 结果当成签名执行验证。
- `bun run spec:validate`、`git diff --check`：通过。
- `TestNativeReleaseResourceExample`：实际构建示例声明的五个目标，在本机执行签名安装后的资源服务，删除原作者目录后仍读取正确快照内容。其余四个目标仅有本机交叉编译证据，尚不代表相应平台运行。
- 本机 `go run ./scripts/release -version v0.1.0-alpha.1`：生成 darwin/arm64 归档，从归档解压的 CLI version/help 通过，`shasum -a 256 -c` 通过。此产物只作本机 smoke，未公开发布，不是最终发行资产。
- `actionlint .github/workflows/release.yml .github/workflows/conformance.yml`：通过。

## Independent reviews

Linnaeus 审查发布与多包规格；发现清单计划/摘要竞争、显式资源被临时文件名过滤，两项均补入设计与验收场景。Peirce 审查控制/恢复规格；补齐 composition 主契约变更、控制提交不确定分类、历史 activation 与图失败区别、被拒绝人工操作不得取消计划。

Godel 审查包/资源公开契约与实现，复现大小写保留元数据冲突和 CLI 丢弃 Files。已通过统一保留路径冲突检查及实际 CLI 文件清单输出修正，并分别加入回归测试。修正后的定向 race 测试及上述完整 conformance 通过。

Tesla 审查发行脚本、workflow 和版本注入，发现发布前仅检查本地 tag，可能把 A 的产物发布到已移动至 B 的远端 tag。已在上传前重新读取远端 ref 并 peel annotated tag，与验收 HEAD 比较。临时 Git 仓库实际验证轻量/附注 tag 接受、移动的远端 tag 拒绝；actionlint 通过。未操作项目远端 tag。

## Outstanding

后三项实现现已接入。2026-10-30 在 darwin/arm64、Go 1.27.3 的最终工作树再次执行完整 conformance/race、vet、module verify、docs:test、docs:examples、25 页 docs:build、严格 OpenSpec、schema（96 结构向量）、actionlint 和 diff 检查，均通过。控制与恢复的独立审查修正见各自 verification。

五平台实际运行、公开 prerelease、归档与最终提交推送尚未完成。不得用本机结果将这些事项视为已交付。
