---
description: Triage pending bugs by launching Bug Judge sub-agents for each node
---

Launch Bug Judge sub-agents to triage all pending bugs.

## Workflow

1. Call `bug_list` with state=pending to get all pending bugs
2. Group pending bugs by node_id
3. For each node that has pending bugs:
   a. Call `bug_list` for that node with state=dismissed (these are false positive examples)
   b. Launch a Bug Judge sub-agent with: the node ID, its pending bugs, and its dismissed bugs
   c. The Bug Judge tools: `bug_list`, `bug_acknowledge`, `bug_dismiss`, `bug_delete`, `read_function`, `read_struct`
4. Each Bug Judge must decide for each pending bug:
   - If similar to a dismissed bug -> delete (known false positive pattern)
   - If clearly a false positive -> dismiss
   - If a real bug -> acknowledge
5. Report decisions made and how many bugs were acknowledged/dismissed/deleted

