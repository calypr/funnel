# Expanded review contract

The user wants as many real, production-relevant security issues as this repository review can establish. Scope is unchanged commit 6c233af6e06e0a404dbc353310dab2c886de0b45. Product source is read-only. Each worker has a distinct subsystem and temporary directory. Preserve prior S01-S19, D01, and P01-P10 identifiers.

Explain the assigned subsystem's actor, data, authority, and control decisions. Read existing report headings before investigating duplicates. Favor a small number of specific, consequential findings over raw counts.

Permitted checks use synthetic data, fake clients, recording transports, or ordinary schema/serialization tests. Network access, live databases/clouds/clusters, containers, actual credential reads, stress tests, process-crash tests, and operational exploit chains are outside this pass. Do not retry the previously blocked database-protocol, malformed-event-handler, or data-disclosure fixtures. A source concern requiring those stays unverified. Do not modify product files or existing audit artifacts. Save your own checks and evidence only in the assigned temporary directory.

For every candidate, state the intended policy, exact file/line, prerequisites, and whether it is a new root cause or an extension. A verified observation needs a command, actual output, and required output. Prefer behavior visible to callers or external clients. A fake client's response that ignores its own filters cannot prove a database authorization failure. Distinguish absent policy, intentionally permissive configuration, recovered request errors, and actual demonstrated effects. No guessed deployment exposure or fabricated CVEs.

Return PASS, ISSUES, or BLOCKED for the assigned coverage, plus components found, flow, files read, boundaries, non-obvious behavior, open questions, and excluded hypotheses. Save concise notes and rerunnable checks. The foreground verifies every retained output and writes the final inventory. Do not apply fixes.
