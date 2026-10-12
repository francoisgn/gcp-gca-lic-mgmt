# GCA License Portal — documentation

Self-service portal managing **Gemini Code Assist Standard / Enterprise** licenses for the whole organization, running on Google Cloud.

![Landing](images/landing-active.png)

## What it does

| Area | Capabilities |
|---|---|
| **Users** | Landing page showing the state of *their* license · progressive request form · waiting list / buffer position · rejection history · feedback to the team |
| **Automation** | Automatic assignment (training + directory GPS code + free seat) · removal after 7 days without usage · removal at expiry · subscription renewal detection and license migration · retries of timed-out operations every 7 min |
| **Admins** | Escalation approval / rejection · manual assignment · atomic transfer · criteria-based mass removal · waiting list and buffer management |
| **Observability** | Subscription, waterfall of license flows, queue / buffer depth, deviations with Google, inventory, technical KPIs (directory errors, response times, timeouts, app access vs Google calls) |

## Read next

| If you want to… | Page |
|---|---|
| Understand the big picture | [[Architecture]] · [[Architecture Decisions]] |
| Know every screen and rule | [[Functional Specification]] · [[Request Lifecycle]] |
| Understand the Google side | [[Google APIs and Permissions]] |
| Plug the real enterprise directory | [[Enterprise Directory]] |
| Read the KPIs | [[KPIs and Observability]] |
| Run it on your laptop | [[Local Development]] · [[Testing]] |
| Deploy it | [[Configuration]] · [[Deployment]] |
| Operate it | [[Runbook]] · [[Scheduled Jobs]] · [[Security]] |
| Change the look | [[Design Language]] |
| Know what is still to validate | [[Open Questions]] |

## Key principles

- 🟢 **Google is the source of truth.** The portal database is a working copy; any difference is a tracked *deviation* until an admin resolves it.
- 🟢 **Nobody waits more than 10 seconds.** Slow Google or directory calls move the request to a *buffer* retried in the background; the seat stays reserved.
- 🟢 **Least privilege.** Licensed users get the Gemini IAM roles through a group; leaving the license removes them.
- 🟢 **Admins are code.** The admin list is a YAML file in git, changed through pull requests.
