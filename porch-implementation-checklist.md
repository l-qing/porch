# Porch 可执行任务清单（实施版）

基于文档：`pipeline-orchestrator.md`、`release-orchestration-request-2026-03-02.md`、`components.yaml`。

目标：按清单直接落地 `porch`（`watch/status/retry` + 自动重试 + DAG 编排 + final_action + 恢复能力）。

## 0. 执行规则

- 执行顺序：必须按里程碑顺序推进，不跳阶段。
- 每个任务完成后，必须补“验收证据”（命令输出或日志片段路径）。
- 所有代码任务默认要求：
  - `go test ./...` 通过
  - `go vet ./...` 通过
  - `go build ./cmd/porch` 通过
- 并发边界：同一组件的 GH 操作（刷新 SHA、发评论、查 check-runs）必须串行。
- 幂等边界：自动重试按 `(component, pipeline, sha, attempt)` 唯一键执行，禁止重复触发。

## 1. 输入资产冻结（当前已知）

### 1.1 组件分支（来自 `components.yaml`）

| 组件名 | revision |
|---|---|
| tektoncd-pipeline | release-1.6 |
| hubs-wrapper | release-1.0 |
| tektoncd-enhancement | release-0.2 |
| tektoncd-pac | release-0.39 |
| tektoncd-chain | release-0.26 |
| tektoncd-hub | release-1.23 |
| tektoncd-trigger | release-0.34 |
| tektoncd-result | release-0.17 |
| tektoncd-pruner | release-0.3 |
| tektoncd-manual-approval-gate | release-0.7 |
| catalog | main |

### 1.2 组件流水线映射（来自设计文档，待线上核验）

| 组件名 | repo | pipeline | retry_command 模板 |
|---|---|---|---|
| tektoncd-pipeline | tektoncd-pipeline | tp-all-in-one | `/test tp-all-in-one branch:{branch}` |
| hubs-wrapper | tektoncd-hubs-api | tha-all-in-one | `/test tha-all-in-one branch:{branch}` |
| tektoncd-enhancement | tektoncd-enhancement | te-enhancement-all-in-one | `/test te-enhancement-all-in-one branch:{branch}` |
| tektoncd-pac | tektoncd-pipelines-as-code | pac-all-in-one | `/test pac-all-in-one branch:{branch}` |
| tektoncd-chain | tektoncd-chain | tc-all-in-one | `/test tc-all-in-one branch:{branch}` |
| tektoncd-hub | tektoncd-hub | th-all-in-one | `/test th-all-in-one branch:{branch}` |
| tektoncd-trigger | tektoncd-trigger | tt-all-in-one | `/test tt-all-in-one branch:{branch}` |
| tektoncd-result | tektoncd-result | tr-all-in-one | `/test tr-all-in-one branch:{branch}` |
| tektoncd-pruner | tektoncd-pruner | tpr-all-in-one | `/test tpr-all-in-one branch:{branch}` |
| tektoncd-manual-approval-gate | tektoncd-manual-approval-gate | approval-all-in-one | `/test approval-all-in-one branch:{branch}` |
| catalog | catalog | catalog-all-in-one | `/test catalog-all-in-one branch:{branch}` |

## 2. 里程碑 M0：规格冻结（预计 0.5-1 天）

> 目标：先锁定高风险规范（状态机、幂等、解析策略），再开始编码。

- [ ] M0-01 固化状态机转移表
  - 操作：定义 `PENDING/WATCHING/RETRYING/SUCCESS/FAILED/EXHAUSTED/BLOCKED/QUERY_ERROR/TIMEOUT` 的允许转移和终态。
  - 产出：`docs/state-machine.md`
  - 验收：文档包含“状态 + 触发条件 + 下一状态 + 不变量”四列。

- [ ] M0-02 固化重试幂等协议
  - 操作：定义 attempt 唯一键、落盘时机、重启恢复行为、重复执行拦截规则。
  - 产出：`docs/retry-idempotency.md`
  - 验收：明确“同一 attempt 最多一次 gh comment”。

- [ ] M0-03 固化 PipelineRun 识别与兜底策略
  - 操作：定义 `check-run details_url` 解析规则、失败降级（手工 namespace）策略。
  - 产出：`docs/pipelinerun-discovery.md`
  - 验收：包含“解析成功路径 + 解析失败路径 + 风险案例”。

