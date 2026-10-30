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

## Final local checks

后三项实现现已接入。2026-10-30 在 darwin/arm64、Go 1.27.3 的最终工作树再次执行完整 conformance/race、vet、module verify、docs:test、docs:examples、25 页 docs:build、严格 OpenSpec、schema（96 结构向量）、actionlint 和 diff 检查，均通过。控制与恢复的独立审查修正见各自 verification。

五平台最终实现、公开 prerelease 和归档结果见下方。

## Verified implementation

2026-10-30，最终实现提交 `e2181fab165b0571c83e85a1d6646060b18d9c3a`。[Conformance](https://github.com/Cyenoch/cordis/actions/runs/41012551756) 的五个平台均在对应原生 runner 上执行 integration/race、真实 native 验收、vet 和 module verify；包含多包开发、本机控制和有限恢复，不是交叉编译结论。Contracts job 的 schema 与严格 OpenSpec 检查通过。

| 平台 | CI job | 结果 |
|---|---|---|
| macOS amd64 | [123921460216](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460216) | 通过 |
| Linux arm64 | [123921460484](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460484) | 通过 |
| Linux amd64 | [123921460568](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460568) | 通过 |
| macOS arm64 | [123921460661](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460661) | 通过 |
| Windows amd64 | [123921460717](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460717) | 通过 |

同一提交的[Documentation](https://github.com/Cyenoch/cordis/actions/runs/41012551777) 通过文档测试、可执行示例和构建，并已部署 GitHub Pages。

## Published prerelease

[v0.1.0-alpha.1](https://github.com/Cyenoch/cordis/releases/tag/v0.1.0-alpha.1) 已公开，标记为 prerelease，非 draft；未设为稳定最新版。tag 固定到 `e2181fab165b0571c83e85a1d6646060b18d9c3a`。[Release workflow](https://github.com/Cyenoch/cordis/actions/runs/41013139380) 再次完成五平台 conformance、文档检查、五平台 CLI 归档及解压后 version/help 执行，全部通过后发布五份归档和 SHA256SUMS。

实际从公开 Release 下载全部六个资产，独立核对五份归档的 SHA-256、精确文件集合、MIT LICENSE、RELEASE.txt 中的版本/平台/提交以及 POSIX 执行位。下载的 darwin/arm64 CLI 实际执行 version/help，通过并报告 `vcs.modified false`；其余平台的执行证据来自各自 Release runner，不冒称本机执行。

| 归档 | SHA-256 |
|---|---|
| `cordis_v0.1.0-alpha.1_linux_amd64.tar.gz` | `cc737b239b7cc9dc75a5963b0b670422a2899e4b9979bf6c3047d0246a22d530` |
| `cordis_v0.1.0-alpha.1_linux_arm64.tar.gz` | `789768749e035dcf288d75ce0836ba83990892409825e11637d14c25bb879585` |
| `cordis_v0.1.0-alpha.1_darwin_amd64.tar.gz` | `4e2e55a91d7264236bcb6b5f0a9e6df211574e0e4e06d84dcca52c2f47d62427` |
| `cordis_v0.1.0-alpha.1_darwin_arm64.tar.gz` | `08c9b40be6dfa4741382d6e05360fc123fcbeb63b1cfda7395bc533f5c63b9db` |
| `cordis_v0.1.0-alpha.1_windows_amd64.zip` | `bc6a1da0b0fec30a677559fe0c10f45cff043db3e953c460896464a1be1a27db` |

## Archive

2026-10-30：7/7 项任务完成，主规格已同步并通过严格校验，按 release → dev → control → recovery 的依赖顺序归档。归档及证据通过后续文档提交交付；发行 tag 保持指向已验收的实现提交。
