# Pipeline Orchestrator (porch) 设计文档

## 1. 背景与问题

`tektoncd-operator` 管理 11 个子组件，每次发版修漏洞需要：

1. 等各子组件 renovate 修漏洞 → 合并 → 触发 PAC 流水线
2. **人工逐一检查** 11 个组件的流水线是否全部成功
3. 失败时**人工通过评论触发重试**
4. 全部成功后执行 `make update-components`

**痛点**：步骤 2-3 完全依赖人工轮询，11 个组件 × 多次重试 = 巨大人力消耗。

在上述发版场景之外，还存在两类高频诉求：

1. 关注**所有组件的关键分支**（例如 `main` + 各 `release-*`）对应流水线；
2. 关注**单个组件的单个分支**（用于定点排障/值守）。

三类场景的共同目标一致：
- 监控指定组件 + 指定分支 + 指定流水线
- 失败后自动重试（基于 PAC comment）
- 按需向企业微信持续汇报进度与异常

## 2. 适用场景

`porch` 适用于以下场景：

### 典型使用场景

| 场景 | 说明 | 推荐命令 |
|------|------|----------|
| **发版时批量监控** | 11 个子组件同时构建，需要全部成功后触发聚合操作 | `porch watch --config orchestrator.yaml` |
| **关键分支全量巡检** | 关注所有组件的 `main` + `release-*`，统一监控并自动重试失败流水线 | `porch watch --config orchestrator.yaml --disable-final-action --probe-mode gh-only` |
| **本地调试/快速查看** | 无集群访问权限，只想通过 GH API 查看当前状态 | `porch status --config orchestrator.yaml --probe-mode gh-only` |
| **单组件单分支值守** | 只关注某个组件的某个分支，对应流水线失败自动重试 | `porch watch --config orchestrator.yaml --component tektoncd-chain --branch release-1.0` |
| **手动重试** | 自动重试前想手动触发某个组件 | `porch retry --component tektoncd-chain --pipeline tc-all-in-one` |
| **CI/CD 集成** | 在 CI 中监控并自动退出 | `porch watch --exit-after-final-ok` |
| **多分支复用配置** | 同一 orchestrator.yaml 用于不同 release 分支 | `porch --components-file ./components-release-1.6.yaml --final-branch release-1.6 watch` |
| **单组件多分支巡检** | 在一个 watch 中同时观测同一组件的 main + 多个 release 分支，并禁用 final_action | `porch watch --config orchestrator.yaml --disable-final-action --probe-mode gh-only` |

关键分支全量巡检建议在配置中为每个组件设置 `branches` 或 `branch_patterns`，由 porch 在启动时展开为运行时实例（例如 `tektoncd-pipeline@main`、`tektoncd-pipeline@release-1.8`），并分别进行状态跟踪、重试计数和通知汇总。

### 不适用场景

- 单个仓库的单条流水线监控（直接用 `gh run watch` 即可）
- 非 Tekton PipelineRun 的 CI 系统（porch 依赖 Tekton 的状态模型和 PAC 评论触发机制）
- 不使用 GitHub 的代码托管平台（porch 依赖 `gh` CLI 与 GitHub API）

## 3. 目标

构建一个轻量级 CLI 工具 `porch`（**P**ipeline **ORCH**estrator），实现：

- 监控多个组件的指定流水线状态（支持 kubectl 和 GH check-runs 双路径）
- 失败时自动通过 commit 评论触发重试
- 支持依赖编排，所有依赖就绪后自动触发下游操作
- 实时终端表格展示进度，有变更自动刷新

## 4. 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                   Pipeline Orchestrator CLI                 │
│                                                             │
│  ┌────────────────┐   ┌──────────────┐   ┌───────────────┐  │
│  │ ComponentLoader│   │   Watcher    │   │    Retrier    │  │
│  │ (解析配置 +    │   │ (kubectl /   │   │ (gh commit    │  │
│  │  gh 初始化)    │──▶│  gh 探测)    │──▶│  comment)     │  │
│  └────────────────┘   └──────┬───────┘   └───────────────┘  │
│                              │                              │
│  ┌───────────────────────────┴────────────────────────────┐ │
│  │          Probe Mode (auto / gh-only / kubectl-first)   │ │
│  └────────────────────────────────────────────────────────┘ │
│                              │                              │
│  ┌───────────────────────────┴────────────────────────────┐ │
│  │              Dependency Resolver (DAG 编排)            │ │
│  └────────────────────────────────────────────────────────┘ │
│                              │                              │
│  ┌───────────────────────────┴────────────────────────────┐ │
│  │              State Store (本地 JSON 文件)              │ │
│  └────────────────────────────────────────────────────────┘ │
│                              │                              │
│  ┌───────────────────────────┴────────────────────────────┐ │
│  │              Notifier (企业微信 Webhook)               │ │
│  └────────────────────────────────────────────────────────┘ │
│                              │                              │
│  ┌───────────────────────────┴────────────────────────────┐ │
│  │              TUI (实时表格 + 事件日志)                 │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
         │                                    │
   kubectl (exec)                       gh CLI (exec)
         │                                    │
    K8s Cluster                         GitHub API
   (PipelineRun)                    (check-runs / comments)
