---
title: 核心概念
description: 定义、实例、激活与服务依赖。
---

# 核心概念

`Definition` 描述初始化函数、配置校验、服务依赖与可选子定义。`Entry` 是一份实例配置；同一定义可以创建多个实例。每次启动产生独立的 activation，资源和服务绑定属于这次 activation。

## 依赖与服务

实例在全部 `Requires` 就绪后调用 `Start`。`Provide` 在初始化期间登记服务，最终发布后才允许调用。服务以名称和 major 标识，消费者声明可接受的 minor 范围。

一个 Host 默认共享根服务空间；父子关系本身只表达生命周期所有权。`Entry.Isolate` 或 `Child.Isolate` 可为 `db@1` 这样的完整 key 建立子树边界，两组数据库便可独立绑定，同时共享未隔离的日志服务。

每个 key 使用最近的显式边界；同域重复 provider 冲突，边界内缺服务时保持 Waiting，不向根或兄弟组回退。`Host.Call` 只查根，`Host.Caller(entryID)` 可取得某个当前登记的解析视图；移除重建后旧视图失效。隔离不改变事件的 Host 内广播，也不是权限沙箱。

包依赖解决“所需代码版本是否安装并选中”，服务依赖解决“所需 provider 是否已经发布”，两者分开报告。

## 生命周期

实例依次进入 Waiting、Starting、Active；退役时进入 Quiescing，清理完成后进入 Stopped 或 Failed。失败不会自动重启，显式 `Update` 可再次初始化。

父实例拥有子实例，provider 还拥有消费者的依赖生命周期。停止 provider 时，框架先取消普通工作，等待消费者清理，再释放 provider。清理继续使用旧的服务绑定。

`Start` 的 context 只覆盖初始化，`scope.Context()` 覆盖整次激活。资源使用 `Defer`；需要随激活取消并等待的后台工作使用 `contract.Go`。详见[组合](composition.md)。
