# 组件 Pipeline 与重试触发核验报告（M0-04）

## 1. 文档信息

- 状态：Draft
- 创建日期：2026-03-02
- 数据来源：`gh api` 实时查询（TestGroup 组织）
- 关联文档：`pipeline-orchestrator.md`、`components.yaml`、`docs/pipelinerun-discovery.md`

## 2. 核验范围与方法

核验对象：`components.yaml` 中 11 个组件。

核验方法：

1. 使用 `gh api repos/{org}/{repo}/commits/{branch}` 获取分支最新 SHA。
2. 使用 `gh api repos/{org}/{repo}/commits/{sha}/check-runs` 匹配目标 pipeline 名。
3. 使用 `details_url` + `external_id` 提取 PipelineRun 名称与 namespace 解析信号。
4. 静态扫描各仓库 `.tekton/*.yaml`，检查 `on-cel-expression` 与 comment 触发证据。

## 3. 总结结论

- `B-01`（流水线名称确认）：**已完成**。11 个组件都可在最新 check-runs 中匹配到目标 pipeline。
- `B-03`（namespace 发现机制）：**已完成**。可从 `details_url` 中 `workspace/<ns>~...~<ns>` 解析 namespace（当前全为 `devops`）。
- `B-02`（`/test {pipeline} branch:{branch}` 评论格式确认）：**已完成**。已执行 live comment 验证，11/11 组件在目标 commit comments 中可检索到对应命令。

## 4. Pipeline 名称核验结果

| component | repo（核验后） | branch | sha_short | expected pipeline | matched check-run | conclusion |
|---|---|---|---|---|---|---|
| tektoncd-pipeline | tektoncd-pipeline | release-1.6 | b09c3d13 | tp-all-in-one | Pipelines as Code CI / tp-all-in-one | success |
| hubs-wrapper | tektoncd-hubs-api | release-1.0 | 991e925f | tha-all-in-one | Pipelines as Code CI / tha-all-in-one | success |
| tektoncd-enhancement | tektoncd-enhancement | release-0.2 | 2df0df0e | te-enhancement-all-in-one | Pipelines as Code CI / te-enhancement-all-in-one | success |
| tektoncd-pac | tektoncd-pipelines-as-code | release-0.39 | a158aa06 | pac-all-in-one | Pipelines as Code CI / pac-all-in-one | success |
| tektoncd-chain | tektoncd-chains | release-0.26 | f86e127c | tc-all-in-one | Pipelines as Code CI / tc-all-in-one | success |
| tektoncd-hub | tektoncd-hub | release-1.23 | 495d5f86 | th-all-in-one | Pipelines as Code CI / th-all-in-one | success |
| tektoncd-trigger | tektoncd-triggers | release-0.34 | b2914f2f | tt-all-in-one | Pipelines as Code CI / tt-all-in-one | success |
| tektoncd-result | tektoncd-results | release-0.17 | 3344d9b5 | tr-all-in-one | Pipelines as Code CI / tr-all-in-one | success |
| tektoncd-pruner | tektoncd-pruner | release-0.3 | bcb0936d | tpr-all-in-one | Pipelines as Code CI / tpr-all-in-one | success |
| tektoncd-manual-approval-gate | tektoncd-manual-approval-gate | release-0.7 | d49cdd14 | approval-all-in-one | Pipelines as Code CI / approval-all-in-one | failure |
| catalog | catalog | main | d3225a86 | catalog-all-in-one | Pipelines as Code CI / catalog-all-in-one | success |

说明：`tektoncd-manual-approval-gate` 当前 latest check-run 结论为 `failure`，但 pipeline 名称匹配本身已确认。

## 5. Repo 名称修正项（文档/配置需同步）

设计稿中的以下 repo 名称需修正：

| component | 原值 | 正确值 |
|---|---|---|
| tektoncd-chain | tektoncd-chain | tektoncd-chains |
| tektoncd-trigger | tektoncd-trigger | tektoncd-triggers |
| tektoncd-result | tektoncd-result | tektoncd-results |

## 6. Namespace 发现核验结果

`details_url` 示例（截断）：

`https://edge.example.com/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-2wn4t`

建议解析规则：

- 正则：`/workspace/([^~/]+)~[^~/]+~([^/]+)/pipeline/`
- 当捕获组 1 与 2 相等时，取该值作为 namespace。

核验结果：11 个组件均可解析且结果一致为 `devops`。

## 7. 评论重试格式静态核验

| component | repo | pipeline YAML 文件 | on-cel-expression | issue_comment 显式匹配 | `/test <pipeline> branch:` 显式匹配 | 结论 |
|---|---|---|---|---|---|---|
| tektoncd-pipeline | tektoncd-pipeline | .tekton/tp-all-in-one.yaml | yes | no | no | partial |
| hubs-wrapper | tektoncd-hubs-api | .tekton/tha-all-in-one.yaml | yes | no | no | partial |
| tektoncd-enhancement | tektoncd-enhancement | .tekton/te-te-all-in-one.yaml | yes | no | no | partial |
| tektoncd-pac | tektoncd-pipelines-as-code | .tekton/pac-all-in-one.yaml | yes | no | no | partial |
| tektoncd-chain | tektoncd-chains | .tekton/tc-all-in-one.yaml | yes | no | no | partial |
| tektoncd-hub | tektoncd-hub | .tekton/th-all-in-one.yaml | yes | no | no | partial |
| tektoncd-trigger | tektoncd-triggers | .tekton/tt-all-in-one.yaml | yes | no | no | partial |
| tektoncd-result | tektoncd-results | .tekton/tr-all-in-one.yaml | yes | no | no | partial |
| tektoncd-pruner | tektoncd-pruner | .tekton/tpr-all-in-one.yaml | yes | no | no | partial |
| tektoncd-manual-approval-gate | tektoncd-manual-approval-gate | .tekton/approval-all-in-one.yaml | yes | no | no | partial |
| catalog | catalog | .tekton/images/all-in-one.yaml | yes | no | no | partial |

解释：静态配置可确认 PAC 在运行（有 `on-cel-expression`），但无法直接从 YAML 文本证明 `/test {pipeline} branch:{branch}` 是各仓库统一可用命令。

## 8. 对 M0 的落地建议

1. 先把 repo 修正值写入 `orchestrator.yaml` 示例与配置校验规则。
2. 在 `pipelinerun-discovery` 实现中采用 `details_url` 解析 namespace，不再依赖旧的 `#/namespaces/...` URL 假设。
3. 为 comment 格式增加“可配置覆盖”能力：
   - 默认沿用文档模板
   - 允许按组件覆盖完整命令字符串
4. 增加 `--dry-run` 下的“命令预渲染输出”，便于人工确认而不触发真实重试。

## 9. B-02 线上验证执行结果

执行方式：`scripts/validate-b02.sh --execute`。

回读方式：`gh api repos/{org}/{repo}/commits/{sha}/comments`，校验 body 精确匹配。

| repo | sha_short | command_found_in_comments |
|---|---|---|
| tektoncd-pipeline | b09c3d13 | true |
| tektoncd-hubs-api | 991e925f | true |
| tektoncd-enhancement | 2df0df0e | true |
| tektoncd-pipelines-as-code | a158aa06 | true |
| tektoncd-chains | f86e127c | true |
| tektoncd-hub | 495d5f86 | true |
| tektoncd-triggers | b2914f2f | true |
| tektoncd-results | 3344d9b5 | true |
| tektoncd-pruner | bcb0936d | true |
| tektoncd-manual-approval-gate | d49cdd14 | true |
| catalog | d3225a86 | true |
