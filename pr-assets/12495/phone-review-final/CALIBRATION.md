# Native width calibration

All 111 primary pairs compare the iPhone at 402 pt with HarmonyOS at 402 vp. Both native screenshots contain 1206 × 2622 pixels. Every HarmonyOS frame has its own native accessibility hierarchy and a device acknowledgement of the active font scale and appearance. Each page also records its independently captured original on the standard Mate 80.
The 111 paired images use the repository’s synthetic review fixtures. Every iPhone image comes from a native build with the same Apple and Rust source trees as this pull request. Its image hash, accessibility hierarchy hash, XCTest case and test-log hash are in the manifest. The third column in the first three width comparisons uses an independent HarmonyOS virtual phone set to 402 vp at 3 physical pixels per vp. The center column is the standard Mate 80. The public images convert tagged source colours to sRGB before fitting each full viewport at its original aspect ratio.
The three comparisons cover the agent conversation, the repository and the worktree. Navigation, font metrics, platform input methods and operating-system controls are visible in the native captures. Reviewers can compare line wrapping, spacing and controls at matching viewport widths. The manifest records the interaction checks for each captured page.
In the compact Inbox, both native clients opened the Source/Time menu, retained the five waiting Agent requests as their own group and kept the selected grouping after a cold launch. The first request row matched the iPhone height in all three appearances. The manifest includes twelve original screenshots from each client, their native hierarchy digests and the measured controls.
Git commit and diff were compared from the native control trees and original pixels in all three appearances. The commit title begins at x=36, y=184 on both phones. The diff starts at y=138 in both readers and keeps the original 11-point code face at maximum interface text. The published measurements include the two file rows, hash-copy target and four sampled diff colors.
The Git overview was measured at the branch card, its branch and stash rows, and the changes header in all three appearances. The unpublished fixture branch kept pull-request creation disabled while the changed-file filter and stash list remained available.
On Machines, the first three overview cards and the first Agent in the MacBook detail were checked against their visible iPhone counterparts. In the six-hour history the drawable CPU plot was 131 points high at regular size and 171.3 at maximum text on both clients. The two simulators sampled their live metric values independently. The overview position check uses an 8-point vertical tolerance. light shows 4 iPhone and 3 HarmonyOS machines in the usage legend. The HarmonyOS NAS and server cards have vertical offsets of -20.97 and -21.63 logical points relative to iPhone. dark shows 4 iPhone and 3 HarmonyOS machines in the usage legend. The HarmonyOS NAS and server cards have vertical offsets of -20.97 and -21.63 logical points relative to iPhone. large shows 4 iPhone and 3 HarmonyOS machines in the usage legend. The HarmonyOS NAS and server cards have vertical offsets of -2.70 and -5.37 logical points relative to iPhone. The manifest identifies each measured card outside that tolerance and records both original legends.
The eight assistant Settings row icons were measured directly in all three appearances. Both native clients painted the symbols in RGB (0, 122, 255) in light mode and RGB (10, 132, 255) in dark mode. The manifest records the original images, control trees and painted bounds of all 24 pairs.
An independent iPhone build from upstream 67f3723f9ceed880613dd4abc28b3ea376711842 was also run on all 37 pages in the three appearances. Its per-frame evidence is recorded in the manifest.

| Screen | Native three-column comparison |
| --- | --- |
| Agent conversation | [Image](calibration/chat.png) |
| Repository | [Image](calibration/repository.png) |
| Worktree | [Image](calibration/worktree.png) |
| Worktree at largest text | [Image](calibration/worktree-large.png) |

## Worktree cards at the largest text size