```

## 5. 配置文件设计

### 5.1 orchestrator.yaml（主配置）

```yaml
apiVersion: porch/v1
kind: ReleaseOrchestration
metadata:
  name: tektoncd-main

# ── 连接配置 ──
connection:
  kubeconfig: ~/.kube/config         # 或读取 KUBECONFIG 环境变量
  context: ""                        # 可选，指定 k8s context；为空则用 current-context
  github_org: TestGroup           # GitHub 组织名
  pipeline_console_base_url: https://edge.alauda.cn/console-pipeline-v2   # Optional: pipeline console base URL
  pipeline_workspace_name: business-build                                  # Optional: workspace middle segment in detail URL

# ── 监控配置 ──
watch:
  interval: 30s                      # 轮询间隔

# ── 重试配置 ──
retry:
  max_retries: 10                    # 单个流水线最大重试次数
  backoff:                           # 退避策略（算法计算）
    initial: 1m                      # 首次重试等待时间
    multiplier: 1.5                  # 退避乘数
    max: 5m                          # 单次退避上限
  retry_settle_delay: 90s            # 重试后等待多久再查询新 PipelineRun（等待 CI 创建）

# ── 超时配置 ──
timeout:
  global: 12h                        # 整个编排的全局超时，超时后标记失败并通知

# ── 通知配置 ──
notification:
  wecom_webhook: ""                  # 企业微信 Webhook URL，为空则不发通知
  events:                            # 触发通知的事件类型，注释掉表示不发该类型
    - all_succeeded                  # 所有组件成功完成（最终通知）
    - progress_report                # 进度定时报告（需配合 progress_interval 使用）
    - component_exhausted            # 某组件重试次数耗尽
    # - global_timeout               # 全局超时（通常与 TIMEOUT 日志重复，按需开启）
  progress_interval: 30m             # 进度报告发送间隔；<=0 表示不发送进度报告
  notify_rows_per_message: 12        # 单条通知最多包含多少行记录；<=0 使用默认值 12
  # 通知消息会附带跳转链接：
  # - component 列链接到 commit checks 页面
  # - branch 列链接到仓库分支页面

# ── 日志配置 ──
log:
  file: ./.porch-events.log          # 事件日志文件路径，持久化到本地
  level: info                        # 日志级别，可选 debug|info|warn|error

# ── 行为开关 ──
disable_final_action: false          # true: 仅监控与重试，不触发 final_action

# ── 分支来源 ──
# 从此文件读取各组件的 revision（分支），支持通过 CLI 参数 --components-file 覆盖
# 每个组件的 key 必须与下面 components 中的 name 匹配
components_file: ./components.yaml

# ── 组件定义 ──
# 除 branch 外的所有属性都在此声明
components:
  - name: tektoncd-pipeline          # 必须与 components.yaml 中的 key 一致
    repo: tektoncd-pipeline          # GitHub 仓库名（org 来自 connection.github_org）
    branches: []                     # 可选；显式分支列表（优先级最高）
    branch_patterns: []              # 可选；Go 正则，启动时匹配仓库分支并冻结
    pipelines:                       # 需要监控的流水线列表
      - name: tp-all-in-one          # PipelineRun 的 original-prname
        retry_command: "/test tp-all-in-one branch:{branch}"

  - name: hubs-wrapper
    repo: tektoncd-hubs-api
    pipelines:
      - name: tha-all-in-one
        retry_command: "/test tha-all-in-one branch:{branch}"

  - name: tektoncd-enhancement
    repo: tektoncd-enhancement
    pipelines:
      - name: te-enhancement-all-in-one
        retry_command: "/test te-enhancement-all-in-one branch:{branch}"

  - name: tektoncd-pac
    repo: tektoncd-pipelines-as-code
    pipelines:
      - name: pac-all-in-one
        retry_command: "/test pac-all-in-one branch:{branch}"

  - name: tektoncd-chain
    repo: tektoncd-chains
    pipelines:
      - name: tc-all-in-one
        retry_command: "/test tc-all-in-one branch:{branch}"

  - name: tektoncd-hub
    repo: tektoncd-hub
    pipelines:
      - name: th-all-in-one
        retry_command: "/test th-all-in-one branch:{branch}"

  - name: tektoncd-trigger
    repo: tektoncd-triggers
    pipelines:
      - name: tt-all-in-one
        retry_command: "/test tt-all-in-one branch:{branch}"

  - name: tektoncd-result
    repo: tektoncd-results
    pipelines:
      - name: tr-all-in-one
        retry_command: "/test tr-all-in-one branch:{branch}"

  - name: tektoncd-pruner
    repo: tektoncd-pruner
    pipelines:
      - name: tpr-all-in-one
        retry_command: "/test tpr-all-in-one branch:{branch}"

  - name: tektoncd-manual-approval-gate
    repo: tektoncd-manual-approval-gate
    pipelines:
      - name: approval-all-in-one
        retry_command: "/test approval-all-in-one branch:{branch}"

  - name: catalog
    repo: catalog
    pipelines:
      - name: catalog-all-in-one
        retry_command: "/test catalog-all-in-one branch:{branch}"

