# Porch PipelineRun 发现与校验规范（M0-03）

## 1. 文档信息

- 状态：Draft
- 负责人：
- 评审人：
- 创建日期：2026-03-02
- 最近更新：2026-03-02
- 关联文档：`pipeline-orchestrator.md`、`docs/state-machine.md`、`docs/retry-idempotency.md`

## 2. 目标与问题

目标：定义从 GitHub check-runs 到 Tekton `PipelineRun(namespace/name)` 的稳定发现链路，并提供失败兜底。

核心问题：

- `details_url` 结构可能变化，解析脆弱。
- 同一分支可能出现新 commit，导致旧 run 与新 run 混淆。
- check-runs 延迟可见，触发后短时间内无法立即拿到新 run。

## 3. 输入与输出

### 3.1 输入

- 组件静态配置：`component.name/repo/pipeline/retry_command`
- 分支信息：来自 `components.yaml` 的 `revision`
- 运行时信息：
  - `sha`（`gh api repos/{org}/{repo}/commits/{branch}`）
  - `check-runs`（`gh api repos/{org}/{repo}/commits/{sha}/check-runs`）

### 3.2 输出

- `namespace`
- `pipelinerun_name`
- `discovery_source`（`details_url`/`manual_namespace`/`fallback_query`）
- `confidence`（`high`/`medium`/`low`）

## 4. 发现流程（主路径）

1. 获取目标分支最新 `sha`。
2. 拉取该 `sha` 的 check-runs 列表。
3. 按配置的 pipeline 名匹配 check-run 条目。
4. 解析匹配条目的 `details_url`，提取 `namespace` 与 `pipelinerun_name`。
5. 通过 `kubectl get pipelinerun -n <ns> <name>` 验证存在性。
6. 校验通过后写入 state，标记 `confidence=high`。

## 5. URL 解析规则（已核验）

已基于 11 个组件最新 check-run `details_url` 样本核验：

```
https://edge.example.com/console-pipeline-v2/workspace/<namespace>~<workspace_name>~<namespace>/pipeline/pipelineRuns/detail/<pipelinerun_name>
```

示例：

```
https://edge.example.com/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-2wn4t
```

`<workspace_name>` defaults to `business-build` and can be overridden by `connection.pipeline_workspace_name`.

解析规则：

- `namespace`：使用正则 `/workspace/([^~/]+)~[^~/]+~([^/]+)/pipeline/`，要求两个捕获组一致。
- `pipelinerun_name`：取 URL 末尾 `detail/<name>` 的 `<name>`；也可与 check-run `external_id` 交叉校验。

解析要求：

- 必须同时提取 `namespace` 和 `pipelinerun_name`，并做一致性校验。
- 解析失败时返回结构化错误（`invalid_details_url`）。
- 不允许“部分解析成功”进入监控流程。

## 6. 交叉校验规则

发现结果写入前必须满足：

1. check-run 名称与配置 pipeline 名一致。
2. 当前 `sha` 与触发上下文一致。
3. `kubectl` 能查询到目标 `PipelineRun`。
4. 若存在 run 的标签/注解可读，建议校验分支或仓库信息（后续增强）。

任一失败：进入兜底流程，不进入 `WATCHING`。

## 7. 兜底流程（降级策略）

### 7.1 降级优先级

1. 手工配置 namespace（推荐在 `orchestrator.yaml` 扩展字段）
2. 基于组件标签在集群查询候选 PipelineRun（`fallback_query`）
3. 标记组件 `needs-override` 并暂停自动流程

### 7.2 fallback_query 约束

- 必须限定时间窗口（例如最近 N 分钟）
- 必须限定 pipeline 名前缀匹配
- 多候选时不得自动选取，需人工确认

## 8. 重试后重新发现流程

1. 触发 gh comment。
2. 等待 `retry_settle_delay`。
3. 重新拉取同 `sha` 的 check-runs。
4. 若该 `sha` 无新 run，允许“先刷新 SHA 后再查一次”。
5. 发现新 run 后更新 state 并回 `WATCHING`。

## 9. 错误分类与处置

| 错误码 | 触发场景 | 处置 |
|---|---|---|
| `invalid_details_url` | URL 无法解析 | 走手工 namespace 兜底 |
| `checkrun_not_found` | 未匹配到 pipeline 名 | 标记待确认，暂停自动重试 |
| `run_not_found` | 解析到 run 但 kubectl 不存在 | 进入重查窗口，超时后兜底 |
| `sha_race_detected` | 查询中检测到分支有新 commit | 刷新 SHA 后重跑发现流程 |

## 10. 可观测性要求

每次发现流程必须记录事件字段：

- `component`
- `pipeline`
- `branch`
- `sha`
- `details_url`
- `namespace`
- `pipelinerun_name`
- `discovery_source`
- `confidence`
- `error_code`（如有）

## 11. 测试清单

- [ ] 标准 URL 正常解析并可被 kubectl 验证。
- [ ] URL 结构变体（多级 path/hash）解析健壮。
- [ ] 解析失败时进入手工 namespace 兜底。
- [ ] 重试后可发现新 run 并替换旧 run。
- [ ] 新 commit 竞态下不会错误关联旧 run。

## 12. 待确认项

- [x] 各仓库 check-run `details_url` 的真实格式样本（11 组件已采样，见 `docs/component-pipeline-validation.md`）。
- [ ] 是否统一扩展 `orchestrator.yaml` 支持每组件显式 namespace。
- [ ] fallback_query 的标签过滤条件可行性。

## 13. 验收清单

- [ ] 主路径与兜底路径均有明确进入/退出条件。
- [ ] 错误分类能直接映射到运行时处理逻辑。
- [ ] 与状态机、重试幂等规范无冲突。
- [ ] 评审通过并冻结版本。