The fourth comparison shows the iPhone at AX5, HarmonyOS before the fix at 3.2×, and the final HarmonyOS build at 3.2×. Both HarmonyOS captures use a 402 vp display at three physical pixels per vp. In the first two complete rows, the lower edges differed by 143, 74 pixels before the fix and by 0, 0 pixels afterward. The four status markers and model labels in these rows stay inside their cards. The model labels share a common row baseline. Both the iPhone and final HarmonyOS original images visibly display the complete first model name, `claude-sonnet-5-5`. The before capture uses source `56d86a699660a774c8671a9f65ab0c4d228615c7` and HAP `698442530b2ea3ffcfb1ffa06c555fdeb975696ff3ef02472b86e54b2a5e4772`. The after capture uses source `11ee8cec526a44cbab2a61c3f0216bdb82d03655` and HAP `03eff759ce894e36e301eaae86c7cc46da6c09ffe07f9b94b001b9c1e1cf9dfe`. The manifest records each native image, hierarchy and measured layout hash.

## Capture and platform details

The iOS reference is `870924d3be3207aa0f69bc9f8e66281abccca600`. The HarmonyOS gallery and 402-vp capture are `11ee8cec526a44cbab2a61c3f0216bdb82d03655`. The visual HAP SHA-256 is `03eff759ce894e36e301eaae86c7cc46da6c09ffe07f9b94b001b9c1e1cf9dfe`. Individual raw image and accessibility hierarchy hashes are in [the manifest](manifest.json).

