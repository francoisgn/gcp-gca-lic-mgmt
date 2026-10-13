---
name: github-wiki
description: Maintain the GitHub wiki of this repository. Use whenever documentation must be written or updated (architecture, functional spec, runbook, configuration...), when the user mentions "the wiki", or after a change that affects documented behaviour.
---

# Managing the wiki of this repository

The README stays short. **The real documentation is the GitHub wiki**, whose
sources are versioned in `docs/wiki/` and mirrored by
`.github/workflows/wiki.yml` on every merge to the default branch.

## Rules

1. Edit pages in `docs/wiki/` only — never in the wiki UI (overwritten at the next sync).
2. One page per topic. File name = page title with `-` instead of spaces
   (`Request-Lifecycle.md` → `[[Request Lifecycle]]`).
3. Link pages with `[[Page Name]]`, or `[[Page Name#anchor]]`.
4. Add every new page to `_Sidebar.md` and, if relevant, to the table of `Home.md`.
5. Images go to `docs/wiki/images/` and are referenced as `images/<file>.png`.
   Screenshots: run the app with demo data (see `Local-Development.md`) and capture
   with Playwright at 1440 px (and 390 px for mobile), hide the DEV bar.
6. Diagrams: Mermaid blocks (rendered by GitHub) or ASCII art in code blocks.
7. English, short sentences, tables over prose; RAG emojis 🟢 🟠 🔴 for statuses.
8. Never mention the company name or internal URLs: use `example.com`.
9. Update the page in the **same pull request** as the code change it documents.
   Design changes also go to `DESIGN.md` (changelog at the bottom).

## Map

| Page | Update when… |
|---|---|
| `Architecture.md`, `Architecture-Decisions.md` | a service, package or structural choice changes (add an ADR) |
| `Functional-Specification.md`, `Request-Lifecycle.md` | a screen, state, rule or status changes |
| `Google-APIs-and-Permissions.md` | an API call, permission or IAM role changes |
| `Enterprise-Directory.md` | the directory contract or simulator markers change |
| `Data-Model.md` | a migration is added |
| `Scheduled-Jobs.md` | a job or schedule changes (also `deploy/terraform/scheduler.tf`) |
| `KPIs-and-Observability.md` | a KPI or chart changes |
| `Configuration.md` | an environment variable or YAML key changes |
| `Deployment.md`, `Runbook.md`, `Security.md`, `Testing.md` | operations, security or tests change |
| `Open-Questions.md` | an assumption is confirmed (remove the row) or a new one is taken |

## Publishing

- Automatic on merge (`Publish wiki` workflow). Manual: *Actions → Publish wiki → Run workflow*.
- First time only: enable the wiki in the repository settings and create any page in the UI
  (GitHub creates the `.wiki.git` repository at that moment), then run the workflow.
- If the push is refused with the default token, add a `WIKI_TOKEN` secret
  (fine-grained token, *Contents: read and write* on this repository).
