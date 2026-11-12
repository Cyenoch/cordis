## MODIFIED Requirements

### Requirement: 离线包与实例意图管理
CLI SHALL 提供 list/install/select/enable/disable/remove/uninstall/status，显式选择 Store 目录；包选择和卸载使用完整包引用，不猜 latest。命令不启动插件；enable 保存或更新校验后的 entry 启用意图，对已有 entry 保留未显式指定的字段，清空须显式输入。disable 持久停用该 entry 及其持久子树，remove 只删除已停用且无持久子项的 entry 意图。status 输出持久意图和最近运行记录并明确当前不是实时状态。目录被占用时 SHALL 拒绝操作，不直接读写其内部文件，错误给出目录并指向本机控制。除 install 外，命令和前台 host MUST 拒绝不存在的目录，不创建它。

#### Scenario: 启用不等于运行
- **WHEN** 在空闲目录执行 install、select、enable
- **THEN** 返回包引用和已保存的 entry；没有创建插件进程，输出不称 Active；随后 status 表明未连接运行中宿主

#### Scenario: 运行期间离线操作
- **WHEN** 前台 Host 持有目录锁，另一 CLI 尝试 status 或 disable
- **THEN** 命令非零退出并说明哪个目录正在使用、Host 运行期间改用本机控制，不绕过锁或谎报停用成功

#### Scenario: 写错的目录
- **WHEN** 用户对一个不存在的目录执行 status、list 或 host
- **THEN** 命令非零退出并说明目录不存在，不创建它，也不报告空的意图或空的宿主

#### Scenario: 不兼容配置的版本切换
- **WHEN** 新旧版本配置 schema 不兼容，用户停用受影响子树、从叶到根 remove 旧意图，再选择新版本并重新 enable
- **THEN** 用户可在不写 Go 宿主、不清空 Store 的情况下使用新配置启动；省略 remove 时旧配置仍阻止 select，不静默丢弃配置

#### Scenario: 重新启用保留已有字段
- **WHEN** 已停用 entry 有配置、父项和隔离声明，用户 enable 时没有重新提供这些字段
- **THEN** 校验并保留原字段；只有显式传入空父项、空隔离列表或新 JSON 才分别替换对应值
