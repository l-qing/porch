# porch

`porch` 是一个用于多组件流水线编排的 CLI，目标是自动化完成以下流程：

- 聚合查询多个组件的 Pipeline 状态
- 在失败时自动重试（基于 gh comment）
- 支持依赖 DAG 编排
- 在全部成功后触发 final action

当前项目已实现 `status` / `retry` / `watch` 命令基础能力，以及 Makefile 驱动的构建与验证流程。

## 快速开始

运行前置（GitHub CLI 认证）：

- `porch` 的 `status/retry/watch` 都会调用 `gh api`，需要提前完成 `gh` 登录认证。
- 建议在执行前先检查认证状态：`gh auth status`
- 若未认证，可执行：`gh auth login`

```bash
# 构建
make build

# 查看帮助
./bin/porch --help

# 一次性状态查询
./bin/porch status --config ./testdata/orchestrator.e2e.yaml

# 手动重试（演练模式）
./bin/porch retry --config ./testdata/orchestrator.e2e.yaml --component tektoncd-pipeline --pipeline tp-all-in-one --dry-run

# 手动重试（运行时覆盖目标分支，不改配置文件）
./bin/porch retry --config ./testdata/orchestrator.e2e.yaml --component tektoncd-pipeline --pipeline tp-all-in-one --branch release-1.0 --dry-run

# 强制重试（即使当前 commit 上该流水线已成功）
./bin/porch retry --config ./testdata/orchestrator.e2e.yaml --component tektoncd-pipeline --pipeline tp-all-in-one --branch release-1.0 --force

# 持续监控（演练模式）
./bin/porch watch --config ./testdata/orchestrator.e2e.yaml --dry-run --state-file ./testdata/.porch-state.local.json

# 单组件/单流水线观测（运行时覆盖分支，不改配置文件）
./bin/porch watch --config ./testdata/orchestrator.e2e.yaml --component tektoncd-pipeline --pipeline tp-all-in-one --branch release-1.0 --dry-run

# Ad-hoc repo watch (repo not defined in components)
./bin/porch watch --config ./config.yaml --component tektoncd-operator --pipeline to-all-in-one --branch main --exit-after-final-ok

# 覆盖 components_file（适配不同分支的 components.yaml）
./bin/porch --components-file ./components-release-1.6.yaml watch --config ./testdata/orchestrator.e2e.yaml --dry-run

# 运行时覆盖 final_action.branch
./bin/porch --final-branch release-1.6 watch --config ./testdata/orchestrator.e2e.yaml --dry-run
```

## 命令说明

### `porch status`

一次性查询状态，流程为：

1. 读取 orchestrator 配置
2. gh 查询分支 SHA + check-runs
3. 尝试解析 `details_url` 获取 namespace / pipelinerun
4. 输出表格

常用参数：

- `-c, --config`：配置文件路径

### `porch retry`

手动触发重试评论，支持组件级和流水线级。
默认会先查询目标分支最新 commit 的 check-run；若目标流水线已成功则跳过触发。可用 `--force` 忽略该检查。

常用参数：

- `-c, --config`：配置文件路径
- `--component`：组件名（必填）
- `--pipeline`：流水线名（可选）
- `--branch`：运行时覆盖目标分支（可选，不会修改配置文件）
- `--force`：即使目标流水线已成功也强制触发重试（可选）
- `--dry-run`：只打印，不发送 gh comment

### `porch watch`

持续监控并自动重试。

常用参数：

- `-c, --config`：配置文件路径
- `--state-file`：状态文件路径
- `--final-branch`：覆盖 `final_action.branch`（优先级最高，可写在根命令或 watch 子命令位置）
- `--components-file`：覆盖配置中的 `components_file` 路径（根命令参数，对所有子命令生效）
- `--disable-final-action`：全局禁用 `final_action` 触发（根命令参数，对所有子命令生效）
- `--probe-mode`：状态探测模式，`auto|gh-only|kubectl-first`（根命令参数，对所有子命令生效）
- `--component`：仅监控指定组件（可选）
- `--pipeline`：仅监控指定组件下的某条流水线（可选，需配合 `--component`）
- `--branch`：仅覆盖 `--component` 对应组件分支（可选，需配合 `--component`）
- `--exit-after-final-ok`：`FINAL_OK` 后立即退出（默认不退出，保持常驻）
- `--dry-run`：监控与计算执行，但不发送重试/final comment

