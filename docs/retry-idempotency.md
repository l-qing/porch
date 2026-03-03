# Porch 自动重试幂等规范（M0-02）

## 1. 文档信息

- 状态：Draft
- 负责人：
- 评审人：
- 创建日期：2026-03-02
- 最近更新：2026-03-02
- 关联文档：`pipeline-orchestrator.md`、`docs/state-machine.md`

## 2. 目标与边界

目标：定义自动重试在网络抖动、进程重启、重复事件输入下仍保持“最多一次有效触发”的规则。

边界：

- 覆盖自动重试（Retrier）和手动重试（`porch retry`）在状态记录上的一致性。
- 不覆盖具体 PAC 工作流是否接受评论命令。

## 3. 术语

- `attempt`：针对同一组件同一流水线同一 SHA 的第 N 次自动重试尝试（从 1 开始）。
- `idempotency_key`：幂等键，用于判定一次 attempt 是否已执行。
- `settle_delay`：触发重试后等待 CI 创建新 PipelineRun 的观察窗口。

## 4. 幂等键定义

```
idempotency_key = <component>|<pipeline>|<sha>|<attempt>
```

规则：

1. 幂等键仅对应自动重试路径。
2. 手动重试单独记录 `manual_retry_id`，不复用自动重试 attempt 序列。
3. 同一幂等键在 state 中若标记为 `triggered=true`，禁止再次发评论。

## 5. 状态数据扩展（草案）

建议在 state 中对每个 pipeline 增加以下字段：

| 字段 | 类型 | 含义 |
|---|---|---|
| `retry_count` | int | 已完成的自动重试次数 |
| `last_retry_key` | string | 最近一次自动重试幂等键 |
| `last_retry_triggered_at` | RFC3339 string/null | 最近一次发评论时间 |
| `last_retry_result` | enum | `triggered`/`skipped_duplicate`/`failed` |
| `manual_retry_count` | int | 手动重试次数 |

## 6. 自动重试执行协议

### 6.1 执行前检查

1. 校验当前状态为 `FAILED`。
2. 校验 `retry_count < max_retries`。
3. 计算 `attempt = retry_count + 1`。
4. 生成 `idempotency_key`。
5. 查询 state：若该 key 已存在且 `triggered=true`，直接跳过（幂等命中）。

### 6.2 执行流程（两阶段落盘）

1. 先落盘“准备执行”记录：`retrying=true`、`pending_key=idempotency_key`。
2. 执行 gh comment。
3. 成功后落盘：
   - `retry_count += 1`
   - `last_retry_key = idempotency_key`
   - `last_retry_result = triggered`
   - `last_retry_triggered_at = now`
4. 失败后落盘：
   - `last_retry_result = failed`
   - 保留 `pending_key` 用于恢复补偿

### 6.3 settle 阶段

1. 等待 `retry_settle_delay`。
2. 查询 check-runs 获取新 run。
3. 成功发现后更新 `pipelinerun_name`，状态回 `WATCHING`。
4. 未发现时进入“重查窗口”策略（次数/时长待配置）。

## 7. 重启恢复协议

启动时扫描 state：

- 若存在 `pending_key` 且无 `triggered` 成功记录：
  - 查询最近事件日志与 GH 回执（如可得）
  - 无法确认时采用“保守不重复触发”策略：先查 check-runs 是否已出现新 run
- 若 `pending_key` 已对应新 run：清理 `pending_key`，回到 `WATCHING`
- 若确认未触发：允许补偿执行一次（复用同 key）

## 8. 手动重试与自动重试关系

- 手动重试不受 `max_retries` 限制。
- 手动重试必须写入独立事件类型 `manual_retry`。
- 自动重试计数与手动重试计数分离，避免误判“已耗尽”。

## 9. 失败分类与处理

| 失败类型 | 示例 | 处理策略 |
|---|---|---|
| 瞬时网络失败 | `gh` 超时、连接重置 | 指数退避后重试当前 attempt（不增 attempt） |
| 认证失败 | token 失效、权限不足 | 标记组件 `EXHAUSTED` 或 `BLOCKED`，通知人工 |
| 语义失败 | 评论格式不被 PAC 识别 | 标记 `needs-override`，停止自动重试 |
| 查询失败 | check-runs 暂不可读 | 进入查询重试窗口，不立即判失败 |

## 10. 测试用例清单

- [ ] 同一失败事件重复输入 2 次，仅触发 1 次评论。
- [ ] 重试触发后进程崩溃，重启后不重复触发。
- [ ] `max_retries` 到达后进入 `EXHAUSTED`。
- [ ] 手动重试可在 `EXHAUSTED` 后重新进入 `RETRYING`。
- [ ] `gh` 临时失败不造成 attempt 泄漏。

## 11. 待确认项

- [ ] “无法确认是否已触发 comment”时的最终补偿策略。
- [ ] settle 阶段最大等待窗口默认值。
- [ ] 自动重试与手动重试在 TUI 中的可视化区分方式。

## 12. 验收清单

- [ ] 幂等键定义在代码、日志、state 中一致。
- [ ] 两阶段落盘策略有对应测试覆盖。
- [ ] 恢复协议在中断场景下验证通过。
- [ ] 与状态机规范评审一致并冻结版本。
