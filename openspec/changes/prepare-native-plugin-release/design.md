# Design

## Context

基线 `1d640b0`；动机见 proposal。`packagehost/archive.go:Pack` 先复制整目录，再由 `stageAuthor` 验证构建描述并生成机器 Manifest。`Inspect` 已验证完整性但只返回 Package。现有五平台 conformance 和 Pages workflow 可复用，仓库尚无发行 tag。

## Goals / Non-Goals

保持作者输入、校验快照与发行文件三个集合清晰。签名算法不变，不靠猜测敏感文件名过滤内容。只交付预发布；不承担 Apple notarization、Windows 商业代码签名或自动安装器。

## Decisions

### 1. 精确资源清单

`contract.Manifest.Resources []string` 与作者源 `resources` 表示额外运行普通文件；省略即空。只接受精确包内路径，不实现 glob 或目录递归。复用现有路径规范、大小配额和链接检查，拒绝重复、元数据/产物冲突。机器 Manifest 保留资源清单，重打包可得到同样集合。

schema 与 Go 同时修改。资源变化进入已有 author fingerprint；更新资源声明必须重建，资源内容由打包完整性表保护。构建证据不替代运行资源内容摘要。

### 2. 先校验输入快照，再产生发行目录

作者 loader 记录实际访问的声明/契约文件，用于构造有限输入闭包。Pack 只复制作者源、递归契约、build.json、其声明的生成代码、所有目标产物和资源；机器 Manifest 模式只复制 Manifest、产物和资源。使用 `os.Root` 与已有普通文件/链接/配额检查，不遍历无关目录。

在私有快照重新加载并验证，避免把第一次解析的源信息与后来不同的内容混用；若依赖清单变化造成文件缺失，明确失败。沿用 stageAuthor 的完整证据验证，之后只保留机器 Manifest、产物和资源，再生成 integrity 并原子写 ZIP。不要改动用于安装和运行快照的通用 copyDirectory 行为。

不通过“.gitignore 或默认秘密扩展名黑名单”实现发布选择，因为它们无法表达运行资源，也不能防止未知名字的私钥混入。

### 3. Inspect 输出

新增 `packagehost.Inspection`，内嵌 Package 并附 `Files []IntegrityEntry`；Inspect 返回 Inspection。清单来自已验证内容，补充 integrity/signature 元数据自身的大小与摘要，按路径排序；元数据自身摘要不递归写入签名输入。包 catalog 继续存 Package，避免持久重复整个文件表。调整所有显式类型调用者、CLI 与文档，未发布契约不保留旧包装。

### 4. CLI 发行

沿用 `debug.ReadBuildInfo`，增加仅发布构建注入的 version/commit 值；开发构建仍显示真实开发元数据。以标准库 Go 发布脚本打包五平台 CLI、LICENSE、简短发行说明并生成校验和，产物文件名包含 tag、OS、arch。Windows ZIP，其余 tar.gz，文件执行位正确。

发布 workflow 从明确 tag 构建，在对应五平台运行新代码完整 conformance 及归档内 CLI version/help。收齐产物并验证提交和校验和后先生成 draft release、上传完整集合，再公开；已有公开 tag/资产拒绝覆盖。失败 draft 可明确重跑完成，不发布半套资产。

四项实施最终验证后创建 `v0.1.0-alpha.1`（若名称已占用先核对，不覆盖），明确 prerelease。对最终实际发布提交记录 CI 和下载证据。实现期各批次可先提交推送取得平台证据，最后统一交付。

native 已使用私有 cwd 并复制可执行文件，不能靠相对 cwd 或 os.Executable 定位资源。运行时注入保留环境变量 `CORDIS_PACKAGE_DIR` 为 launch.Directory 的绝对路径；与显式 Environment 冲突时启动前拒绝。继续由 Host 的 snapshot 生命周期持有目录直至进程和清理完成，不新增文件服务/SDK 资源客户端，插件直接用标准库读取。该目录不得用于持久业务写入。

### 5. 模块及验证

包内容逻辑留 `packagehost`，公共字段在 `contract`/schema；发布脚本只组合 Go build 和归档，不进运行时。测试关注误夹带、必要资源可读、证据漂移拒绝、旧输出不变、inspect 可信清单。示例使用临时测试密钥和已有签名/信任路径，至少两目标构建，本机实际运行。发布脚本另验证归档内容、版本及可执行性，不为 YAML 文本写镜像断言。

## Risks / Trade-offs

- 精确资源路径较长 → 第一版保持可读可检查；目录或匹配规则有实际需求再设计。
- 输入可能在复制时变化 → 私有快照重验声明与构建证据，失败保留旧 archive；不宣称整个源构建为事务。
- Release 依赖外部 CI → 记录真实状态，失败不归档成已完成交付；绝不使用历史提交结果冒充。

## Migration Plan

项目未发布，直接更新 Manifest/schema/示例。已有作者需要显式声明运行资源后重建；不迁移生产 Store 或保留整目录模式。现存已经签名的内容不就地改写。