当设置 `--component` 进入单组件模式时：

- 只输出该组件（以及可选的单条 pipeline）数据
- 忽略 DAG 依赖，直接观测
- `final_action` 自动禁用，避免单组件观测误触发全局动作
- 若设置 `--exit-after-final-ok`，则目标成功后直接退出

Ad-hoc repo mode:

- If `--component` does not match any configured component but `--pipeline` is provided, porch will build a runtime target directly from `github_org + repo(--component)`.
- This mode does not require the repo to exist in `components`.
- The generated retry command is `/test <pipeline> branch:{branch}`.
- If `--branch` is not provided, branch defaults to `main`.

Example:

```bash
./bin/porch watch --config ./config.yaml --component tektoncd-operator --pipeline to-all-in-one --branch main --exit-after-final-ok
```

## 环境变量映射（viper）

CLI 使用 `cobra + viper`。环境变量前缀为 `PORCH`，键名中的 `.` 和 `-` 会被替换为 `_`。

例如：

- `status.config` -> `PORCH_STATUS_CONFIG`
- `retry.config` -> `PORCH_RETRY_CONFIG`
- `watch.config` -> `PORCH_WATCH_CONFIG`
- `watch.state_file` -> `PORCH_WATCH_STATE_FILE`
- `watch.exit_after_final_ok` -> `PORCH_WATCH_EXIT_AFTER_FINAL_OK`
- `final_branch` -> `PORCH_FINAL_BRANCH`
- `components_file` -> `PORCH_COMPONENTS_FILE`
- `disable_final_action` -> `PORCH_DISABLE_FINAL_ACTION`
- `probe_mode` -> `PORCH_PROBE_MODE`

示例：

```bash
export PORCH_WATCH_CONFIG=./testdata/orchestrator.e2e.yaml
export PORCH_WATCH_STATE_FILE=./testdata/.porch-state.env.json
export PORCH_WATCH_EXIT_AFTER_FINAL_OK=false
export PORCH_FINAL_BRANCH=release-1.6
export PORCH_COMPONENTS_FILE=./components-release-1.6.yaml
export PORCH_DISABLE_FINAL_ACTION=true
export PORCH_PROBE_MODE=gh-only
./bin/porch watch --dry-run
```

## 单组件多分支示例

目标：一次 `watch` 同时观测同一个仓库的多个分支（例如 `main` + 多个 `release-*`），失败自动重试，不触发 `tektoncd-operator` 的 `final_action`。

关键点：

- 在 `orchestrator.yaml` 里用 `components[].branches` 声明多分支。
- `branches` 存在时优先级高于 `components_file` 中的 `revision`，无需改外部拷贝来的 `components.yaml`。
- 运行时会自动展开为 `component@branch` 实例（用于日志、状态与依赖追踪）。
- 打开 `disable_final_action: true`（或命令行 `--disable-final-action`）。

示例 `orchestrator.multi-branch.yaml`：

```yaml
apiVersion: porch/v1
kind: ReleaseOrchestration
metadata:
  name: tektoncd-pipeline-multi-branch

connection:
  github_org: TestGroup
  kubeconfig: ""
  context: ""

watch:
  interval: 30s
  exit_after_final_ok: true

retry:
  max_retries: 5
  backoff:
    initial: 1m
    multiplier: 1.5
    max: 5m
  retry_settle_delay: 90s

timeout:
  global: 12h

notification:
  wecom_webhook: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=YOUR_KEY"
  events:
    - all_succeeded
    - component_exhausted
  progress_interval: 10m
  notify_rows_per_message: 12

log:
  file: ./.porch-events.log

disable_final_action: true
components_file: ./components.multi-branch.yaml

components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    branches:
      - main
      - release-1.8
      - release-1.9
    pipelines:
      - name: tp-all-in-one
        retry_command: "/test tp-all-in-one branch:{branch}"

final_action:
  repo: tektoncd-operator
  branch: ""
  branch_from_component: ""
  comment: "/test to-update-components branch:{branch}"
```

示例 `components.multi-branch.yaml`：

```yaml
tektoncd-pipeline:
  revision: release-0.1
```

执行命令：

