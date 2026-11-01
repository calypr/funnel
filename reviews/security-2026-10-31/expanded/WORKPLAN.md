# Expanded security investigation

## Investigation playbook

1. Route through the **how** skill. For motivation questions, also route through the **why** skill.
2. Throughput checkpoint stays one line: `throughput checkpoint: n/a, read-only investigation`.
3. Produce the `how`-shaped output (Overview / Key Concepts / How It Works / Where Things Live / Gotchas), or a recommendation with a tradeoffs table if the request is a decision between alternatives.
4. Apply the **unslop** skill to the reply.

## Coverage phases

1. Frame.
2. Fan out.
3. Aggregate.
4. Report.

## Scope and completion

Review unchanged commit 6c233af6e06e0a404dbc353310dab2c886de0b45. Complete distinct source passes over cloud/HPC job construction, event brokers and administrative clients, and storage publication/cancellation. Return a source coverage table, bounded local evidence for surviving observations, excluded hypotheses, and a rerun script. Preserve prior finding IDs. Verify the actual outputs in the foreground and have a fresh reviewer check the decision trail. No claim of exhaustive vulnerability discovery.

Production files are read-only. Checks use synthetic fixtures, fake clients or recording transports, and isolated temporary source snapshots. Do not retry previously blocked malformed-message, database-protocol, or data-disclosure tests. No network probes, live cloud access, containers, real credential reads, or production fixes. New findings require a violated policy and stated deployment prerequisites. Additional instances of existing root causes are extensions, not new counts.

The how exploration covers the actor, authority, data, and control that matters in each assigned subsystem. Its synthesis feeds the issue report. A separate product architecture design is skipped because no product code changes are requested. The no-comments cleanup workflow is skipped because deleting production comments is outside this read-only security scope. No commit or PR is requested.
