---
description: Generate descriptions for undocumented resources in the topology
agent: descriptor
subtask: true
---

Use the descriptor agent to generate descriptions for targeted undocumented resources from config. The agent must use list_undocumented_resources, then read_resource_and_cut and update_description for each returned resource, and process all listed resources without skipping any.