- [ ] M0-04 核验 11 个组件的 pipeline 名称与评论格式
  - 操作：对每个组件抽样检查最近 check-runs 和 `on-comment` 支持格式。
  - 产出：`docs/component-pipeline-validation.md`
  - 验收：11 组件全部有结论（confirmed/needs-override）。

## 3. 里程碑 M1：项目骨架 + 基础命令（预计 1.5-2 天）

> 目标：交付可编译、可配置、可查询状态、可手动重试的最小可运行版本。

- [x] M1-01 初始化项目结构
  - 操作：创建 `cmd/porch`、`pkg/config`、`pkg/gh`、`pkg/state`、`pkg/component`、`pkg/watcher`、`pkg/logger`。
  - 验收：`go build ./cmd/porch` 成功。

- [x] M1-02 实现配置类型与加载
  - 操作：实现 `orchestrator.yaml + components.yaml` 合并逻辑，注入 branch。
  - 产出：`pkg/config/types.go`、`pkg/config/loader.go`、`pkg/config/validate.go`
  - 验收：
    - 配置错误可读报错（缺字段、name 不匹配、重复组件）
    - `go test ./pkg/config -v` 通过

- [x] M1-03 实现 GH 客户端封装
  - 操作：封装 `gh` 调用：获取分支 SHA、获取 check-runs、发 commit comment。
  - 产出：`pkg/gh/client.go`
  - 验收：
    - 子进程调用支持 `context timeout`
    - 错误包含 command + stderr
    - `go test ./pkg/gh -v` 通过（mock exec）

- [x] M1-04 实现 state store 原子读写
  - 操作：`flock + temp file + rename`；定义 state schema v1。
  - 产出：`pkg/state/store.go`、`pkg/state/types.go`
  - 验收：
    - 并发写不损坏
    - 重启后可读取
    - `go test ./pkg/state -v` 通过

- [x] M1-05 实现 `porch status`
  - 操作：执行初始化（SHA/check-runs 发现）+ 单次 kubectl 查询 + 表格输出。
  - 产出：`cmd/porch/status.go`、`pkg/component/loader.go`、`pkg/watcher/probe.go`
  - 验收：`porch status --config orchestrator.yaml` 输出 11 组件状态。

- [x] M1-06 实现 `porch retry`（手动）
  - 操作：支持组件级/流水线级手动重试，忽略 backoff 和上限。
  - 产出：`cmd/porch/retry.go`、`pkg/retrier/manual.go`
  - 验收：
    - 可发起正确 comment
    - 事件日志记录“manual_retry”

- [x] M1-07 基础事件日志
  - 操作：同时写 stdout 与 `log.file`。
  - 产出：`pkg/logger/logger.go`
  - 验收：关键事件（INIT、GH_CALL、KUBECTL_QUERY、RETRY_TRIGGER）可检索。

## 4. 里程碑 M2：持续监控 + 自动重试（预计 2-3 天）

> 目标：交付可长期运行的 `porch watch`，具备失败自动恢复能力。

- [x] M2-01 实现 `porch watch` 主循环
  - 操作：轮询间隔、context cancel、Ctrl+C 优雅退出。
  - 产出：`cmd/porch/watch.go`、`pkg/watcher/watcher.go`
  - 验收：中断后 state 落盘，重启可继续。

- [x] M2-02 实现 PipelineRun 状态判定
  - 操作：仅基于 `Succeeded` condition（True/False/Unknown）映射状态。
  - 验收：单元测试覆盖 succeeded/failed/running/query_error。

- [x] M2-03 实现自动重试与 backoff
  - 操作：`initial * multiplier^(n-1)`，封顶 `max`。
  - 产出：`pkg/retrier/retrier.go`
  - 验收：重试时间计算和 `max_retries` 逻辑测试通过。

- [x] M2-04 实现重试后 PipelineRun 重新发现
  - 操作：执行 comment -> settle delay -> check-runs -> 更新 name/namespace。
  - 验收：失败后可切换到新 PipelineRun 并继续监控。

- [x] M2-05 实现 QUERY_ERROR 容错
  - 操作：连续查询失败计数；阈值内不判流水线失败；恢复后回 WATCHING。
  - 验收：网络抖动场景不会误触发重试。

