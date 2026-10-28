---
title: 授权、任务与存储实操
description: 配置受限宿主能力，发布贡献，持久化命名空间，并把任务从插件移交给宿主。
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - go.mod
  - host.go
  - access
  - sdk/task.go
  - contract/task.go
  - examples/plugins/chain
---

# 授权、任务与存储实操

这个程序只使用公开 `access` API：授予一个只能查询 `profile` 的能力，发布一条贡献，写入并重开持久存储，再把任务交给宿主。撤销激活后，贡献消失，已移交任务仍可完成。

这里由程序创建可信身份，单独展示访问层。真实外部插件的身份由 Host 会话绑定，不接受请求自报；Host 已经管理 Policy、Broker、贡献和任务，不要另建一套互不关联的 Policy。

## 准备与运行

按[组合实操](composition-tutorial.md)的方式，在空 module 中用绝对 `replace` 指向 Cordis 检出。保存以下 `main.go`，执行 `go mod tidy` 和 `go run .`。本例不启动插件进程，也不证明 Native 或 WASM 的隔离行为。

## 完整程序

```go title="main.go"
package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "slices"
    "strings"
    "time"

    "github.com/Cyenoch/cordis/access"
    "github.com/Cyenoch/cordis/contract"
)

type Scope struct {
    Resources []string `json:"resources"`
}

type Query struct {
    Resource string `json:"resource"`
}

type MenuItem struct {
    Label string `json:"label"`
}

func main() {
    if err := run(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func run() (result error) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    directory, err := os.MkdirTemp("", "cordis-access-")
    if err != nil {
        return err
    }
    defer func() { result = errors.Join(result, os.RemoveAll(directory)) }()
    policy := access.NewPolicy()
    owner := contract.Identity{
        Package: "tutorial", Entry: "main", Activation: contract.NewID(), Digest: "builtin",
    }
    capability := contract.Capability{
        ID: "tutorial.read", Scope: json.RawMessage(`{"resources":["profile","settings"]}`),
    }
    if err := policy.Activate(owner, []contract.Capability{capability}); err != nil {
        return err
    }
    if _, err := policy.Grant(owner, capability.ID, json.RawMessage(`{"resources":["profile"]}`)); err != nil {
        return err
    }
    broker := access.NewBroker(policy)
    if err := broker.Register(capability.ID,
        func(_ context.Context, _ contract.Identity, declared, granted, input json.RawMessage) error {
            var declaration, grant Scope
            var query Query
            if err := contract.DecodeData(declared, &declaration); err != nil {
                return err
            }
            if err := contract.DecodeData(granted, &grant); err != nil {
                return err
            }
            if err := contract.DecodeData(input, &query); err != nil {
                return err
            }
            if !slices.Contains(declaration.Resources, query.Resource) || !slices.Contains(grant.Resources, query.Resource) {
                return contract.Fail(contract.Denied, "resource outside scope")
            }
            return nil
        },
        func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error) {
            return json.RawMessage(`{"visible":true}`), nil
        }); err != nil {
        return err
    }
    response, err := broker.Call(ctx, owner, capability.ID, json.RawMessage(`{"resource":"profile"}`))
    if err != nil {
        return err
    }
    fmt.Println("allowed:", string(response))
    if _, err := broker.Call(ctx, owner, capability.ID, json.RawMessage(`{"resource":"settings"}`)); err == nil {
        return errors.New("settings should be denied")
    }
    fmt.Println("settings denied")

    contributions := access.NewContributionStore(policy)
    if err := contributions.Register("menu", func(_ context.Context, _ contract.Identity, item contract.Contribution) error {
        var data MenuItem
        if err := contract.DecodeData(item.Data, &data); err != nil {
            return err
        }
        if data.Label == "" {
            return contract.Fail(contract.Invalid, "menu label is required")
        }
        return nil
    }); err != nil {
        return err
    }
    batch, err := contributions.Stage(ctx, owner, []contract.Contribution{{
        Slot: "menu", ID: "open", Data: json.RawMessage(`{"label":"Open"}`),
    }})
    if err != nil {
        return err
    }
    if err := batch.Commit(); err != nil {
        return err
    }
    fmt.Println("contributions:", len(contributions.Snapshot()))

    namespaces, err := access.OpenNamespaceStore(policy, directory)
    if err != nil {
        return err
    }
    namespaceConfig := access.NamespaceConfig{
        Name: "tutorial-data", QuotaBytes: 4096, DataVersion: 1,
    }
    if err := namespaces.Configure(owner.Package, namespaceConfig); err != nil {
        return err
    }
    if err := namespaces.Write(ctx, owner, "tutorial-data", "greeting", []byte("hello")); err != nil {
        return err
    }
    // Only the reopened store is used from here; no concurrent writers.
    namespaces, err = access.OpenNamespaceStore(policy, directory)
    if err != nil {
        return err
    }
    value, err := namespaces.Read(ctx, owner, "tutorial-data", "greeting")
    if err != nil {
        return err
    }
    fmt.Println("reopened:", string(value))
    confirm := func(_ context.Context, namespace, operation string) error {
        if namespace != "tutorial-data" || operation != "migrate" {
            return contract.Fail(contract.Denied, "maintenance not approved")
        }
        return nil
    }
    if err := namespaces.Migrate(ctx, "tutorial-data", 2, confirm,
        func(_ context.Context, previous uint32, values map[string][]byte) (map[string][]byte, error) {
            if previous != 1 {
                return nil, errors.New("migration requires data version 1")
            }
            values["greeting"] = []byte(strings.ToUpper(string(values["greeting"])))
            return values, nil
        }); err != nil {
        return err
    }
    value, err = namespaces.Read(ctx, owner, "tutorial-data", "greeting")
    if err != nil {
        return err
    }
    fmt.Println("migrated:", string(value))
    if err := namespaces.Compact(ctx); err != nil {
        return err
    }
    namespaces, err = access.OpenNamespaceStore(policy, directory)
    if err != nil {
        return err
    }
    // The manifest's initial version does not reset migrated data.
    namespaceConfig.QuotaBytes = 8192
    if err := namespaces.Configure(owner.Package, namespaceConfig); err != nil {
        return err
    }
    value, err = namespaces.Read(ctx, owner, "tutorial-data", "greeting")
    if err != nil {
        return err
    }
    fmt.Println("reconfigured:", string(value))

    tasks := access.NewTaskManager(policy)
    guestCtx, stopGuest := context.WithCancel(ctx)
    defer stopGuest()
    task, err := tasks.Create(guestCtx, owner, "export", contract.NewID())
    if err != nil {
        return err
    }
    if err := task.Start(); err != nil {
        return err
    }
    if err := task.Transfer(ctx); err != nil {
        return err
    }
    stopGuest()
    if err := policy.Revoke(owner); err != nil {
        return err
    }
    if err := task.Context().Err(); err != nil {
        return fmt.Errorf("host task canceled by guest: %w", err)
    }
    fmt.Println("after revoke:", len(contributions.Snapshot()), "contributions")
    if err := task.Finish(access.TaskCompleted, json.RawMessage(`{"rows":3}`), nil); err != nil {
        return err
    }
    fmt.Println("host task:", task.Status().State, string(task.Status().Result))
    if err := task.Release(); err != nil {
        return err
    }
    fmt.Println("retained tasks:", len(tasks.Snapshot()))
    return nil
}
```

