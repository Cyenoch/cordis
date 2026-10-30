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

2026-10-30：7/7 项任务完成，主规格已同步并通过严格校验，按 release → dev → control → recovery 的依赖顺序归档。首轮归档已通过 `abd85fb4df4237617b78a97b44d78cf3ede5ae60` 提交推送；发行 tag 各自保持指向已验收的实现提交。

## Linux lifecycle follow-up

归档提交 `abd85fb4df4237617b78a97b44d78cf3ede5ae60` 的 [Linux amd64 job](https://github.com/Cyenoch/cordis/actions/runs/41014380025/job/123927270623) 在 `TestNativeIsolationRestoreAndChildBoundaries` 启动私有副本时返回 `text file busy`。这不能用先前成功的发布验收代替处理；alpha.1 资产保留，Release 说明已记录已知问题。

本机 Linux arm64 容器、Go 1.27.3 复现：并发复制并启动 1600 次，原逻辑出现 275 次 ETXTBSY。即使父进程已关闭写 FD，并发 fork 的子进程仍可能在 exec 前持有继承副本。复制目标可写期间持有 Go 已有的 ForkLock 读锁，关闭后释放，阻止这一继承窗口；同一复现保护后为零错误，不增加启动重试。生产复制函数对应的新回归在未修复时立即失败，修复后连续 20 次通过。

进一步的 Linux 全量检查还暴露构建取消后的后代端口未关闭。单独复跑取消测试 30 次通过，但新的受控场景证明原 Unix Tree.Close 在 leader 已退出、后台子进程仍运行时直接返回。Linux 现在等待本进程组无存活成员，等待期限沿用 Windows 已有的 30 秒；忽略资源已释放、仅待 init 回收的僵尸，同时检查剩余线程数，不将 leader 的退出等同于全部后代退出。

Wegener 独立审查复制与 ForkLock 边界，未发现生产缺陷；发现测试复制 BusyBox 后改变 argv[0] 会误报失败，已修正为实际执行新文件但保留 applet 名 `true`。进程组完成检查和后续交付结果见下方；alpha.1 的 tag 与资产不变。

修复后的本机实际结果：Linux arm64 容器、Go 1.27.3 中，两项新回归在 race 下各运行 20 次，构建取消测试运行 30 次，随后完整 `bash conformance/run.sh` 全部通过；darwin/arm64 的 runtime/packagehost integration/race 与全量 vet 通过。严格 OpenSpec 和 diff 检查通过。容器结果不代替后续 Linux amd64 与其他平台的提交级 CI。

将修正 argv[0] 后的测试交叉编译为 Linux arm64，在现有 Alpine/BusyBox 容器中实际执行五次，全部通过；该项未开启 race，不冒称为 race 结果。

Carver 独立审查进程组边界，复现受限 `/proc` 隐藏活成员时可能吞掉 EPERM，以及打开 stat 后进程消失导致读取 ESRCH。已保留 Linux 权限错误，将 EPERM 僵尸例外限于原有 Darwin 路径；非完整可见的 hidepid 挂载明确报告无法确认排空，ESRCH 与 ENOENT 一样继续检查其余成员。临时只读 Linux 容器在 hidepid=2、移除 CAP_KILL 后直接执行修正后的 Tree.Kill/Close，确认隐藏视图不假报成功、EPERM 被保留；这两项专门验证未开启 race。

上述修正之后再次通过 Linux arm64 的两个 race 回归各 20 次、完整 conformance 和 vet；文档测试与构建通过。全程未替换 alpha.1 的发行资产。

## Alpha.2 implementation acceptance

修复提交 `1b9f444e0ddcbeab8fc41e10611fd5f66341e6ce` 的[五平台 Conformance](https://github.com/Cyenoch/cordis/actions/runs/41020299506) 全部通过，包括新的 Linux 并发复制/启动与进程组清理回归；[Documentation](https://github.com/Cyenoch/cordis/actions/runs/41020299637) 的示例、构建与 Pages 部署通过。

| 平台 | CI job | 结果 |
|---|---|---|
| Linux amd64 | [123945847437](https://github.com/Cyenoch/cordis/actions/runs/41020299506/job/123945847437) | 通过 |
| Linux arm64 | [123945847567](https://github.com/Cyenoch/cordis/actions/runs/41020299506/job/123945847567) | 通过 |
| macOS amd64 | [123945847477](https://github.com/Cyenoch/cordis/actions/runs/41020299506/job/123945847477) | 通过 |
| macOS arm64 | [123945847876](https://github.com/Cyenoch/cordis/actions/runs/41020299506/job/123945847876) | 通过 |
| Windows amd64 | [123945847290](https://github.com/Cyenoch/cordis/actions/runs/41020299506/job/123945847290) | 通过 |

## Alpha.2 published assets

[v0.1.0-alpha.2](https://github.com/Cyenoch/cordis/releases/tag/v0.1.0-alpha.2) 已公开为 prerelease，固定提交 `1b9f444e0ddcbeab8fc41e10611fd5f66341e6ce`。[Release workflow](https://github.com/Cyenoch/cordis/actions/runs/41020930952) 的五平台真实验收、文档及全部 CLI 构建/归档内执行通过。

再次从公开 Release 下载五份归档和 SHA256SUMS，全部 SHA-256、MIT 许可、版本、平台、来源提交和 POSIX 执行位核对通过。实际执行下载的 darwin/arm64 CLI version/help，输出正确 alpha.2 版本和提交且 `vcs.modified false`；其他四个平台的执行由各自 Release job 验证。alpha.1 资产保持原样，其说明已推荐升级 alpha.2。

| 归档 | SHA-256 |
|---|---|
| `cordis_v0.1.0-alpha.2_linux_amd64.tar.gz` | `260bc0fc1a1b3c0e7677d67752abd058ddb60ea4c18559e7012d73afecb6a00c` |
| `cordis_v0.1.0-alpha.2_linux_arm64.tar.gz` | `59a1cc52ace74ae7c09501b917295a737f2ce6869157e0dea6cb840248cf1b33` |
| `cordis_v0.1.0-alpha.2_darwin_amd64.tar.gz` | `60863088e478bc9f78628bd1d3706dd477769820dcf84ff002be818545999db8` |
| `cordis_v0.1.0-alpha.2_darwin_arm64.tar.gz` | `d9565c62073a56445fef0d72e012e3f059e145efafbfc7448a988cdc87fc2142` |
| `cordis_v0.1.0-alpha.2_windows_amd64.zip` | `8a96afcb52586a46b9ac7219bb4ee81052d98116c31ca1b2b505c2c322668d08` |