```bash
./bin/porch watch \
  --config ./orchestrator.multi-branch.yaml \
  --probe-mode gh-only \
  --disable-final-action \
  --exit-after-final-ok \
  --verbose
```

如果不想手工列出所有 release 分支，可以改用 `branch_patterns`（Go 正则）：

```yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    branch_patterns:
      - "^main$"
      - "^release-[0-9]+\\.[0-9]+$"
    pipelines:
      - name: tp-all-in-one
        retry_command: "/test tp-all-in-one branch:{branch}"
```

说明：

- 只有配置了 `branch_patterns` 的组件，才会在启动时调用一次 GH 分支列表 API（`repos/{org}/{repo}/branches`）。
- 通过正则匹配出的分支集合会在启动时冻结，运行过程中不会动态增删。
- 未配置 `branch_patterns` 的组件不会触发这次全量分支查询。

## Makefile

常用目标：

- `make help`：查看全部目标
- `make check`：执行 `test + vet + build`
- `make run-status`：运行 status
- `make run-retry-dry`：运行 retry dry-run
- `make run-watch-dry`：运行 watch dry-run
- `make integration`：仅运行集成测试包
- `make failure-drill`：执行故障演练命令
- `make b02-dry`：B-02 评论校验 dry-run
- `make b02-exec`：执行真实评论校验（会触发流水线）

## 通知事件（企业微信）

`notification.events` 按事件名控制发送类型，当前支持：

- `all_succeeded`：所有组件成功完成后的最终通知
- `progress_report`：按 `notification.progress_interval` 周期发送进度快照
- `component_exhausted`：某组件重试耗尽
- `global_timeout`：全局超时（可选，通常与终端 `TIMEOUT` 日志重复）

通知中的表格会包含跳转链接：

- `component` 链接到对应 commit checks 页面
- `branch` 链接到仓库 branch 页面

推荐配置示例：

```yaml
notification:
  wecom_webhook: ""
  events:
    - all_succeeded
    - progress_report
    - component_exhausted
    # - global_timeout
  progress_interval: 30m
  notify_rows_per_message: 12
```

## 注意事项

- `b02-exec` 会向目标 commit 发真实评论，可能触发实际 Pipeline，请只在可控窗口执行。
- `status/retry/watch` 依赖 `gh` 已完成认证；可先执行 `gh auth status` 检查。
- `watch` 在集群不可达时会进入 `QUERY_WARN/QUERY_ERROR` 路径，并按超时退出。
- `watch` 默认在 `FINAL_OK` 后继续常驻；如需一次性执行后退出，可用 `--exit-after-final-ok` 或配置 `watch.exit_after_final_ok: true`。
- `disable_final_action` 可通过配置、`--disable-final-action` 或 `PORCH_DISABLE_FINAL_ACTION` 打开；开启后不会触发 `tektoncd-operator`。
- 即使禁用了 `final_action`，当所有目标流水线成功时仍会发送 `all_succeeded` 通知；如果再配合 `--exit-after-final-ok`，会输出最终表格并退出。
- `scoped watch`（`--component`）达到成功时，也会发送一次成功通知；若同时设置 `--exit-after-final-ok`，会先输出最终表格再退出。
- 通知会按 `notification.notify_rows_per_message`（默认 12）分片，并在接近企业微信 4k 限制时提前自动切段，避免超长被拒绝。
- `watch/status` 默认 `probe-mode=auto`：如果 `connection.kubeconfig/context` 都为空，会自动走 `gh-only`，不调用 kubectl。
- 可以通过 `--probe-mode gh-only`（或 `PORCH_PROBE_MODE=gh-only`）强制只信任 GH 结果，便于本地调试。
- 如果需要优先走集群查询，可显式指定 `--probe-mode kubectl-first`，并配置 `connection.kubeconfig/context`。
- 日志是双写：会输出到终端，同时写入 `log.file` 指定文件。
- 使用 `--verbose`（或 `--log-level debug`）可看到外部命令追踪日志（如 `gh api`、`kubectl get pipelinerun` 的开始/耗时/失败摘要）。
- `watch` 模式下终端会显示实时表格与 Events 区域；同样会持续写日志文件。
- 运行过程会写入日志与 state 文件，见 `.gitignore` 已忽略项。

## License

Copyright 2026 The porch Authors.

Licensed under the Apache License, Version 2.0. See [LICENSE](./LICENSE) for details.