```text title="预期输出"
allowed: {"visible":true}
settings denied
contributions: 1
reopened: hello
migrated: HELLO
reconfigured: HELLO
after revoke: 0 contributions
host task: completed {"rows":3}
retained tasks: 0
```

## 接入真实 Host

在 `cordis.Options.Authorize` 中决定给哪些 capability 发 grant；返回 `nil, nil` 表示不授予。它不是每次请求的范围校验，具体输入仍必须经过 `host.Broker().Register` 的 scope handler。能力 scope 是宿主自定的数据格式，框架不解释上面的 `resources` 字段。

注册 contribution validator 后再 Enable 或 Restore。外部插件可用 `(*sdk.Client).Contribute` 在初始化时登记贡献；Host 把贡献和服务发布绑定到同一激活可见点。本例未连接 core，`batch.Commit` 就是可见点；不要将这一步搬到真实 Host 的任意业务 handler 中绕过发布门控。

namespace 不会自动成为插件 API。宿主将 `OpenNamespaceStore(host.Access(), directory)` 接到自己的 Broker handler 中，身份使用 handler 取得的可信 owner，不能从 input 解码。目录只由一个 store 写入；框架没有跨进程 namespace 写锁，也不把打开失败转成内存模式。

重开后继续使用清单中的 `DataVersion: 1` 调用 `Configure` 不会把已经迁移到 2 的数据降级；版本只在新建时使用。例子同时把配额从 4096 提高到 8192 并持久化。降低配额也允许，但不能低于现有 key/value 的总字节数，拒绝时原配额和数据不变。

## 外部插件发起任务

在外部 SDK 的服务回调中，`scope` 是 `*sdk.Client`。通过 `BeginTask` 建立任务，再用 `Finish` 提交终态。创建和完成 RPC 都使用从 `scope.Context()` 派生的有期限控制 context，避免单次业务调用取消时丢失确认。每次创建使用新的任务 ID；宿主保留终态记录，SDK 更新 token 并不代表逻辑 ID 可以复用。

```go
client, ok := scope.(*sdk.Client)
if !ok {
    return contract.Fail(contract.Incompatible, "external SDK scope required")
}
beginCtx, cancelBegin := context.WithTimeout(scope.Context(), 3*time.Second)
task, err := client.BeginTask(beginCtx, "export."+contract.NewID())
cancelBegin()
if err != nil {
    return err
}
// 此处省略业务工作；失败使用 failed 和稳定错误码。
finishCtx, cancel := context.WithTimeout(scope.Context(), 3*time.Second)
defer cancel()
return task.Finish(finishCtx, "completed", json.RawMessage(`{"rows":3}`), "")
```

`task.Context()` 随任务取消或 activation 退役而取消，不随单次业务调用的 `ctx` 自动取消。如果工作也应随请求结束，业务代码必须同时观察这两个 context。控制 RPC 也可能因自身超时而丢失确认，遇到错误不能假定宿主没有创建任务。

此片段只展示 SDK 调用顺序；完整的外部任务与取消见 chain 示例及[生命周期验收](lifecycle-acceptance.md)。SDK 只持有来宾句柄，没有 `Transfer`。宿主要移交的业务，应由宿主处理器创建、持有可信 `*access.Task`，在持有该句柄时明确决定接管；不要把宿主句柄交给插件。

`Transfer` 只改变取消权，不持久化 checkpoint，也不保证重启后继续。宿主完成任务、保存自己的业务记录后再 `Release`；既有 handle 仍可读终态，但 manager 不再为它占用结果额度。

## 失败时怎么处理

`ErrQuota`、`ErrTaskQuota` 和 `ErrNotFound` 都应交给业务判断，不转换成“成功但为空”。namespace 的 `CommittedError` 表示记录已提交、后续同步或清理失败，先核对状态，不盲目重放；`RecoveryError` 表示该 store 不能继续写入，需要重新打开并处理恢复错误。向 guest 返回宿主错误前用 `access.PublicError(err)` 投影，无需创建诊断 Sink。完整限制、配额和错误投影见[访问层参考](access.md)。
