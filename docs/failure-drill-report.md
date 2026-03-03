# M4-04 故障演练报告

日期：2026-03-02

## 场景与结果

| 场景 | 注入方式 | 结果 |
|---|---|---|
| GH 失败 | `porch status` 使用无效 org 配置 `testdata/orchestrator.badorg.yaml` | 命令优雅失败，返回明确错误（HTTP 404），无 panic |
| kubectl 网络错误/超时 | `porch watch --dry-run` 在当前不可达集群环境运行 | 连续记录 `QUERY_WARN`，到阈值后进入 `QUERY_ERROR` 路径，进程未崩溃 |
| 全局超时 | `testdata/orchestrator.e2e.yaml` 设置 `timeout.global=10s` | 超时触发 `TIMEOUT` 事件，状态落盘完成 |
| 文件锁竞争 | `go test ./pkg/state -run TestSaveConcurrentNoCorruption -v` | 并发写场景通过，state 文件无损坏 |
| 进程中断 | 运行 `watch` 后发送 `SIGINT` | 进程优雅退出，state 文件存在且 JSON 可解析 |

## 关键证据

- GH 失败日志：`porch status error: component tektoncd-pipeline branch sha ... gh: Not Found (HTTP 404)`
- kubectl 错误日志：`QUERY_WARN ... exit status 1` + `TIMEOUT ... context_deadline_exceeded`
- 中断落盘验证：`testdata/.porch-state.interrupt.json` 可读取，`version=1`，`components=1`

## 结论

- `M4-04` 验收通过：错误路径均有明确日志，且均未导致进程崩溃。
- 演练与实现匹配清单要求：`gh` 失败、`kubectl` 异常、文件锁竞争、进程中断均已覆盖。