Meeting reminders on HarmonyOS phones are subject to the operating system’s agent-reminder authorization and quota. Calendar access and notification permission are separate. On the isolated API 26 phone the app could create, read, move and delete its synthetic calendar occurrence. Its first agent reminder returned error 1700002, and the OS confirmed zero scheduled reminders. The app reports that limit in meeting settings. The tested source, OS-result hash and current implementation match are listed in the manifest. [Huawei agent reminder guide](https://developer.huawei.com/consumer/cn/doc/harmonyOS-guides/agent-powered-reminder), [Huawei reminder error codes](https://developer.huawei.com/consumer/cn/doc/harmonyos-references/errorcode-reminderagentmanager/).

The Persona source is distributed under the repository’s MIT License. This review package contains synthetic test scenes generated by Persona. [Persona license at the captured revision](https://github.com/nwverse/persona/blob/11ee8cec526a44cbab2a61c3f0216bdb82d03655/LICENSE).

## iPhone meeting transcription

The iPhone used the same app binaries as the screenshots. Its official streaming XCTest passed in 14.462 seconds. The test server received 796,800 bytes of synthetic audio. Live Hub lines appeared, an upstream failure switched the phone to local lines, and service recovery brought the Hub lines back. The simulator denied the app microphone access.

## Per-meeting transcription

A native test selected the configured Hub for one synthetic recording. Its microphone-free test package used 16 kHz PCM from the private test file. A sentence and five seconds of acknowledged audio arrived while the meeting was still recording. A controlled service failure switched the displayed source to local lines. Restoring the service returned it to Hub lines. The recording resumed at the acknowledged offset after a disconnect and the Hub confirmed a final transcript. A second stopped meeting repaired its sole 1–3 second gap over authenticated HTTPS. The unmetered-only setting sent zero audio on the restricted emulator route. The local engine recognized the same synthetic Mandarin sentence in three file passes and in the live input stream. A separate native call verified the echo-cancellation capability reported by the OS. The local test package declared zero permissions. For all five native cases the consent, room and audio production files are byte-for-byte identical to their gallery revisions. The manifest names the tested commit and the gallery commit independently.


## Opening a pending plan for review

On release `40adb0752b7f10a00bed4c7abdc7da9a3712d831`, tapping the expand button left the original card visible after the transition. An instrumented build from `f5e39b3d79243cde708b3c75851f1156f48e11a2` recorded that the tap was accepted. The content cover appeared 9 ms after the tap and was dismissed automatically after 643 ms. On this revision, the iPhone and HarmonyOS show 5 distinct pending decisions. HarmonyOS went from the first to the last and back, then folded and reopened the queue on the same HEIC card. The plan opens a native sheet titled “支持 HEIC”. It remained visible through 3.60 seconds of repeated native checks. The leading Close button and system Back each returned to the same pending card. Its terminal handoff menu opened and closed, with zero approvals or chat messages submitted. The manifest names the previous release, instrumented baseline, original fixed test package and reviewed package separately.

On both phones, View in Chat opened the original photo-sync Agent conversation on the Studio. Both native clients fetched the same transcript, and Back restored the first of five pending decisions with the HEIC card still selected.

In the Agent conversation both phones showed a Bash permission request, a JPEG/PNG format question, and the rendered HEIC plan in that order. The session-level field accepted a draft while all three requests remained pending. Dismissing the native keyboard preserved the text. Clearing it restored the original field. The apps submitted zero decisions or messages. The manifest records the original native screenshots and control trees.


## Composer and repository at matching width

The attachment and dictation buttons measured 48.3 pt on iPhone and 48.33 vp on HarmonyOS at normal type. At maximum type they measured 68 pt and 68 vp. Both controls sat 16 units from the horizontal edges. Their distance above the safe area was 10 vp at normal type and 10.333 vp at largest type on HarmonyOS, compared with 10 pt on iPhone. The original draft survived the system keyboard and full-page editor round trip. The iPhone reserved 34 pt below its content and HarmonyOS reserved 28 vp.

| Repository control | Normal iPhone, HarmonyOS | Largest iPhone, HarmonyOS |
| --- | --- | --- |
| Machine heading | 48 pt, 48 vp | 62 pt, 61.67 vp |
| Repository | 44 pt, 44 vp | 58 pt, 57.67 vp |
| Main checkout | 44 pt, 44 vp | 58 pt, 57.67 vp |
| Network test Agent | 118.7 pt, 118.67 vp | 172.3 pt, 172 vp |
| Release terminal | 46 pt, 46 vp | 58 pt, 57.67 vp |
| First Release Agent | 118.7 pt, 118.67 vp | 146.7 pt, 147 vp |
| Second Release Agent | 118.7 pt, 118.67 vp | 146.7 pt, 147 vp |

The main checkout's fold action covered 370 vp. Taps at both edges folded and reopened it, and the leading edge of an Agent row opened its conversation.

| Visible symbol in original pixels | Normal iPhone, HarmonyOS | Largest iPhone, HarmonyOS |
| --- | --- | --- |
| Attachment | 58 × 58, 58 × 59 | 82 × 82, 84 × 83 |
| Dictation | 43 × 61, 43 × 61 | 63 × 90, 64 × 91 |

## Draft wrapping and pending requests

The same unsent session note was entered on the two native phones. The original session retained all 18 lines of the longer draft after leaving for another session and returning. Both physical edges of the keyboard button dismissed the system input method.

| Draft and appearance | Keyboard | iPhone editor height (pt) | HarmonyOS editor height (vp) |
| --- | --- | ---: | ---: |
| Short / regular | Shown | 23 | 23 |
| Short / regular | Hidden | 23 | 23 |
| Short / largest | Shown | 126 | 126 |
| Short / largest | Hidden | 64 | 64 |
| 18 lines / largest | Shown | 295 | 295 |
| 18 lines / largest | Hidden | 245 | 245 |
| Full-page editor / Regular | Shown | 350.7 | 350.667 |
| Full-page editor / Largest | Shown | 317.3 | 317.333 |

The native pending-request viewport at the largest size measured 44 pt and 44 vp with the keyboard shown, then 389 pt and 389 vp after dismissal.

## Full-page draft controls

The native keyboard was hidden and reopened in the full-page editor in light, dark and largest text. Returning to the inline composer preserved all 18 lines and the editing focus.

| Control | Standard width (iPhone pt / HarmonyOS vp) | Largest width (iPhone pt / HarmonyOS vp) |
| --- | --- | --- |
| Editable text | 378 / 378 | 378 / 378 |
| Collapse | 44 / 44 | 57.7 / 57.6667 |
| Hide Keyboard | 44 / 44 | 57.7 / 57.6667 |
| Send | 48.3 / 48.3333 | 68 / 68 |

On HarmonyOS, opening the keyboard, dismissing it and returning retained the draft in landscape (874 × 402 vp) and split (402 × 413 vp) windows.

## Agent cards and queued draft

View in Chat opened the same photo-sync session on each phone. These paired native screenshots show its three pending requests and the unsent draft.

| State | Native comparison |
| --- | --- |
| Bash permission request | [Open image](supplemental/agent-permission.png) |
| JPEG and PNG choices | [Open image](supplemental/agent-format.png) |
| HEIC plan and steps | [Open image](supplemental/agent-plan.png) |
| Queued session draft with the keyboard | [Open image](supplemental/agent-draft.png) |
| Session input after clearing the draft | [Open image](supplemental/agent-cleared.png) |


## All Chats and Session Info

The All Chats menu and Assistant Profile changed the same Hub preference. Each phone showed the same five recent chats before and after the change. Each chat title opened information for its own machine and session. Returning from the weather workspace kept the original conversation and all seven workspace members.

The same All Chats entry on both phones offered 13 chats. All five disjoint source filters held 1, 6, 4, 1 and 1 chats. Light, dark and maximum text replayed the drawer, six filters, display options and opening the same chat.

On isolated synthetic Hubs, the native row menu and swipe archived their own conversations, pinning required confirmation, and Move To kept its selected machine and conversation. The app retained the row after capacity and permission refusals. The legacy Hub offered its supported actions.

iPhone shows a rounded swipe button above the Archive caption. HarmonyOS shows the icon and caption inside its swipe action.
The two native menu previews use their platform background and disclosure treatments.

- [All Chats filters, row actions and display options](supplemental/all-chats-options.png)
- [The same setting in Assistant Profile](supplemental/all-chats-profile.png)
- [PR #42 chat and its session information](supplemental/session-info-weather.png)
- [Photo-sync chat and its session information](supplemental/session-info-photo-sync.png)

## Agents grouping and search

The signed HarmonyOS app opened all eight grouping choices on a 402 vp phone in light, dark and maximum system text. Native view trees identify the selected list and its visible sections. Two iPhone XCTest runs exercised the eight choices in light and dark. Three separate iPhone captures verified the search field after a downward pull. Content pairs use the same physical scale, cropped around the first visible native section. Relative time labels follow each fixture run. The manifest records per-frame raw PNG and accessibility hashes, device configurations and source revisions.

| Native comparison | Image |
| --- | --- |
| All eight native grouping choices | [View](supplemental/agents-grouping-menu.png) |
| Repository, project, to-do and status | [View](supplemental/agents-groups-first.png) |
| Machine, app, recently used and pinned | [View](supplemental/agents-groups-second.png) |
| Agents search after a downward pull | [View](supplemental/agents-search.png) |

## Native controls and workflow comparisons

The comparison images use the native iPhone and HarmonyOS clients with synthetic tasks. The manifest records the read-only refusal and the grouping choices available on the older Hub.

The Persona decision dock was dragged shorter, tapped taller and returned to its default in all three appearances. At maximum system text, the full-text action opened the plan and returned to the same decision. The 20 interaction captures recorded zero approvals or messages.

| Appearance | HarmonyOS card top minus iPhone (vp/pt) | Primary button height difference | Secondary button height difference |
| --- | ---: | ---: | ---: |
| Light | +2.63 | +0.03 | +0.03 |
| Dark | +2.63 | +0.03 | +0.03 |
| Maximum text | -7.03 | +0.03 | +0.03 |

The recording facts editor kept the draft after an HTTP 409 from a concurrent edit. The client retried against the new revision and restored the original facts after clearing them.

[Edit the same to-do on its detail page](supplemental/editable-todo-details.png) · [Agents grouped by to-do and pinned direction](supplemental/agent-task-direction-groups.png) · [Recording detail and its editable confirmed facts](supplemental/recording-details-and-facts.png) · [Resize the Persona decision card and open the full plan](supplemental/persona-decision-dock.png)