---
description: Generate descriptions for undocumented resources in the topology
agent: build
---

Generate descriptions for targeted undocumented resources from config using main-session orchestration.

Important: do not launch an orchestrator subagent. The main session must coordinate the work because subagents may not be able to launch other subagents reliably.

Workflow:
1. Get the current targeted undocumented resources. Prefer `node_list_no_description` if it is available; otherwise run `arac node list --no-description`.
2. Split the returned resources into deterministic batches of at most 20 resources. Each resource ID must appear in exactly one active batch.
3. Launch one `descriptions-generation-executor` subagent for each batch. Give each executor only its assigned IDs, names, and kinds.
4. Executors must manually study each assigned resource with `read`, then call `update_description`. They must not automate by code and must not process unassigned resources.
5. After each wave of executors completes, get the undocumented resource list again. Treat the topology database as the source of truth, not executor self-reports.
6. Reassign any still-undocumented targeted resources to new executor subagents, again in batches of at most 20, without duplicating active assignments.
7. Continue until no targeted undocumented resources remain, or report any resources that still fail after repeated executor attempts.
