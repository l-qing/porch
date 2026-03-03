# Porch — 多组件流水线编排器

> **P**ipeline **ORCH**estrator：自动监控、智能重试、实时通知，一键搞定多组件发版

## 解决什么问题

`tektoncd-operator` 有 11 个子组件，每次发版需要：

1. 人工逐一检查 11 个组件的流水线状态
2. 失败时手动通过评论触发重试
3. 全部成功后手动触发聚合操作

**11 个组件 × 多次重试 = 每次发版 4-8 小时人工轮询**

Porch 把这个过程变成一条命令：

```bash
porch watch -c orchestrator.yaml --exit-after-final-ok
```

## 核心能力

| 能力 | 说明 |
|------|------|
| 多组件并行监控 | 同时追踪 11+ 组件的流水线状态 |
| 智能重试 | 失败后指数退避自动重试，无需人工介入 |
| 多分支展开 | 支持 `branch_patterns` 正则，一次巡检 main + 所有 release 分支 |
| 企业微信通知 | 进度报告 + 成功/失败通知，带 GitHub 跳转链接 |
| 双路径探测 | kubectl 优先（零 API 消耗），GH check-runs 作为回退 |
| DAG 依赖编排 | 上游全部成功后才启动下游组件监控 |
| 断点续跑 | 进程重启后从 state file 恢复，不丢失重试计数 |

## 典型场景

```bash
# 发版：监控全部组件，成功后自动触发 final_action
porch watch -c orchestrator.yaml --exit-after-final-ok

# 巡检：监控所有组件的所有分支，不触发 final_action
porch watch -c orchestrator.yaml --disable-final-action --probe-mode gh-only

# 值守：只盯一个组件的一个分支
porch watch -c orchestrator.yaml --component tektoncd-pipeline --branch main

# 手动重试
porch retry -c orchestrator.yaml --component tektoncd-chain --pipeline tc-all-in-one

# 快速查看状态
porch status -c orchestrator.yaml --probe-mode gh-only
```

## 技术栈

Go + Cobra/Viper + gh CLI + kubectl，无重型依赖，开箱即用。

## 开发方式

全程 Claude Code (Opus 4.6) + Codex (codex-5.3) 多智能体协作，从 800+ 行设计文档生成 5,000+ 行 Go 代码。
