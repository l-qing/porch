#!/usr/bin/env bash
set -uo pipefail

MODE="dry-run"
if [[ "${1:-}" == "--execute" ]]; then
  MODE="execute"
fi

ORG="TestGroup"

rows=(
  "tektoncd-pipeline|tektoncd-pipeline|release-1.6|tp-all-in-one"
  "hubs-wrapper|tektoncd-hubs-api|release-1.0|tha-all-in-one"
  "tektoncd-enhancement|tektoncd-enhancement|release-0.2|te-enhancement-all-in-one"
  "tektoncd-pac|tektoncd-pipelines-as-code|release-0.39|pac-all-in-one"
  "tektoncd-chain|tektoncd-chains|release-0.26|tc-all-in-one"
  "tektoncd-hub|tektoncd-hub|release-1.23|th-all-in-one"
  "tektoncd-trigger|tektoncd-triggers|release-0.34|tt-all-in-one"
  "tektoncd-result|tektoncd-results|release-0.17|tr-all-in-one"
  "tektoncd-pruner|tektoncd-pruner|release-0.3|tpr-all-in-one"
  "tektoncd-manual-approval-gate|tektoncd-manual-approval-gate|release-0.7|approval-all-in-one"
  "catalog|catalog|main|catalog-all-in-one"
)

echo "mode=${MODE}"
echo "org=${ORG}"

for row in "${rows[@]}"; do
  IFS='|' read -r component repo branch pipeline <<< "${row}"

  if ! sha="$(gh api "repos/${ORG}/${repo}/commits/${branch}" --jq '.sha' 2>/tmp/porch-b02.err)"; then
    echo "---"
    echo "component=${component} repo=${repo} branch=${branch} pipeline=${pipeline}"
    echo "error=$(tr '\n' ' ' </tmp/porch-b02.err)"
    echo "triggered=false"
    continue
  fi
  body="/test ${pipeline} branch:${branch}"

  echo "---"
  echo "component=${component} repo=${repo} branch=${branch} sha=${sha:0:8} pipeline=${pipeline}"
  echo "comment=${body}"

  if [[ "${MODE}" == "execute" ]]; then
    gh api "repos/${ORG}/${repo}/commits/${sha}/comments" -f "body=${body}" >/dev/null
    echo "triggered=true"
  else
    echo "triggered=false"
  fi
done
