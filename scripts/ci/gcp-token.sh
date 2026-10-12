#!/bin/sh
# Prints a short-lived Google access token of the deployer service account,
# obtained from the OIDC token GitLab gives to the job (Workload Identity
# Federation): no service account key anywhere.
#
#   GitLab OIDC token ──STS──▶ federated token ──IAM Credentials──▶ deployer token
#
# Needs curl and the variables GCP_ID_TOKEN (id_tokens: in .gitlab-ci.yml),
# GCP_WIF_PROVIDER and GCP_DEPLOYER_SA (`terraform output gitlab_ci_variables`).
# The token goes to stdout only: never echo it.
set -eu
: "${GCP_ID_TOKEN:?missing (id_tokens in .gitlab-ci.yml)}"
: "${GCP_WIF_PROVIDER:?missing CI/CD variable}"
: "${GCP_DEPLOYER_SA:?missing CI/CD variable}"

# json_field NAME: value of a string field of a JSON document read on stdin.
json_field() { sed -n "s/.*\"$1\" *: *\"\([^\"]*\)\".*/\1/p" | head -n 1; }

federated=$(curl -sS --fail-with-body https://sts.googleapis.com/v1/token \
  --data-urlencode "grant_type=urn:ietf:params:oauth:grant-type:token-exchange" \
  --data-urlencode "audience=//iam.googleapis.com/${GCP_WIF_PROVIDER}" \
  --data-urlencode "scope=https://www.googleapis.com/auth/cloud-platform" \
  --data-urlencode "requested_token_type=urn:ietf:params:oauth:token-type:access_token" \
  --data-urlencode "subject_token_type=urn:ietf:params:oauth:token-type:jwt" \
  --data-urlencode "subject_token=${GCP_ID_TOKEN}" | json_field access_token)
if [ -z "$federated" ]; then
  echo "Token exchange refused: check GCP_WIF_PROVIDER and that the branch / tag is protected." >&2
  exit 1
fi

token=$(curl -sS --fail-with-body -X POST \
  "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/${GCP_DEPLOYER_SA}:generateAccessToken" \
  -H "Authorization: Bearer ${federated}" -H "Content-Type: application/json" \
  -d '{"scope":["https://www.googleapis.com/auth/cloud-platform"]}' | json_field accessToken)
if [ -z "$token" ]; then
  echo "Impersonation of ${GCP_DEPLOYER_SA} refused: check roles/iam.workloadIdentityUser (terraform cicd.tf)." >&2
  exit 1
fi
printf '%s' "$token"
