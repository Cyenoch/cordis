# Spec Delta

## ADDED Requirements

### Requirement: Windows 非阻塞 stdin 有界读取
Windows WASI worker SHALL 在 stdin 可读积压超过单个 guest 目标缓冲区时，仅读取该缓冲区范围内的字节；剩余数据 MUST 留待后续读取，不得 trap、丢字节或因限制读取量把正常数据误报为 EOF。

#### Scenario: 小缓冲区读取大积压
- **WHEN** stdin 已积压大于 4096 字节的数据，而非阻塞 guest 以 4096 字节缓冲区反复读取
- **THEN** 每次返回不超过该容量，全部数据按序且完整可恢复，worker 不因宿主 slice 越界退出

### Requirement: Windows stdin 调度与权限保留
读取修复 SHALL 保留非阻塞 EAGAIN、poll/HANGUP、关闭 fd 拒绝及宿主终止行为；非 stdin 操作 MUST 继续受既有文件表约束，失败不扩大能力或泄露 trap 原因。

#### Scenario: 暂无数据后继续运行
- **WHEN** 非阻塞 stdin 尚无数据且 writer 仍打开
- **THEN** guest 通过 WASI ABI 观察到 AGAIN 错误（编号 6），不阻塞调度，也不会将内部错误编号误读为权限拒绝；之后可继续消费到达的数据

#### Scenario: 关闭与非法 guest 内存
- **WHEN** writer 关闭、guest 已关闭 stdin 或 guest 提供越界的读取描述
- **THEN** 分别按 EOF、坏 fd 或内存错误返回；非法描述不消费 stdin，关闭 fd 后不能继续读取宿主流

#### Scenario: 取消等待中的受管插件
- **WHEN** guest 等待 stdin 或正在运行，而宿主取消并关闭受管 worker
- **THEN** worker 按既有终止流程退出，不新增无限等待、后台读取者或业务重试
