# Porch 状态机规范（M0-01）

## 1. 文档信息

- 状态：Draft
- 负责人：
- 评审人：
- 创建日期：2026-03-02
- 最近更新：2026-03-02
- 关联文档：`pipeline-orchestrator.md`、`porch-implementation-checklist.md`

## 2. 目标与范围

本规范用于定义 `porch watch` 过程中组件流水线状态机的唯一语义来源，覆盖：

- 组件/流水线状态定义
- 状态转移触发条件
- 不变量（不可违反规则）
- 终态与 `final_action` 触发门控

不包含：

- TUI 展示样式细节
- 企业微信消息模板文案
- 具体 Go 代码实现

## 3. 状态定义

| 状态 | 含义 | 是否终态 | 备注 |
|---|---|---|---|
| `PENDING` | 组件等待依赖满足，尚未进入监控 | 否 | 仅在有依赖时出现 |
| `WATCHING` | 正在监控当前 PipelineRun | 否 | 常态运行中状态 |
| `RETRYING` | 已触发自动重试，等待 settle 或重新发现新 run | 否 | 对应一次 attempt |
| `SUCCESS` | 组件所有目标流水线成功 | 是 | 终态，不可回退 |
| `FAILED` | 当前 run 判定失败，待决策是否重试 | 否 | 瞬时决策状态 |
| `EXHAUSTED` | 自动重试次数耗尽 | 是 | 进入人工介入路径 |
| `BLOCKED` | 编排被阻塞（例如依赖组件耗尽） | 是 | 编排层终态 |
| `QUERY_ERROR` | 查询异常累计超过阈值 | 否 | 查询恢复后回 `WATCHING` |
| `TIMEOUT` | 到达全局超时 | 是 | 编排层终态 |

## 4. 状态转移矩阵（需评审确认）

| 当前状态 | 触发条件 | 下一状态 | 动作 |
|---|---|---|---|
| `PENDING` | 上游依赖全部 `SUCCESS` | `WATCHING` | 开始查询该组件流水线 |
| `WATCHING` | `Succeeded=True` | `SUCCESS` | 记录完成时间，刷新 SHA（按策略） |
| `WATCHING` | `Succeeded=False` | `FAILED` | 记录失败原因 |
| `WATCHING` | `Succeeded=Unknown` | `WATCHING` | 保持监控 |
| `WATCHING` | 连续查询失败超过阈值 | `QUERY_ERROR` | 记录查询错误计数 |
| `QUERY_ERROR` | 下次查询成功 | `WATCHING` | 清零查询错误计数 |
| `FAILED` | `retry_count < max_retries` | `RETRYING` | 计算 backoff 并触发自动重试 |
| `FAILED` | `retry_count >= max_retries` | `EXHAUSTED` | 发送通知，等待人工介入 |
| `RETRYING` | settle 后发现新 run 成功 | `WATCHING` | 更新 run 名称后继续监控 |
| `RETRYING` | settle 后未发现新 run | `RETRYING` | 保持等待或进入失败恢复策略 |
| `EXHAUSTED` | 人工手动重试触发 | `RETRYING` | 记录 `manual_retry` 事件 |
| 任意非终态 | 到达全局超时 | `TIMEOUT` | 停止编排并通知 |
| 编排中任意组件 | 上游组件进入 `EXHAUSTED/BLOCKED` 且策略为阻塞 | `BLOCKED` | 暂停下游编排 |

## 5. 不变量（必须满足）

1. `SUCCESS`、`BLOCKED`、`TIMEOUT` 为不可逆终态。
2. `QUERY_ERROR` 不等价于流水线失败，不得直接触发自动重试。
3. 同一组件同一流水线同一 attempt 只能进入一次 `RETRYING` 触发路径。
4. `final_action` 仅在所有组件状态均为 `SUCCESS` 且 `triggered=false` 时触发一次。
5. 任何状态转移都必须落事件日志并更新 state store。

## 6. 编排层规则

- 依赖编排默认并行执行，无依赖组件启动即 `WATCHING`。
- 有依赖组件必须保持 `PENDING`，直到依赖组件全部 `SUCCESS`。
- 任一关键组件进入 `EXHAUSTED` 时，可按策略：
  - 策略 A：全局 `BLOCKED`（默认建议）
  - 策略 B：仅阻塞依赖树分支（后续增强）

## 7. `final_action` 门控规则

- 触发前置条件：
  - 全部组件全部目标流水线为 `SUCCESS`
  - `state.final_action.triggered == false`
  - 未处于 `--dry-run`
- 触发动作：
  - 解析最终分支优先级：`--final-branch > final_action.branch > branch_from_component`
  - 获取 `tektoncd-operator` 对应分支最新 SHA
  - 发评论触发 `/test to-update-components branch:{branch}`
- 触发后动作：
  - 原子落盘 `triggered=true` 与 `triggered_at`
  - 记录事件并发送通知（若配置）

## 8. 异常与恢复规则

- `kubectl` 临时失败：不改变流水线业务状态，仅累计查询错误。
- state 文件损坏：拒绝继续 watch，输出恢复建议，允许 `--force-reinit`（后续实现）。
- 重启恢复：优先恢复已有 run；若 run 不存在则回初始化发现流程。

## 9. 待确认项

- [ ] `RETRYING` 在 settle 超时后的最大等待窗口策略。
- [ ] `BLOCKED` 是否允许通过命令行手动解除。
- [ ] `EXHAUSTED` 后是否允许自动降级为低频重试模式。

## 10. 验收清单

- [ ] 转移矩阵覆盖所有状态与关键触发条件。
- [ ] 不变量可映射为自动化测试断言。
- [ ] 与 `retry-idempotency.md`、`pipelinerun-discovery.md` 无冲突。
- [ ] 评审通过并冻结版本（标记为 Approved）。
