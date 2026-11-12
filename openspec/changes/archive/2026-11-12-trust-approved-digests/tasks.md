## 1. 信任校验（packagehost/format.go 与 packagehost 测试）

- [x] 1.1 先写失败测试：`TestStoreApprovedDigestOutranksUnknownSignature` 在未修改的代码上因 `untrusted ... signing key "stranger"` 失败；`TestTrustPolicyDigestApprovalAndSignatures` 中摘要被批准的签名用例失败
- [x] 1.2 `TrustPolicy.verify`：发布者检查之后，摘要被批准即信任，不读签名；两项测试通过（`go test -tags=integration -race ./packagehost/...`）
- [x] 1.3 用例覆盖：未知密钥、已撤销密钥、受信任密钥验证失败，各自有无摘要批准；未签名有无批准与批准其他摘要；受信任密钥的有效签名；发布者撤销与未授权时批准无效；撤回批准后快照被拒绝

## 2. 文档（docs/packages.md、CHANGELOG.md）

- [x] 2.1 `docs/packages.md` 新增"信任"一节：信任条件、摘要批准不查看签名、发布者记录与 `PublisherChanges`、每次快照重新校验
- [x] 2.2 `CHANGELOG.md` 未发布一节记入行为变化
- [x] 2.3 `docs/cli.md` 改写"已签名但未被信任的包不会由 `-dev-unsigned` 绕过坏信任"：`-dev-unsigned` 批准本次产物的精确摘要，签名不影响结果，`signature.json` 格式错误仍拒绝；`docs/getting-started.md` 的"正式交付需要签名"改为由 TrustPolicy 明确授权；`Inspect` 注释去掉只用于开发的说法
- [x] 2.4 `docs/packages.md` 改写撤销语义（摘要批准与有效受信任签名都不成立时快照才拒绝；撤销发布者或撤回包 ID 授权总是先拒绝），并写明格式检查先于信任、摘要批准只跳过签名的信任与密码学验证

## 3. `DevDigests` 改名为 `Digests`

- [x] 3.1 `TrustPolicy.DevDigests` 改名为 `Digests`，不留别名；`verify` 删去 `development` 参数；更新 `cmd/cordis/run.go`、`cmd/cordis/dev.go`、`plugintest/harness.go`、`packagehost/store.go`、`packagehost` 与 `hosting/fixture_test.go` 的调用方；确认 CLI 信任文件没有摘要字段
- [x] 3.2 `CHANGELOG.md` 记入破坏性改名

## 4. 格式边界测试

- [x] 4.1 `TestStoreApprovedDigestRejectsMalformedSignatureFile`：摘要被批准，同一内容带不是 JSON、重复字段或未知字段的 `signature.json`，安装失败且不装入任何包
- [x] 4.2 `TestTrustPolicyDigestApprovalAndSignatures` 增加用例：其他发布者的密钥、签名值不是 Base64，各自有无摘要批准

## 5. 收尾

- [x] 5.1 `openspec validate trust-approved-digests --strict`、`go vet ./...`、`go test ./...`、`GOOS=windows go vet ./packagehost/...` 通过
- [x] 5.2 非作者独立审查（三项 MINOR 与改名建议已在 2.3、2.4、3、4 处理）
- [x] 5.3 修正后的复审