# ── 依赖关系（可选） ──
# 默认所有组件并行监控。声明依赖后，子组件等上游全部成功才开始监控。
dependencies:
  # tektoncd-hub:
  #   depends_on: [tektoncd-pipeline]
  # catalog:
  #   depends_on: [tektoncd-hub]

# ── 最终动作 ──
# 所有组件的所有流水线都成功后执行
final_action:
  # 在 tektoncd-operator 自己的对应分支最新 commit 上评论
  # 触发 pr-update-components.yaml 中定义的 to-update-components 流水线
  repo: tektoncd-operator            # GitHub 仓库名
  # branch 来源策略（三选一，优先级从高到低）：
  #   1. CLI 参数 --final-branch 覆盖
  #   2. 直接指定 branch
  #   3. 指定 branch_from_component，从某个组件的 revision 推导
  branch: "main"                     # 直接指定分支（如 release-1.6）
  branch_from_component: ""          # 从指定组件的 revision 推导（如 tektoncd-pipeline）
  comment: "/test to-update-components branch:{branch}"      # 评论内容
```

### 5.2 与 components.yaml 的关系

`components.yaml`（已有文件）提供**分支信息**：

```yaml
# components.yaml（已有，不需要修改）
tektoncd-pipeline:
  revision: release-1.6    # ← porch 读取此字段作为 branch
  releases:
    - remote_path: pipeline
      local_path: tekton-pipeline

tektoncd-pac:
  revision: release-0.39   # ← porch 读取此字段
  ...
```

**加载逻辑**：
1. 读取 `orchestrator.yaml` 中的 `components` 列表 → 得到每个组件的 repo、pipelines 等属性
2. 对每个组件按以下优先级确定分支列表：
   - 若 `components[].branches` 非空：直接使用该列表（可一次展开多个分支）
   - 否则若 `components[].branch_patterns` 非空：启动时调用一次 GH 分支列表并按正则匹配（Go regexp）
   - 否则读取 `components_file` 中同名组件的 `revisions`（若有）或 `revision`
3. 对多分支组件自动展开运行时实例（例如 `tektoncd-pipeline@main`）
4. 合并后得到完整的组件信息

`branch_patterns` 说明：
- 只有配置正则的组件会触发 GH 全量分支查询。
- 查询发生在启动阶段，匹配结果在本次运行中冻结。

```
最终每个组件的完整属性：
┌─────────────────────────────────────────────────────────┐
│ name:           tektoncd-pipeline@release-1.6           │
│ repo:           tektoncd-pipeline        (orchestrator) │
│ branch:         release-1.6              (resolved)     │
│ pipelines:                               (orchestrator) │
│   - name:       tp-all-in-one                           │
│     retry_cmd:  /test tp-all-in-one branch:release-1.6  │
│ sha:            (运行时通过 gh 获取)                    │
│ namespace:      (运行时通过 gh check-run 获取)          │
└─────────────────────────────────────────────────────────┘
```

## 6. 核心流程

### 6.1 初始化（ComponentLoader）

```
启动时执行一次：

1. 读取 orchestrator.yaml + components.yaml
   → 合并得到 11 个组件的完整配置

2. 对每个组件，通过 gh 获取运行时信息：

   a) 获取分支最新 commit SHA
      $ gh api repos/{org}/{repo}/commits/{branch}
      → sha = "abc123def456"

   b) 获取该 commit 的 check-runs，找到匹配的流水线
      $ gh api repos/{org}/{repo}/commits/{sha}/check-runs
      → 从 details_url 中解析出 namespace 和 PipelineRun 名称
         例如: https://edge.example.com/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-2wn4t
               ────────────────────────────────────────────────────────────  ──────────────────────
               namespace（workspace 中两个 ns 需一致）                      pipelinerun name

   c) 缓存到 state store