- [x] M2-06 实现最小 TUI（表格 + 事件流）
  - 操作：`bubbletea + bubbles(table/viewport)`。
  - 产出：`pkg/tui/table.go`
  - 验收：状态变化自动刷新，事件可滚动查看。

## 5. 里程碑 M3：编排与最终动作（预计 2 天）

> 目标：支持依赖 DAG、全局超时、全量成功后自动触发 final_action。

- [x] M3-01 实现依赖 DAG 解析
  - 操作：检测循环依赖；无依赖组件先启动；有依赖组件按上游 SUCCESS 解锁。
  - 产出：`pkg/resolver/dag.go`
  - 验收：提供 DAG 单元测试（并行、串行、环路）。

- [x] M3-02 实现全局超时机制
  - 操作：超时后将未完成组件标记 TIMEOUT，写事件并退出。
  - 验收：超时行为可重复复现。

- [x] M3-03 实现 final_action 分支解析优先级
  - 操作：`--final-branch > final_action.branch > branch_from_component`。
  - 验收：3 种路径都有测试。

- [x] M3-04 实现 final_action 一次性触发闸门
  - 操作：使用 `state.final_action.triggered` 防重。
  - 验收：重复启动 `watch` 不会重复触发。

- [x] M3-05 实现企业微信通知
- 操作：支持 `all_succeeded/progress_report/component_exhausted/global_timeout` 事件，并支持 `progress_interval`。
  - 产出：`pkg/notify/wecom.go`
  - 验收：空 webhook 时静默跳过；配置后发送成功。

## 6. 里程碑 M4：恢复与生产加固（预计 1.5-2 天）

> 目标：降低真实运行中的不确定性，保证可恢复、可定位、可运维。

- [x] M4-01 断点续跑恢复
  - 操作：启动时读取 state，验证已记录 PipelineRun 是否存在；不存在则重新初始化该组件。
  - 验收：模拟删除旧 PipelineRun 后仍可自愈。

- [x] M4-02 GH 竞态与 SHA 刷新策略
  - 操作：重试或成功后刷新 SHA；常规轮询不刷新；同组件 GH 操作串行。
  - 验收：分支有新 commit 时不误用旧 SHA。

- [x] M4-03 可观测性补齐
  - 操作：统一事件字段（component/pipeline/sha/attempt/reason/error）。
  - 验收：可以从日志重建任一组件完整生命周期。

- [x] M4-04 故障演练
  - 操作：注入 gh 失败、kubectl 超时、网络错误、文件锁竞争、进程中断。
  - 验收：无崩溃；错误路径均有明确日志和状态落盘。

## 7. 测试与验收矩阵（必须全过）

- [x] T-01 单元测试
  - `pkg/config`、`pkg/state`、`pkg/retrier`、`pkg/resolver` 覆盖核心分支。

- [x] T-02 集成测试（建议）
  - fake `gh` + fake `kubectl` 回放场景：成功、失败重试、重试耗尽、超时、恢复。

- [x] T-03 命令级验收
  - `porch status --config orchestrator.yaml`
  - `porch retry --config orchestrator.yaml --component tektoncd-chain`
  - `porch watch --config orchestrator.yaml`

- [x] T-04 质量门
  - `go test ./...`
  - `go vet ./...`
  - `go build ./cmd/porch`

## 8. 日程建议（可直接照抄到迭代计划）

- Day 1：完成 M0 + M1-01~M1-03
- Day 2：完成 M1-04~M1-07
- Day 3：完成 M2-01~M2-03
- Day 4：完成 M2-04~M2-06
- Day 5：完成 M3-01~M3-05
- Day 6：完成 M4-01~M4-04 + 全量验收

## 9. 当前阻塞项（需尽早关闭）

- [x] B-01 确认 11 个组件真实 pipeline 名称（见 `docs/component-pipeline-validation.md`）
- [x] B-02 确认各组件 PAC `on-comment` 是否统一支持 `/test {pipeline} branch:{branch}`（已通过 live comment 验证，见 `docs/component-pipeline-validation.md`）
- [x] B-03 确认 check-run `details_url` 解析 namespace 的稳定格式（已验证可解析 `workspace/<ns>~...~<ns>`）

---

执行建议：先完成 M0，M0 通过后再进入编码。M0 不通过时，不建议启动 M1 以免返工。
