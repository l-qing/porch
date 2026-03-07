package main

import (
	"fmt"
	"net/url"
	"strings"

	"porch/pkg/config"
)

const pipelineConsoleBaseURL = "https://edge.alauda.cn/console-pipeline-v2"
const pipelineWorkspaceName = "business-build"

// pipelineRunDetailURL builds the stable Console path from namespace and PipelineRun name.
func pipelineRunDetailURL(namespace, pipelineRun string, conn config.Connection) string {
	ns := strings.TrimSpace(namespace)
	run := strings.TrimSpace(pipelineRun)
	if ns == "" || run == "" {
		return ""
	}
	baseURL := resolvePipelineConsoleBaseURL(conn.PipelineConsoleBaseURL)
	workspaceName := resolvePipelineWorkspaceName(conn.PipelineWorkspaceName)
	return fmt.Sprintf(
		"%s/workspace/%s~%s~%s/pipeline/pipelineRuns/detail/%s",
		strings.TrimRight(baseURL, "/"),
		url.PathEscape(ns),
		url.PathEscape(workspaceName),
		url.PathEscape(ns),
		url.PathEscape(run),
	)
}

func resolvePipelineConsoleBaseURL(raw string) string {
	baseURL := strings.TrimSpace(raw)
	if baseURL == "" {
		return pipelineConsoleBaseURL
	}
	return baseURL
}

func resolvePipelineWorkspaceName(raw string) string {
	workspaceName := strings.TrimSpace(raw)
	if workspaceName == "" {
		return pipelineWorkspaceName
	}
	return workspaceName
}