3. GH API 消耗：N 组件 × 2 请求 = 2N 次（仅启动时）
```

### 6.2 状态探测（Probe Mode）

porch 支持三种状态探测模式，通过 `--probe-mode` 参数或 `PORCH_PROBE_MODE` 环境变量控制：

| 模式 | 行为 | 适用场景 |
|------|------|----------|
| `auto`（默认） | 如果 `connection.kubeconfig` 或 `context` 任一非空 → `kubectl-first`；否则 → `gh-only` | 自动适配环境 |
| `gh-only` | 仅通过 GH check-runs 查询状态，不访问集群 | 本地调试、无集群权限 |
| `kubectl-first` | 优先通过 kubectl 查询 PipelineRun，失败时自动回退到 GH check-runs | 生产环境、高频轮询 |

#### kubectl-first 模式

```
每 {watch.interval} 执行一次：

  对每个组件的每个流水线：
    kubectl get pipelinerun -n {namespace} {pipelinerun_name} -o json

    检查 status.conditions:
      type=Succeeded, status=True    → succeeded
      type=Succeeded, status=False   → failed → 交给 Retrier
      type=Succeeded, status=Unknown → running

    kubectl 失败时 → 自动回退到 GH check-runs（记录 GH_FALLBACK 事件）
```

选择 kubectl 作为主路径的原因：kubectl 走 kubeconfig 本地连接集群，
无 rate limit 限制，可高频轮询；GH 仅作为异常场景回退与关键动作查询，降低 API 压力。

#### gh-only 模式

```
每 {watch.interval} 执行一次：

  对每个组件的每个流水线：
    gh api repos/{org}/{repo}/commits/{sha}/check-runs

    解析 check-run 的 status 和 conclusion:
      status=completed + conclusion=success → succeeded
      status=completed + conclusion≠success → failed
      status=in_progress/queued/pending     → running

    额外检测 check-run annotations（annotation_level=failure → 标记为 failed）
```

#### GH 回退时的增强检测

在 GH check-runs 回退路径中，porch 会进行两项额外检测：

1. **Annotation 检测**：如果 check-run 的 `output.annotations_count > 0`，查询 annotations，
   若存在 `annotation_level=failure` 的条目，即使 check-run 状态为 success 也标记为 failed。

2. **Run Mismatch 检测**：如果当前追踪的 PipelineRun 名称与 GH check-run 中最新的 run 不匹配，
   记录 `RUN_MISMATCH` 事件。连续不匹配达到 3 次阈值后，强制标记为 failed 并触发重试，
   以应对 PipelineRun 已过期但 GH 侧有新 run 的情况。

#### 错误容忍

kubectl/GH 查询可能因网络或集群临时问题失败。此类查询失败
**不等同于流水线失败**。Watcher 会在日志中记录错误并在下一个轮询周期重试查询，
连续查询失败超过阈值（默认 5 次）才标记为 `QUERY_ERROR` 状态。

### 6.3 自动重试（Retrier）

当 Watcher 检测到流水线失败：

```
1. 检查重试次数是否超过 max_retries
   → 超过：标记为 EXHAUSTED，发送企业微信通知，等待人工介入
   → 未超过：继续

2. 计算 backoff 时间（initial × multiplier^(n-1)，不超过 max），进入 backoff 状态
   第1次: 1m, 第2次: 1.5m, 第3次: 2.25m, ..., 上限: 5m

3. backoff 到期后，刷新分支最新 commit SHA，在该 commit 上发评论触发重试：
   $ gh api repos/{org}/{repo}/commits/{sha}/comments \
       -f body="/test tp-all-in-one branch:release-1.6"

4. 进入 settling 状态，等待 retry_settle_delay（默认 90s）
   等待 CI 系统创建新的 PipelineRun

5. settling 到期后，通过 gh 重新查询 check-runs，获取新的 PipelineRun 名称：
   → 从 details_url 解析出新的 PipelineRun name + namespace
   → 更新追踪信息

6. 递增重试计数，状态变为 watching，继续监控新的 PipelineRun

7. GH API 消耗：每次重试 2 请求（1 发评论 + 1 查 check-runs）
```

**SHA 刷新策略**：在重试或流水线成功时，重新获取分支最新 commit SHA，
以应对 porch 运行期间分支上可能出现的新 commit（如 renovate 再次提交）。
正常轮询周期内不刷新 SHA，避免不必要的 GH API 消耗。

### 6.4 流水线状态机

```
                             ┌──────────────────────────┐
                             ↓                          │
  MISSING ──→ WATCHING/RUNNING ──→ SUCCEEDED            │
  (初始无    (监控中)              (完成)                │
   check-run)     │                                     │
                  ↓                                     │
               FAILED ──→ BACKOFF ──→ SETTLING ──→ WATCHING
                             (等待退避)    (等待新 run)
                  │
                  ↓ (超过 max_retries)
               EXHAUSTED

  特殊状态：
    PENDING      ──→ WATCHING（DAG 依赖满足后）
    QUERY_ERROR  ──→ WATCHING（查询恢复后自动回到 WATCHING）
    TIMEOUT      ──  全局超时，终态
