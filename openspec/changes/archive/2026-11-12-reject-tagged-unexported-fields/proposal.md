## Why

encoding/json 不编码未导出字段，推导契约时它们被静默略去。作者给小写字段写了 `json` 标签，以为它在契约里，直到调用方发现字段不见了才察觉。

## What Changes

- 本包中带 `json` 或 `cordis` 标签的未导出字段使生成失败，提示导出它或去掉标签。其他包里的类型不受影响。
- 约束不适用的报错改为 `minLength does not apply to integer values`。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-contracts`: 带标签的未导出字段被拒绝。

## Impact

- `schema/gocontract`。
