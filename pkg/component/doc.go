package component

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"porch/pkg/config"
	"porch/pkg/gh"
	pipestatus "porch/pkg/pipeline"
)

type PipelineRuntime struct {
	Name           string
	CheckRunName   string
	Status         string
	Conclusion     string
	DetailsURL     string
	Namespace      string
	PipelineRun    string
	DetectionNotes string
}

type RuntimeComponent struct {
	Name      string
	Repo      string
	Branch    string
	SHA       string
	Pipelines []PipelineRuntime
}

var workspaceNS = regexp.MustCompile(`/workspace/([^~/]+)~[^~/]+~([^/]+)/pipeline/`)
var runName = regexp.MustCompile(`/detail/([^/?#]+)$`)

func Initialize(ctx context.Context, cfg config.RuntimeConfig, ghc *gh.Client) ([]RuntimeComponent, error) {
	out := make([]RuntimeComponent, 0, len(cfg.Components))
	for _, c := range cfg.Components {
		sha, err := ghc.BranchSHA(ctx, c.Repo, c.Branch)
		if err != nil {
			return nil, fmt.Errorf("component %s branch sha: %w", c.Name, err)
		}

		checkRuns, err := ghc.CheckRuns(ctx, c.Repo, sha)
		if err != nil {
			return nil, fmt.Errorf("component %s check-runs: %w", c.Name, err)
		}

		runtime := RuntimeComponent{Name: c.Name, Repo: c.Repo, Branch: c.Branch, SHA: sha}
		for _, p := range c.Pipelines {
			pr := PipelineRuntime{Name: p.Name, Status: pipestatus.StatusMissing.String(), Conclusion: "-"}
			if cr, ok := FindPipelineCheckRun(checkRuns, p.Name); ok {
				pr.CheckRunName = cr.Name
				pr.Status = cr.Status
				pr.Conclusion = cr.Conclusion
				pr.DetailsURL = cr.DetailsURL
				ns, run, note := ParseDetailsURLForPipeline(cr.DetailsURL, p.Name)
				pr.Namespace = ns
				pr.PipelineRun = run
				pr.DetectionNotes = note
			} else {
				pr.DetectionNotes = "exact pipeline check-run not found"
			}
			runtime.Pipelines = append(runtime.Pipelines, pr)
		}
		out = append(out, runtime)
	}
	return out, nil
}

func ParseDetailsURL(url string) (namespace, pipelinerun, note string) {
	nsMatch := workspaceNS.FindStringSubmatch(url)
	runMatch := runName.FindStringSubmatch(url)
	if len(nsMatch) == 3 {
		if nsMatch[1] == nsMatch[2] {
			namespace = nsMatch[1]
		} else {
			note = "namespace mismatch in details_url"
		}
	} else {
		note = "namespace not found in details_url"
	}
	if len(runMatch) == 2 {
		pipelinerun = runMatch[1]
	}
	return namespace, pipelinerun, note
}

func ParseDetailsURLForPipeline(url, pipeline string) (namespace, pipelinerun, note string) {
	namespace, pipelinerun, note = ParseDetailsURL(url)
	if pipelinerun == "" {
		return namespace, pipelinerun, note
	}
	// Check-run details_url often points to child task runs.
	// Normalize to the logical parent PipelineRun key used by watch/retry state.
	return namespace, NormalizePipelineRunName(pipeline, pipelinerun), note
}

func NormalizePipelineRunName(pipeline, actual string) string {
	prefix := pipeline + "-"
	if !strings.HasPrefix(actual, prefix) {
		return actual
	}
	rest := strings.TrimPrefix(actual, prefix)
	if rest == "" {
		return actual
	}
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) == 0 || parts[0] == "" {
		return actual
	}
	return prefix + parts[0]
}

func LogicalCheckRunName(checkRunName string) string {
	parts := strings.Split(checkRunName, "/")
	if len(parts) == 0 {
		return strings.TrimSpace(checkRunName)
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func PipelineRunFromCheckRun(r gh.CheckRun, pipeline string) string {
	_, run, _ := ParseDetailsURL(strings.TrimSpace(r.DetailsURL))
	if run == "" {
		run = strings.TrimSpace(r.ExternalID)
	}
	if run == "" {
		return ""
	}
	return NormalizePipelineRunName(pipeline, run)
}

func FindPipelineCheckRun(runs []gh.CheckRun, pipeline string) (gh.CheckRun, bool) {
	var selected gh.CheckRun
	found := false
	useID := false
	var maxID int64

	for _, r := range runs {
		if LogicalCheckRunName(r.Name) == pipeline {
			if !found {
				selected = r
				found = true
				if r.ID > 0 {
					useID = true
					maxID = r.ID
				}
				continue
			}
			if r.ID > 0 {
				// Prefer the newest GitHub check-run when multiple logical names match.
				// This avoids stale status from older retries on the same commit.
				if !useID || r.ID > maxID {
					selected = r
					useID = true
					maxID = r.ID
				}
			}
		}
	}
	return selected, found
}

func FindPipelineCheckRunForRun(runs []gh.CheckRun, pipeline, pipelineRun string) (gh.CheckRun, bool) {
	targetRun := NormalizePipelineRunName(pipeline, strings.TrimSpace(pipelineRun))
	if targetRun == "" {
		return FindPipelineCheckRun(runs, pipeline)
	}

	var selected gh.CheckRun
	found := false
	useID := false
	var maxID int64

	for _, r := range runs {
		if LogicalCheckRunName(r.Name) != pipeline {
			continue
		}
		// Match by normalized run name first to avoid cross-run contamination
		// when GitHub still keeps multiple check-runs for the same pipeline.
		run := PipelineRunFromCheckRun(r, pipeline)
		if run != targetRun {
			continue
		}
		if !found {
			selected = r
			found = true
			if r.ID > 0 {
				useID = true
				maxID = r.ID
			}
			continue
		}
		if r.ID > 0 {
			if !useID || r.ID > maxID {
				selected = r
				useID = true
				maxID = r.ID
			}
		}
	}

	return selected, found
}