```

状态说明：

| 状态 | 含义 |
|------|------|
| `missing` | 初始化时未在 GH check-runs 中找到对应的流水线 |
| `watching`/`running` | 正在监控中，流水线运行中 |
| `succeeded` | 流水线成功完成 |
| `failed` | 流水线失败，即将进入重试流程 |
| `backoff` | 等待退避计时器到期后触发重试 |
| `settling` | 已发送重试评论，等待新 PipelineRun 被创建 |
| `exhausted` | 重试次数耗尽，需人工介入 |
| `pending` | 等待 DAG 上游依赖完成 |
| `query_error` | 连续查询失败超过阈值 |
| `timeout` | 全局超时 |

### 6.5 依赖编排（Dependency Resolver）

```
编排逻辑：
  1. 解析依赖 DAG（检测循环依赖）
  2. 无依赖的组件 → 直接 WATCHING
  3. 有依赖的组件 → PENDING，等上游全部 SUCCESS 后进入 WATCHING
  4. 所有组件 SUCCESS → 执行 final_action
  5. 任何组件 EXHAUSTED → 发送企业微信通知
  6. 全局超时到达 → 标记所有未完成组件为 TIMEOUT + 发送通知
```

### 6.6 最终动作（Final Action）

当所有组件的所有流水线都成功时：

```
1. 解析 final_action.branch：
   - 若 CLI 传入了 --final-branch，优先使用 CLI 参数
   - 若配置了 branch，直接使用
   - 若配置了 branch_from_component，从对应组件的 revision 推导

2. 获取 tektoncd-operator 自身分支的最新 commit SHA：
   $ gh api repos/{org}/tektoncd-operator/commits/{branch}

3. 在该 commit 上评论触发 update-components 流水线：
   $ gh api repos/{org}/tektoncd-operator/commits/{sha}/comments \
       -f body="/test to-update-components branch:{branch}"

4. 这会触发 .tekton/pr-update-components.yaml 中定义的流水线，
   该流水线会自动执行 make update-components 并提交 PR。

5. 发送企业微信通知：所有组件已成功，final_action 已触发。
   Notification includes jump links (commit checks / branch / PipelineRun detail), which makes triage faster.

6. GH API 消耗：2 请求
```

### 6.7 通知内容与跳转链接

To reduce manual triage time, WeCom notifications include clickable links:

- `component` links to the current commit checks page:
  `https://github.com/{org}/{repo}/commit/{sha}/checks`
- `branch` links to the branch commits page:
  `https://github.com/{org}/{repo}/commits/{branch}/`
- `pipeline` links to the PipelineRun detail page:
  `{pipeline_console_base_url}/workspace/{namespace}~{workspace_name}~{namespace}/pipeline/pipelineRuns/detail/{pipelinerun}`
  (rendered directly in the `Pipeline` column; no extra `RunURL` column in webhook table)

发送策略（由 `notification.events` 控制）：

- `all_succeeded`：所有组件成功后发送最终通知（含汇总表与链接）
- `progress_report`：按 `notification.progress_interval` 周期发送进度快照（含链接）
- `component_exhausted`：某组件重试耗尽时发送
- `global_timeout`：全局超时时发送（可选）

通知使用企业微信 `markdown_v2` 格式，包含 GFM 风格的 Markdown 表格。
当记录很多时，会按 `notify_rows_per_message` 分片，并在接近企业微信 4k 限制时自动提前切段。

## 7. CLI 命令设计

### 7.1 `porch watch`（主命令，持续监控）

```bash
$ porch watch --config orchestrator.yaml
```

启动后进入终端实时模式，展示所有组件状态，每个轮询周期刷新：

```
Component                       Branch        Pipeline                   Status     Retries  Run
---------                       ------        --------                   ------     -------  ---
Summary: succeeded=7/11

catalog                         main          catalog-all-in-one         RUN              0  catalog-all-in-one-abc12
hubs-wrapper                    release-1.0   tha-all-in-one             OK               0  tha-all-in-one-def34
tektoncd-chain                  release-0.26  tc-all-in-one              RUN              1  tc-all-in-one-ghi56
tektoncd-enhancement            release-0.2   te-enhancement-all-in-one  OK               0  te-enhancement-all-in-one-jkl78
tektoncd-hub                    release-1.23  th-all-in-one              OK               0  th-all-in-one-mno90
tektoncd-manual-approval-gate   release-0.7   approval-all-in-one        OK               0  approval-all-in-one-pqr12
tektoncd-pac                    release-0.39  pac-all-in-one             RUN              0  pac-all-in-one-stu34
tektoncd-pipeline               release-1.6   tp-all-in-one              OK               0  tp-all-in-one-vwx56
tektoncd-pruner                 release-0.3   tpr-all-in-one             OK               0  tpr-all-in-one-yza78
tektoncd-result                 release-0.17  tr-all-in-one              OK               2  tr-all-in-one-bcd90
tektoncd-trigger                release-0.34  tt-all-in-one              OK               0  tt-all-in-one-efg12

Events:
[10:32:15] FAILED        tektoncd-chain/tc-all-in-one failed, retry_count=0
[10:32:15] RETRYING      tektoncd-chain/tc-all-in-one attempt=1 backoff=1m0s
[10:33:15] DRY_RETRY     would comment on tektoncd-chains@abc123de: /test tc-all-in-one branch:release-0.26
[10:34:46] RETRY_OK      tektoncd-chain/tc-all-in-one new_run=tc-all-in-one-ghi56
[10:35:20] SUCCESS       tektoncd-pac/pac-all-in-one
[10:40:00] SUCCESS       tektoncd-chain/tc-all-in-one
[11:15:30] FINAL_OK      final_action triggered branch=release-1.6
```

