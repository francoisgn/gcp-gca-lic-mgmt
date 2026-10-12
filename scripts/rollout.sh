#!/bin/sh
# Rolls an image out to the portal: the Cloud Run service gcalm-web and every
# Cloud Run job gcalm-* (the scheduled jobs).
#
#   scripts/rollout.sh IMAGE REGION [PROJECT]
#
# Used by the GitLab pipeline (deploy job) and by hand (gcloud authenticated).
# Terraform creates the service and the jobs and then ignores image changes:
# rolling back = rolling out the previous image.
set -eu
image="${1:?usage: rollout.sh IMAGE REGION [PROJECT]}"
region="${2:?usage: rollout.sh IMAGE REGION [PROJECT]}"
project="${3:-${CLOUDSDK_CORE_PROJECT:-$(gcloud config get-value project 2>/dev/null)}}"

echo "Rolling out $image to $project / $region"
gcloud run services update gcalm-web --image="$image" --region="$region" --project="$project" --quiet
jobs=$(gcloud run jobs list --region="$region" --project="$project" \
  --filter='metadata.name ~ ^gcalm-' --format='value(metadata.name)')
for job in $jobs; do
  gcloud run jobs update "$job" --image="$image" --region="$region" --project="$project" --quiet
done
echo "Done: gcalm-web and $(echo "$jobs" | wc -w | tr -d ' ') job(s)."
