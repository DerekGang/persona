# Assistant entry fixtures (five peer task groups)

Web, Android and Apple replay these synthetic wire inputs through their pure entry functions. The composer entries
are Needs you, In progress, Awaiting acceptance, Stopped unfinished, and Parked, in that order. The authoritative
contract is "Needs you and In progress: each thing once" in `docs/assistant/unified-entry.md`.

- Owner questions / decisions win. Actual execution (starting, working, commands, subagents or workflows) is In progress.
- A stopped `done` closeout waits for supervisor acceptance; an unread reply alone never proves completion.
- Stopped `not_done`, `blocked` and missing closeouts are unfinished, whether the reply has been read or not.
- More than one hour without a real supervisor review or confirmed send escalates unfinished work to Needs you.
  Exactly one hour does not. Automatic ledger writes and reviewer-only records do not reset the clock.
- `work.turn_at` is stable across read receipts and hub restarts. Older hubs fall back to session state / update / link
  times. `supervision.followed_up_at` and actual supervisor review records bound the latest follow-up.
- Explicit parks and existing fresh matching ready-PR waits are separate from unfinished work. A generic stopped
  `blocked/external` never overrides an explicit park. An unparked external wait stays unfinished, for the supervisor
  to confirm and park with real evidence. Live work and owner decisions still win.
- Accepted / closed work never returns to stopped or awaiting acceptance. Existing finished / shelved history is
  separate and uncounted; stale unaccepted work cannot disappear there.
- Each task has one home and one count. Shared Agents' requests belong to their tied task, else the first lister;
  other tasks do not repeat them or pretend to be running. Manual Agents appear only in the main conversation.
- Plans waiting for Start and messages waiting for Send remain one Needs you row each.

Structure: `{"version": 1, "cases": [...]}`. `input` contains `conversation`, `main`, RFC3339 `now`, and wire `todos`,
`sessions`, pending `interactions`, `plans` by id, and `feed`. `expected` lists ordered row keys in `needs_you`,
`running`, `review`, `stopped`, `parked`, and the uncounted `shelved`. Missing optional groups mean empty. Row keys are
`task:<id>`, `agent:<machine>/<session>`, `plan:<id>`, or `message:<feed item id>`. Tasks use current rank then newest
card / creation time; real questions use oldest first. A plan is one row whatever its task count. The older case names
are stable identifiers, not specifications of their current expected group.

`expected.activity` optionally checks background task / subagent / workflow counts, phase and progress; null means
no actual background work. A stopped external wait must retain no fake spinner or background activity.

Not covered: the conversation's own pending cards, local Later state, Android inbox drafts, and platform-specific
subtask folding. All data is made up.

Run `cd web && npx vitest run src/lib/assistant-entries.test.ts`,
`cd android && ./gradlew :core:domain:test --tests '*AssistantEntriesFixtureTest*'`, and
`AssistantEntriesFixtureTests` in Apple's `PersonaTests`.

Native UI replay: start `scripts/mock/mock_hub.py` on an owned loopback port with `MOCK_TASKS=1`,
`MOCK_TASKS_NO_ASKS=1`, and `MOCK_TASK_CLOSEOUT_GROUPS=1`. Apple `TaskCloseoutGroupsUITests` uses
`PERSONA_MOCK_HUB` and `PERSONA_CLOSEOUT_GROUPS=1`; it checks separate groups and that long waiting targets do not
push task rows outside the phone. Android `AssistantTraysShotsTest.fivePeerGroupsUseCloseoutsAndPreserveExplicitExternalParks`
checks the real composable, counts, closeout wording, explicit parks, and no accidental acceptance writes.