**状态缩写对照**：

| 缩写 | 含义 |
|------|------|
| `OK` | succeeded |
| `RUN` | running / watching |
| `FAIL` | failed |
| `EXHAUSTED` | 重试耗尽 |
| `PENDING` | 等待 DAG 依赖 |
| `QUERY_ERR` | 查询错误 |
| `TIMEOUT` | 全局超时 |

**行为**：
- 启动时执行一次初始化（gh 查询）
- 之后按 `watch.interval` 轮询，表格自动刷新
- 检测到失败自动触发重试（backoff → settling → watching）
- 所有成功后自动触发 final_action
- 全局超时到达后标记失败并退出
- 所有事件同时写入日志文件（`log.file` 配置）
- `Ctrl+C` 优雅退出，状态保存到 state file
- 默认在 `FINAL_OK` 后继续常驻监控；使用 `--exit-after-final-ok` 可在完成后退出

### 7.2 `porch status`（一次性查询）

```bash
$ porch status --config orchestrator.yaml
```

**行为**：执行一次初始化 + 状态查询，输出表格后退出。
适合在脚本中使用或快速查看当前状态。同样支持 `--probe-mode` 参数。

### 7.3 `porch retry`（手动重试）

```bash
# 重试指定组件的所有流水线
$ porch retry --config orchestrator.yaml --component tektoncd-chain

# 重试指定组件的指定流水线
$ porch retry --config orchestrator.yaml --component tektoncd-chain --pipeline tc-all-in-one

# 运行时覆盖目标分支
$ porch retry --config orchestrator.yaml --component tektoncd-chain --branch release-1.0

# 强制重试（即使当前 commit 上该流水线已成功）
$ porch retry --config orchestrator.yaml --component tektoncd-chain --force
```

**行为**：
- 先查询目标分支最新 commit 的 check-run
- 若目标流水线已成功则跳过触发（可用 `--force` 忽略该检查）
- 不受 backoff 和重试计数限制，立即发评论触发

### 7.4 通用参数

根命令参数（对所有子命令生效）：

```
--components-file   覆盖配置中的 components_file 路径
--final-branch      覆盖 final_action 的 branch（优先级最高）
--probe-mode        状态探测模式：auto|gh-only|kubectl-first
--log-level         日志级别：debug|info|warn|error
--verbose           启用 debug 日志（等效于 --log-level=debug）
```

watch 子命令参数：

```
--config, -c            配置文件路径（默认: ./orchestrator.yaml）
--state-file            状态文件路径（默认: 系统临时目录下按工作目录分隔；如需当前目录请显式指定 ./.porch-state.json）
--component             仅监控指定组件（进入单组件模式）
--pipeline              仅监控指定组件下的某条流水线（需配合 --component）
--branch                覆盖 --component 对应组件的分支（需配合 --component）
--branch-pattern        Filter branches by Go regexp under --component (mutually exclusive with --branch)
--exit-after-final-ok   FINAL_OK 后立即退出（默认不退出，保持常驻）
--dry-run               监控与计算执行，但不发送重试/final comment
```

Regex example for matching `main` and `release-*.*`:

```bash
porch watch --config orchestrator.yaml --component tektoncd-pipeline --branch-pattern "^(main|release-[0-9]+[.][0-9]+)$"
```

retry 子命令参数：

```
--config, -c        配置文件路径
--component         组件名（必填）
--pipeline          流水线名（可选）
--branch            运行时覆盖目标分支（不修改配置文件）
--force             即使流水线已成功也强制重试
--dry-run           只打印，不发送 gh comment
```

### 7.5 单组件模式（Scoped Watch）

当设置 `--component` 进入单组件模式时：

- 只输出该组件（以及可选的单条 pipeline）数据
- 忽略 DAG 依赖，直接观测
- `final_action` 自动禁用，避免单组件观测误触发全局动作
- 目标成功时发送一次成功通知（如果配置了 `wecom_webhook`）
- 若设置 `--exit-after-final-ok`，则目标成功后直接退出
- 跳过 state file 恢复（每次从 GH 初始化）

`--final-branch` 建议作为根命令参数使用，便于在不同发布分支复用同一套配置文件，例如：

```bash
porch --final-branch release-1.6 watch --config orchestrator.yaml
```

`--components-file` 也建议作为根命令参数使用，例如：

```bash
porch --components-file ./components-release-1.6.yaml watch --config orchestrator.yaml
```

## 8. State Store 设计

