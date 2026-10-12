# Security policy

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's
[private vulnerability reporting](../../security/advisories/new) instead
(*Security → Report a vulnerability*). You will get an answer within a few
working days.

## Scope

- The application code of this repository (Go service, templates, JavaScript).
- The Terraform configuration in `deploy/terraform`.

## Design notes

The security model (IAP authentication, authorization, CSRF, CSP, secrets in
Secret Manager, least-privilege IAM, audit log) is described in the
[Security](../../wiki/Security) page of the wiki. No secret is stored in this
repository: integration settings in `config/settings*.yaml` only name the
environment variables that hold secrets.
