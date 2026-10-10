# Blind authoring

`kazu.yaml` and `checks/shop.py` in this example were written **blind**: by an author who was given only `BRIEF.md` (the shop's design, its business rules, its telemetry, and what Kazu can do) and had never seen the bug flags, the traps or the matrix. `RESPONSE.md` is that author's answer, unedited.

The rules (SPEC.md, "Ground rules"):

- Checks and scenarios come only from this process. Nobody who knows the bug list edits them to catch a bug.
- Edits after the fact are allowed in exactly two cases, and each is logged in `EDITS.md`: correcting a fact the brief left ambiguous, and following a diagnostic Kazu itself printed (`kazu doctor`, config validation), as a real user would.
- If the checks ever need a rewrite, a new blind author does it from an updated brief.