使用本地 JSON 文件（默认位于系统临时目录；也可通过 `--state-file` 指定为 `./.porch-state.json`），结构如下：

```json
{
  "version": 1,
  "started_at": "2026-03-02T10:00:00Z",
  "updated_at": "2026-03-02T11:15:30Z",
  "components": {
    "tektoncd-pipeline": {
      "branch": "release-1.6",
      "sha": "abc123def456",
      "namespace": "tektoncd-pipeline-pipelines",
      "pipelines": {
        "tp-all-in-one": {
          "status": "succeeded",
          "pipelinerun_name": "tp-all-in-one-abc12-push-1709xx",
          "retry_count": 0,
          "last_retry_at": null,
          "completed_at": "2026-03-02T10:15:30Z"
        }
      }
    },
    "tektoncd-chain": {
      "branch": "release-0.26",
      "sha": "def789ghi012",
      "namespace": "tektoncd-chain-pipelines",
      "pipelines": {
        "tc-all-in-one": {
          "status": "backoff",
          "pipelinerun_name": "tc-all-in-one-def78-push-1709xx",
          "retry_count": 1,
          "last_retry_at": null,
          "completed_at": null,
          "retry_after": "2026-03-02T10:33:17Z",
          "settle_after": null
        }
      }
    }
  },
  "final_action": {
    "triggered": false,
    "triggered_at": null
  }
}
```

**用途**：
- `porch watch` 断点续跑：重启后从 state file 恢复状态（包括重试计数、backoff/settling 计时器）
- 重试计数持久化：避免重启后重复重试
- 恢复时校验分支一致性：若 state 中记录的 branch 与当前配置不匹配，跳过该组件的恢复

**健壮性**：
- 原子写入：写入临时文件后 rename，避免写入中途崩溃导致文件损坏
- 文件锁：对 state file 使用文件锁，防止多个 `porch watch` 实例同时写入
- 恢复验证：从 state file 恢复时，在 kubectl-first 模式下会验证已记录的 PipelineRun 是否仍然存在

## 9. GH API 消耗分析

| 操作 | 请求数 | 时机 |
|---|---|---|
| 获取各组件最新 commit SHA | N | 启动时一次 |
| 获取 check-runs（提取 namespace） | N | 启动时一次 |
| GH 回退探测（check-runs + annotations） | 0 ~ M | kubectl 失败时或 gh-only 模式每次轮询 |
| 自动重试（发评论 + 重新查 check-runs） | 0 ~ 2R | 仅失败时（R = 总重试次数） |
| 重试/成功时刷新 SHA | 0 ~ S | 按需 |
| 触发 final_action | 2 | 全部成功时一次 |

**kubectl-first 模式**（推荐）：常态下不消耗 GH API，仅启动时 2N 次 + 重试时 2R 次。
正常情况约 **30~40 次** GH API 请求完成整个发版流程。

**gh-only 模式**：每次轮询消耗 N 次（或更多，如果触发 annotation 检测），
适合短期本地调试，不建议长时间高频使用。

## 10. 技术选型

| 维度 | 选择 | 理由 |
|---|---|---|
| 语言 | **Go** | Tekton 生态、团队熟悉 |
| K8s 交互 | **`kubectl`**（通过 `os/exec` 调用） | 轻量，复用用户已有 kubeconfig，无需引入 client-go 依赖 |
| GH 交互 | **`gh` CLI**（通过 `os/exec` 调用） | 复用用户已有的 gh 认证，无需管理 token |
| 配置格式 | **YAML** | 声明式，版本可控 |
| State Store | **JSON 文件**（原子写入 + 文件锁） | 轻量，无外部依赖 |
| TUI | **自定义纯文本表格** | 简洁对齐输出 + 事件日志区域，无第三方 TUI 依赖 |
| CLI 框架 | **cobra + viper** | Go 标准 CLI 框架，支持环境变量绑定 |
| 日志 | **logrus** | 结构化日志，支持 stdout + 文件双写 |
| 通知 | **企业微信 Webhook**（markdown_v2） | 简单 HTTP POST，无需额外依赖 |

### 关键依赖

```
github.com/spf13/cobra             # CLI 框架
github.com/spf13/viper             # 配置/环境变量绑定
github.com/sirupsen/logrus         # 结构化日志
gopkg.in/yaml.v3                   # YAML 解析
```

### 外部命令追踪

使用 `--verbose`（或 `--log-level debug`）可看到外部命令追踪日志，包括：
- `gh api` 调用的开始、耗时、失败摘要（body 内容会脱敏为 `<redacted>`）
- `kubectl get pipelinerun` 的开始、耗时、失败摘要（kubeconfig 路径会脱敏为 `<path>`）

## 11. 项目结构

```
porch/
├── cmd/
│   └── porch/
│       ├── main.go                # CLI 入口
│       ├── root.go                # 根命令 + 全局参数
│       ├── options.go             # 通用选项 + viper key 定义
│       ├── status.go              # status 子命令
│       ├── retry.go               # retry 子命令
│       ├── watch.go               # watch 子命令 + 主循环逻辑
│       ├── runtime.go             # 配置加载 + 日志初始化
│       └── probe_mode.go          # 探测模式解析
├── pkg/
│   ├── config/
│   │   ├── types.go               # 配置类型定义（Root, Duration 等）
│   │   ├── loader.go              # 配置加载（orchestrator.yaml + components.yaml 合并）
│   │   └── validate.go            # 配置校验
│   ├── component/
│   │   └── doc.go                 # 组件初始化（gh 获取 SHA + check-run 解析）
│   ├── watcher/
│   │   ├── probe.go               # kubectl 探测 PipelineRun 状态
│   │   ├── checkrun.go            # GH check-run → ProbeResult 转换
│   │   └── watcher.go             # 状态推导（query error 阈值等）
│   ├── retrier/
│   │   └── retrier.go             # BackoffDuration 计算 + PipelineRun 重发现
│   ├── resolver/
│   │   └── dag.go                 # 依赖 DAG 解析（循环检测 + 就绪判断）
│   ├── state/
│   │   ├── types.go               # State 类型定义
│   │   └── store.go               # JSON state file 读写（原子写入 + 文件锁）
│   ├── gh/
│   │   └── client.go              # gh CLI 封装（BranchSHA / CheckRuns / Annotations / Comment）
│   ├── notify/
│   │   └── wecom.go               # 企业微信 Webhook 通知（markdown_v2）
│   └── tui/
│       └── table.go               # 终端表格 + Markdown 表格渲染
├── testdata/                      # 测试配置与 fixtures
├── docs/                          # 补充文档
├── Makefile
└── go.mod
```

## 12. 实施计划

### Phase 1：核心功能（已完成）

- [x] 项目脚手架（cobra + viper CLI + 配置类型）
- [x] 配置加载（orchestrator.yaml + components.yaml 合并 + 校验）
- [x] gh 封装（SHA 查询、check-runs 查询、annotations 查询、commit comment）
- [x] `porch status`：gh 初始化 + kubectl/gh 查询 + 表格输出
- [x] `porch retry`：手动触发单个组件重试（支持 --branch / --force）
- [x] JSON state file 读写（原子写入 + 文件锁）
- [x] 事件日志文件写入（logrus 双写）

### Phase 2：自动化（已完成）

- [x] `porch watch`：持续监控 + TUI 实时刷新
- [x] 自动重试（失败检测 + backoff + settling + 评论触发 + PipelineRun 重新发现）
- [x] SHA 刷新策略（重试/成功时刷新）
- [x] 依赖 DAG 编排
- [x] final_action 自动触发
- [x] 全局超时机制
- [x] 企业微信 Webhook 通知（progress_report / all_succeeded / component_exhausted）
- [x] 断点续跑（从 state file 恢复 + 分支一致性校验）
- [x] 单组件观测模式（--component / --pipeline / --branch）
- [x] 三种 probe-mode（auto / gh-only / kubectl-first）
- [x] GH 回退 + annotation 检测 + run mismatch 检测
- [x] 外部命令追踪日志（gh / kubectl 耗时、错误摘要、脱敏）

### Phase 3：增强（可选）

- [ ] K8s Informer 替代轮询（实时响应）
- [ ] 多 release 并行管理（多个 orchestrator.yaml）
- [ ] `porch init`：交互式生成 orchestrator.yaml

## 13. 已确认事项

### 13.1 Namespace 发现机制

从 GitHub check-run 的 `details_url` 中解析 PipelineRun 的命名空间。
已确认当前 URL 模式为：

`/workspace/<namespace>~<workspace_name>~<namespace>/pipeline/pipelineRuns/detail/<pipelinerun_name>`

`<workspace_name>` defaults to `business-build` and can be overridden by `connection.pipeline_workspace_name`.

实现中会校验两个 `<namespace>` 一致后再使用；若不一致或解析失败则记录 detection notes。

### 13.2 各组件流水线名称

上面配置中的流水线名称（`tp-all-in-one`、`pac-all-in-one` 等）已在最新 check-runs 核验。
详见 `docs/component-pipeline-validation.md`。

### 13.3 重试评论格式

格式为 `/test {pipeline_name} branch:{branch}`，
`{branch}` 在运行时会被替换为实际分支名。各子组件的 PAC `on-comment` 注解已确认支持此格式。

### 13.4 Check-run 匹配策略

当同一 pipeline 在同一 commit 上有多条 check-run 记录时（例如重试产生了新的 run），
porch 取 `check_run.id` 最大的那条作为最新状态来源。同时支持通过 `details_url`
或 `external_id` 解析 PipelineRun 名称。

### 13.5 Check-run 逻辑名称

check-run 的 `name` 字段可能带前缀（如 `repo/pipeline-name`），
porch 取最后一个 `/` 之后的部分作为逻辑名称进行匹配。
