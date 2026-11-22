# Partner hubs: two independent hubs whose assistants talk (#13248)

Two hubs that stay **independent** (for example one at work that manages work computers, one at home that manages personal
ones, each with its own assistant, Inbox, knowledge base and state) can be paired as **partner hubs**. Their assistants then
send each other messages: the assistant on one hub writes to the other hub's main conversation and gets a reply back, as
`conversation_send` does between two conversations of one hub. Asking the other side to get something done ("have your
computers run the tests of X and tell me what failed") is an ordinary message: the other assistant plans and runs it with
its own tools, under its own rules, and answers with text.

This is not [linked hubs (mesh)](mesh.md). A mesh merges hubs into one group with one primary that holds all state and
drives every computer. Partner hubs never join a group, never hand over credentials or records, never forward App
requests or open node streams to the other's computers; each hub keeps answering only for itself. A hub that is a
linked-hubs member cannot pair, and two hubs linked in a mesh cannot also be partners.

Who it serves: people who run more than one hub. Everyday use is plain conversation ("ask the work hub whether the build
is green"); pairing and the per-hub switches live in Settings › This hub › Partner hubs (web) or `personad hub partner`.

```text
 Home hub                                                              Work hub
 ┌────────────────────────┐   partner link (WSS, pinned, one per pair, ┌────────────────────────┐
 │ hub: pairing, the link │◄═ dialed by whichever side can reach the ═►│ hub: pairing, the link │
 └───────────┬────────────┘    other; HTTP CONNECT / SOCKS5 proxy ok)  └───────────┬────────────┘
             │ bus: partner.send, partner.hubs, partner.receive, partner.changed   │ bus
 ┌───────────┴────────────┐                                            ┌───────────┴────────────┐
 │ manager: hub_send tool,│   envelope {message_id, kind, text, hop}   │ manager: dedupe by     │
 │ owner's card, scrub,   │ ─────────────────────────────────────────► │ message_id, deliver to │
 │ outbox, replies        │ ◄───────── reply / status envelopes ────── │ main as a request      │
 └────────────────────────┘                                            └────────────────────────┘
```

## Layers

1. **Hub to hub** (`server/internal/hub/partner.go` and the `partner_*.go` beside it): pairing, credentials, one
   long-lived link per pair, dialing with reconnects and proxies. The hub carries envelopes and stamps who they came
   from; it never reads or stores the text.
2. **Assistant to assistant** (`server/internal/assistant/partner.go` and the `partner_*.go` beside it): the `hub_list`
   and `hub_send` tools, the owner's confirmation, scrubbing, the durable outbox and inbox keyed by `message_id`,
   delivery into a conversation, replies and message states.
3. **No groups**: each pair is its own relationship, paired and unpaired by the two owners. With three hubs, each pair is
   paired separately, and nothing is relayed: a message goes to the hub it was sent to and no further (that hub's
   assistant may write to a third partner, but that is a new message of its own, under its own owner's confirmation).

## Pairing

- **Code**: made on the hub the other one can reach (Settings › This hub › Partner hubs › **Show my code**, or
  `personad hub partner code`), single use, 10 minutes:
  `persona://partner?hub=<hub_id>&name=<name>&fp=<public-key fingerprint>&tls=<TLS port>&url=<https address>…&code=jh_<64 hex digits>`
  (at most 6 `url`s). Only its hash is stored (`partner_codes`, expired ones pruned when a code is made). It has its own
  scheme and prefix, so pasting it into the linked-hubs page (or an older hub) fails instead of joining a group. Making
  one needs a TLS key and an https address (`public.tls_listen`, `public.advertise`): without them `409 no_tls`.
- **Pair**: on the other hub, Settings › This hub › Partner hubs › **Pair…** (or `personad hub partner add '<code>'`).
  The page shows the name and fingerprint read from the code before pairing. The pasting hub issues a credential for the
  code's hub, reaches it at the code's addresses pinned to the code's fingerprint (through the proxy when one is set,
  "Proxies") and posts `POST /api/v1/partner/pair` with `Authorization: Bearer jh_…`. In one transaction the code's hub
  uses the code up, revokes any older partner credential for that hub, issues a credential back and stores the pair. Both
  sides end up with the other's pin, addresses and a credential the other issued (sealed with `~/.persona/secret.key`);
  each keeps only the hash of the one it issued. The pasting hub checks that the answer comes from the code's hub_id
  (`502 wrong_hub`) and revokes its own credential again on any failure.
- **Only the hub that gets dialed needs TLS.** The pasting hub sends its own pin and https addresses when it has them.
  With both, the code's hub stores it with **Connect to this hub** on (`dial: auto`); without them, off (`dial: never`):
  it cannot dial it and waits for its link. A pin learned later from that hub's hello is kept, and the owner can switch
  dialing on once it can be reached. The pasting hub always stores the code's hub with dial on.
- Refused: your own code (`409 own_code`); a used, expired or unknown code (the code's hub answers 401, this hub
  `401 code_refused`); this hub is a linked-hubs member (`409 member_hub`), or the other hub is already its linked-hubs
  peer (`409 already_linked`); a known partner presenting another key (`409 pin_changed`); partner hubs switched off
  (`403 partners_off`). The other hub's own refusal comes back as a 409 with its code, from a fixed list ("Security
  boundaries"); anything else it answered is `502 refused`; no address worked, `502 unreachable`. "Routes" lists them.
- **Unpair** (either side, Settings or `personad hub partner remove <hub_id>`): tell the other hub over the link (when it
  is up), delete the pair, close the link (4012), stop dialing, revoke the credential this hub issued. The other hub does
  the same on receipt. A hub that was not linked then learns at its next dial: the partner answers 401, its page shows
  **Couldn't connect** ("refused"), and it waits 5 minutes between tries; one that never dials keeps the pairing until
  its owner unpairs there too. Revoking the partner's credential under Settings › Devices is the same as unpairing.

## The link

- `GET /api/v1/partner/link`: a WebSocket on the TLS port only (plain HTTP is `403 tls_required`, even on loopback),
  pinned to the fingerprint from pairing, `Authorization: Bearer <credential the dialed hub issued>`, permessage-deflate.
  The `partner` role opens only this path, the `partner_pair` role (a `jh_` code) only `POST /api/v1/partner/pair`;
  every other route answers them 403. A valid credential whose pairing this hub has not saved yet (the code's hub may
  dial right after redeeming the code, before the pasting hub stored the pair) is `404 not_paired`, which the dialer
  retries with its usual backoff; partner hubs off here is `403 partners_off`, a member `409 member_hub`.
- JSON-line RPC (`internal/rpc`) with its own dispatcher and three calls, nothing else (no records, no streams, no HTTP,
  no relay; another method is an error): `hello` (each side once, within 10 s: `{hub_id, name, version, proto: 1, fp,
  endpoints}`; a hub_id other than the pairing's closes the link 1008; the name, pin when none was known, and https
  addresses refresh what the other side shows and dials), `msg` (one envelope, answered with the receiving manager's
  result, "Bus") and `unpair`. Frames are at most 1 MiB.
- **One link per pair, whoever dials.** Each partner has a **Connect to this hub** switch (`dial`: `auto` or `never`).
  With both on, both dial and the link dialed by the smaller hub_id stays (the other closes with 4011); a newer link from
  the same dialer replaces the older one. With one side off, only the other dials, and the one link carries both
  directions: a hub nobody can reach (behind NAT, a private address) still receives messages over the link its partner
  dialed. Switching dialing off does not drop a link that is up; the dialer ends when it would dial again.
- Heartbeat every 10 s, the link drops after 5 s without a pong (`link.PingEvery`, `link.PingTimeout`; hub.json's `link`
  heartbeat does not apply). Reconnects back off from 1 s doubling to a minute, randomized, reset after a link that
  lasted a minute. A 401 or 403 answer, or a 4012 close, shows as refused and waits 5 minutes; a 409 (it joined a
  linked-hubs group) waits 5 minutes too.
- Close codes: 4011 duplicate (or paired again), 4012 unpaired (or a credential a new pairing replaced), 4013 the
  partner had more than 64 calls running here at once (it dials again), 1001 hub stopping.

## Proxies

Hubs dial each other directly, through a proxy, or both:

- `partners.proxy` in hub.json: `http://[user:pass@]host:port` or `https://…` (HTTP CONNECT, persona-link's code,
  `link.DialThrough`), `socks5://[user:pass@]host:port` or `socks5h://…` (default port 1080; the proxy resolves the name
  either way), or `off` (no proxy, not even the environment's). Read at the hub's start.
- Unset: the environment's `https_proxy`, `HTTPS_PROXY`, `all_proxy`, `ALL_PROXY`, the first one set (a SOCKS URL there is
  used too, unlike persona-link). `NO_PROXY` / `no_proxy` exempt addresses; loopback and `localhost` never use a proxy.
  An environment proxy that does not read is skipped. A hub run as a service rarely has these variables, so set
  `partners.proxy` there.
- Direct and proxied candidates race together, one every 300 ms: every address directly first, then each through the proxy
  (LAN addresses last of those); `partners.proxy_always: true` uses the proxy only and never dials direct. An explicit proxy that
  does not read fails every dial rather than falling back to direct, and the start log says so once.
- The tunnel is made under TLS: the partner's pinned TLS runs inside it end to end (`Transport.Proxy` stays nil), so the
  proxy can never stand in for the partner. Errors and logs never show the proxy's address or credentials; the start log
  names only its kind and whose setting it is.
- Company proxies often allow CONNECT only to port 443; put the dialed hub's TLS listener there, or have its port allowed.

## Messages

### Sending (`hub_send`)

- The `hub_list` and `hub_send` tools are listed only while at least one partner is paired (and the manager has a
  database to keep the messages), and only to the assistant's own conversations (the main one and the others of its
  space). The health space, guest conversations, a meeting's "Just me", outward readers and conversations on a work
  computer are refused; Agents never get them. `hub_list` names each partner (name, hub_id), whether it is online, and
  whether a message to it waits for the owner's OK.
- `hub_send {hub, text, reply_to?}`: `hub` is a partner's hub_id or a name only one partner has; `text` is what the
  assistant wrote, at most 12,000 characters. Only that text is sent: never transcripts, files, the knowledge base or
  anything else automatically. Before anything is stored or shown, credentials are scrubbed
  (`transcript.SanitizeShareText`: private keys, Bearer tokens, `name=value` secrets, known token formats, credentials in
  URLs; plus Persona's own `jd_`, `jp_`, `jx_`, `jb_`, `jc_`, `jh_` and `pext_` tokens); the tool says how many it
  removed. At most 20 messages and replies go to one partner in 10 minutes; past that the tool refuses.
- **The owner confirms on the phone by default.** The tool raises a permission card in the conversation ("Send this
  message to the partner hub "Work"?", a blank line, then the exact scrubbed text) and returns at once with the
  message_id: the conversation is never held while the card waits. Allow queues it; Deny ends it (`declined`) and the
  conversation is told; **Don't ask again for messages to "Work"** with Allow stops asking for that partner. A card left
  unanswered for 24 hours after the message was made, dismissed, or allowed on the owner's behalf (by the assistant or
  a supervising conversation) expires, and the conversation is told. Each partner has an **Ask me before sending**
  switch (on by default) in Settings; no tool can turn it off. A manager restart raises the waiting cards again.
- The tool's answer, the card and the message list keep **receipt, delivery and answer distinct**: `confirming` (waiting
  for your OK) → `queued` (waiting for the link) → `sent` (the partner's manager stored it) → `delivered` (it is in the
  partner's conversation) → `answered`; or `declined`, `expired`, `failed` with a code (`invalid`, `too_long`,
  `hop_limit`, `rate_limited`, `no_main`, `unpaired`, `undelivered`).
- Offline partner, or one whose manager is away: the message stays queued and goes out again with the same
  `message_id`, 5 s doubling to 5 minutes apart, and at once when the partner comes online. A message still queued
  after 7 days expires and the conversation is told. A partner the hub no longer knows (unpaired, or partner hubs off
  here) fails the message as `unpaired`.

### Receiving

- The receiving manager checks the envelope (refusals: `invalid`, `too_long`, `hop_limit`, `rate_limited`), stores each
  `(partner, message_id)` once (a resend is answered `duplicate` and changes nothing), answers `accepted` at once, and
  then delivers it. The partner's text is scrubbed here too.
- A **message** goes to the **main conversation** as it is at delivery, through the relay queue (it waits behind a
  running turn, like any relayed message; one queued for a main conversation put away since moves to the new one). With
  no main conversation it fails `no_main`, with the main one on a work computer `undelivered`.
- It arrives **labeled with the hub it came from** (message source kind `hub`, title = the partner's name, `hub_id`; the
  web shows "@Work · Request from another hub, not from you") and framed as a **request, never the owner's
  instruction** (`server/internal/prompts/assistant/partner_message.md`): the receiving assistant handles it under its own hub's rules,
  asks its own owner where it normally would (starting Agents that change things, sending things out, spending,
  deleting), and answers only with what is needed. It never carries a `client_msg_id`, the owner's quoted words, a
  hand-over or any of the hub's "this came from the owner" marks.
- **Replies** go back with `hub_send {hub, reply_to: <message_id>, text}`. A reply leaves the replying hub, so its owner
  is asked first too, unless they turned that off for the partner. It arrives in the conversation that sent the original
  message (or the main conversation if that one is gone), framed as information from another assistant
  (`partner_reply.md`); the original becomes `answered`, the incoming message `replied`.
- **Status envelopes** (`delivered`, or `failed` with a code) go back once a message or reply is in its conversation or
  cannot be; they carry no text and need no confirmation. A conversation that refuses a reply for good sends it on to
  the main conversation; anything else refused fails `undelivered`. An incoming envelope not delivered within 2 minutes
  is taken up again by the delivery loop, after checking the transcript for it.
- Limits: an exchange is at most 6 hops (message 1, reply 2, …; the hop of a reply is taken from this hub's own record,
  never the envelope); each manager takes at most 30 messages and replies and 120 statuses per partner in 10 minutes.

## Routes

Errors are JSON `{"error": <text in the request's language>, "code": …}` like the hub's other routes.

**The apps' (the hub's own; full credentials only, read-only devices get 403):**

| Method | Path | Body → answer |
| --- | --- | --- |
| GET | `/api/v1/partner-hubs` | → `{enabled, member, can_issue, hubs:[view]}`; `enabled`: partner hubs are on here; `can_issue`: this hub can show a code (a TLS key and an https address) |
| POST | `/api/v1/partner-hubs/code` | → `{code, expires_at}` (`Cache-Control: no-store`); `409 no_tls`, `409 member_hub`, `403 partners_off`, `503 no_database` |
| POST | `/api/v1/partner-hubs` | `{code}` → view, once the link is up (at most 5 s later); `400 bad_code`, `401 code_refused`, `403 partners_off`, `409 own_code` / `member_hub` / `already_linked` / `pin_changed`, a 409 with the other hub's code, `502 unreachable` / `wrong_hub` / `no_credentials` / `refused`, `503 no_database` |
| PATCH | `/api/v1/partner-hubs/{hub_id}` | `{dial: "auto" \| "never"}` → view; `404 not_found` |
| DELETE | `/api/v1/partner-hubs/{hub_id}` | → `{ok: true}`; `404 not_found` |

- A view is `{hub_id, name, fp, state, dial, since?, last_seen?, error?, dialed_by, rtt_ms?, created_at}`. `state`:
  `connected` (a link is up; `since`, `rtt_ms`, `dialed_by` `us` or `them`), `refused` (the partner refused this hub's
  credential), `connecting` (this hub dials it), `offline` (it waits for the partner to dial). `error` is why the last
  dial failed, each address's reason in turn, or that dialing is on but the hub has no pin or https address for it.
- `401 code_refused` is the other hub refusing the pasted code, not this request's credential: a client must not sign
  out on it (the web passes it to the dialog).
- Every change (a pairing made or ended, a link up or down, a dial setting, a dial's outcome) is pushed as
  `{"type":"partner_hubs"}` (no fields); a client that shows the list reloads it.

**Between hubs** (TLS only, `403 tls_required` otherwise):

- `POST /api/v1/partner/pair`, `Authorization: Bearer jh_…` (the code's secret), body at most 64 KiB:
  `{hub_id, name, version?, fp?, tls_port?, endpoints?, token}` (`token`: the credential the pasting hub issued, `jd_…`)
  → the same fields of the code's hub, with the credential it issued. A used, expired or unknown code is 401; a bad
  hub_id or token 400; `403 partners_off`; `409 member_hub` / `already_linked` / `pin_changed`; `503 no_database`.
- `GET /api/v1/partner/link`: "The link". A linked-hubs member answers both itself (`409 member_hub`), never forwarding
  them to its primary.

**The command line**: `personad hub partner code | add <code>|- | list | dial <hub_id> auto|never | remove <hub_id>`
calls the same five routes under `/api/v1/admin/cli/partners` (`GET`, `POST …/code`, `POST`, `PATCH …/{hub_id}`,
`DELETE …/{hub_id}`) on loopback with the hub's token, on the same mux as `personad hub peer`.

**The manager's** (`assistantwire` `partnerRoutes`, forwarded by the hub like the assistant's other routes; full role
only, `403 partner_read_only`; `503 partner_store` without a database). Clients send them with no `X-Persona-Manager`:
the default manager is the one that sends. No tool reaches them: a model cannot switch its own confirmation off.

| Method | Path | Body → answer |
| --- | --- | --- |
| GET | `/api/v1/partner-messages?hub=<hub_id>&limit=<n>` | → `{messages:[{id, direction: "out" \| "in", hub_id, hub_name, kind: "message" \| "reply", state, code?, text, title?, created_at, updated_at}]}`, newest first, statuses left out; `limit` 20 by default, at most 100; `400 partner_invalid` |
| GET | `/api/v1/partner-messages/prefs` | → `{prefs:[{hub_id, confirm}]}`: the stored settings only (no row: `confirm` is true) |
| PUT | `/api/v1/partner-messages/prefs/{hub_id}` | `{confirm: bool}` → `{hub_id, confirm}` |

## Bus

Types in `server/internal/assistantwire/partners.go`.

- **Capability** `partner_hubs` (`CapabilityPartnerHubs`) in `manager.hello`: the hub delivers partner envelopes and sends
  `partner.changed` only to a default manager that names it, and lists the `partner_hubs` feature only then.
- `partner.send {to, envelope}` → `{status, code?}` (manager → hub; only the default manager, another gets
  `other_manager`): hands one envelope to the paired hub over the live link and returns that hub's manager's answer.
  Errors: `invalid_params` (an envelope that does not read: `message_id` not `pm_` and 32 hex digits, an unknown kind, a
  bad `reply_to`, `state` or `code` over 32 bytes, text over 64 KiB), `partner_unknown` (not paired, or partner hubs off
  here: stop), `partner_offline` (no link now, or the call dropped or found no answer within 30 s: send it again, the
  partner deduplicates), `partner_unavailable` (the partner hub has no manager that takes it now: send it again).
- `partner.hubs {}` → `{enabled, hubs:[{hub_id, name, online, dial}]}` (manager → hub): the default manager's list;
  any other manager gets `{enabled: false, hubs: []}`.
- `partner.receive {from_hub, from_name, envelope}` → `{status, code?}` (hub → its default manager, 20 s): `from_hub`
  comes from the link's credential and `from_name` from this hub's pairing record, never from the envelope. The hub
  answers an envelope that does not read itself (`refused`, `invalid` or `too_long`); with no manager that takes it, or
  a call that fails, the partner gets `partner_unavailable`.
- `partner.changed` (hub → its default manager, one way, the shape of `partner.hubs`'s answer): right after the
  manager's hello and on every change. With partner hubs off, `enabled` is false and the list empty; the manager lists
  its tools only while it holds a list with `enabled` and a hub.
- **Envelope** `{message_id, kind: "message" | "reply" | "status", reply_to?, hop?, text?, state?: "delivered" | "failed",
  code?, sent_at}`; **result** `status` `accepted` | `duplicate` | `refused` with `code` `invalid`, `too_long`,
  `hop_limit`, `rate_limited` (or, in a failed status, `no_main`, `unpaired`, `undelivered`). Text is at most 12,000
  characters (`PartnerTextMax`), an exchange at most 6 hops (`PartnerHopMax`).

## Security boundaries

- **What a partner can do**: send envelopes over its link to this hub's default manager (messages to the main
  conversation, replies to a conversation that wrote to it, statuses about this hub's messages), within the manager's
  rates and limits; see this hub's id, name, version, pin and https addresses. **What it cannot**: open any App route
  (its credential opens the link alone, a code pairing alone), read anything (transcripts, tool calls, terminals, files,
  diffs, the knowledge base, the Inbox, mail, phone data, settings, credentials), answer cards, reach computers or node
  streams, join or see a group, or reach a third hub through this one.
- **A message is a request, never the owner's**: no owner provenance, no quote, no hand-over; the framing tells the
  receiving assistant to ask its own owner as always. What it can make happen is what that assistant would do for a
  stranger's request under its owner's rules.
- **The hub the data leaves decides**: its owner confirms each outgoing message by default and can stop confirming per
  partner; the receiving hub cannot approve the sender's card (its link reaches no App route). Limitation: the card goes
  by `proto.Resolution.UserAllowed()`, an allow nobody gave on the owner's behalf (not the assistant, not a supervising
  conversation). Like other permission cards, it does not check which device answered: an allow from an extension
  granted `agents.control`, or from a device using the hub's token, approves it too.
- **A partner's text is bounded and cleaned**: names and refusal reasons from the other hub lose control and
  bidirectional formatting characters (names at most 64 characters, reasons 300); envelope text is at most 64 KiB at the
  hub and 12,000 characters at the manager, keeps no control characters but line breaks and tabs, is scrubbed again, and
  goes into the conversation inside a quoted-message context block, with the partner's name inline only after its angle
  brackets and quote marks were replaced by look-alikes. Hub ids must match `^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`;
  addresses are cut to 8 https ones without `user:pass@`.
- **Refusal codes from a partner are mapped to known ones**: a pairing refusal is passed on only for `partners_off`,
  `member_hub`, `already_linked`, `pin_changed`, `tls_required` and `no_database` (anything else is `refused`); the
  manager keeps only the contract's codes (anything else is `invalid` for a refusal, `undelivered` for a failed status),
  so a partner's words never go into Persona's own notices.
- **Credentials**: codes single use, 10 minutes, hash only; issued credentials (device tokens of kind and role
  `partner`, `machine_id` the partner's hub_id) stored as hashes, received ones sealed; TLS pinned both ways, inside any
  proxy. Unpairing or revoking the credential ends the link at once.
- **A compromised partner** can send requests to the main conversation at a limited rate, which the receiving assistant
  treats as requests. It cannot read anything, answer cards, reach computers, or join a group.
- **Abuse limits**: 64 calls running at once per link (4013), 1 MiB frames, 30 messages and replies and 120 statuses per
  partner in 10 minutes at the manager; public authentication failures count as for any credential.

## Settings

- hub.json (read at start):

  ```json
  {"partners": {"enabled": true, "proxy": "", "proxy_always": false}}
  ```

  `enabled: false` is the emergency switch: no pairing, links refused (`403 partners_off`), no dialing, the tools gone;
  the pairings are kept, and messages still queued fail as `unpaired`.
- In Postgres: the pairs (`partner_hubs`, `partner_codes`, hub set) with each partner's **Connect to this hub** switch;
  the messages (`partner_messages`) and each partner's **Ask me before sending** switch (`partner_prefs`), manager set.

## Setting up two hubs where only one can reach the other

Example: the home hub can reach the work hub (through a VPN or a proxy), the work hub cannot reach the home hub.

1. On the **work hub** (the one that gets dialed): enable TLS and name an address the home hub can reach, in hub.json:
   `"public": {"tls_listen": ":18662", "advertise": ["https://<work hub address>:18662"]}`; restart the hub. Open the work
   hub's web app › Settings › This hub › Partner hubs › **Show my code**, and copy the code.
2. On the **home hub** (the one that dials): if it needs a proxy to reach the work network, set
   `"partners": {"proxy": "http://<proxy address>:<port>"}` (or `socks5://…`) and restart. Open its web app › Settings ›
   This hub › Partner hubs › **Pair…**, paste the code, check the name and fingerprint, and pair.
3. On the work hub the home hub now appears with **Connect to this hub** off when the home hub has no TLS address to
   give; if it gave one the work hub cannot reach, switch it off there. The link comes up from the home side and carries
   both directions.
4. Each side's **Ask me before sending** is on: the first message either way shows a card on that hub's owner's phone.

Headless hubs can do the same from a shell on the hub machine: `personad hub partner code` on the work hub,
`personad hub partner add '<code>'` on the home hub, `personad hub partner list`, `personad hub partner dial <hub_id> never`.

## Code

- **Contract**: `server/internal/assistantwire/partners.go` (methods, envelope, codes, limits, the manager's routes);
  `proto.SourceHub` and `MessageSource.HubID` in `server/internal/proto/item.go`.
- **Hub** (`server/internal/hub/`): `partner.go` (state, lifecycle, telling the manager and the apps), `partner_pair.go`
  (codes, pairing, unpairing), `partner_link.go` (the link and its dispatcher), `partner_dial.go` (dialing),
  `partner_proxy.go` (candidates, proxies, the pinned client), `partner_bus.go` (`partner.send`, `partner.hubs`,
  inbound `msg`), `partner_routes.go` (the apps' and the command line's routes), `partner_cli.go`
  (`personad hub partner`), `partner_store.go` (the optional store interface), `config_partners.go` (hub.json
  `partners`). Wiring: `devicetokens.go` (the `jh_` lookup, the two roles, revoking unpairs), `http.go`, `peer_http.go`
  (a member answers the two hub-to-hub paths itself), `bus.go`, `bus_broadcast.go`, `extensions_link.go` (the feature),
  `shutdown.go`, `peer_admin.go`; `link.DialThrough` in `server/internal/link/proxy.go`.
- **Manager** (`server/internal/assistant/`): `partner.go` (states, limits, the store and bus interfaces, the partner
  list), `partner_tools.go` (`hub_list`, `hub_send`, the card), `partner_receive.go` (`partner.receive`, delivery,
  statuses), `partner_loop.go` (the delivery loop), `partner_routes.go`; small hooks in `bus.go` (the capability),
  `tools.go`, `conversations.go`, `relay_once.go`, `relay_refusal.go`, `fleet.go`, `task_brief.go`, `schedule.go`,
  `service.go`.
- **Prompts**: `server/internal/prompts/assistant/partner_message.md`, `partner_reply.md`, `partner_notice.md`
  (rendered in `server/internal/prompts/assistant_partner.go`); tool descriptions
  `server/internal/prompts/mcp/assistant/hub_list.md` and `hub_send.md`.
- **Database**: `server/internal/db/partners.go` with the hub-set migration
  `server/internal/db/migrations/hub/20261122193642_partner_hubs.sql` (`partner_hubs`, `partner_codes`);
  `server/internal/db/partner_messages.go` with the manager-set migration
  `server/internal/db/migrations/manager/20261122194225_partner_messages.sql` (`partner_messages`, `partner_prefs`).
- **Strings**: `hub-partner_pair`, `hub-partner_dial`, `hub-partner_routes`, `hub-partner_cli`, `assistant-partners` and
  `assistant-tool_titles` in `server/internal/i18n/locales/`; `settings-partnerhubs` in `web/src/i18n/locales/`.
- **Web**: `web/src/lib/partner-hubs.ts`, `web/src/views/settings/PartnerHubs.tsx` (`docs/web/web.md`); the `hub` source
  in `web/src/lib/message-source.ts` and `web/src/views/chat/SourceMessage.tsx`; the card's detail in
  `web/src/lib/tool-detail.ts`. Mock: `scripts/mock/partner_hubs_mock.py` (`MOCK_PARTNER_HUBS`).

## Verification

- **Hub, in process** (no database: the test store `meshStore` keeps partners in memory; TLS hubs on 127.0.0.1 from
  `server/internal/hub/partner_harness_test.go`, scripted managers on each bus):
  - `TestPartnerPairAndLink`, `TestPartnerPairingCode`, `TestPartnerPairingRefusals`, `TestPartnerPairBounds`,
    `TestPartnerName`, `TestPartnerAnswered` (`partner_test.go`): pairing both ways, the code's rules, every refusal,
    bounds on what the other hub sends, cleaned names, refusal codes passed on only from the list.
  - `TestPartnerOneSideDialsAndReconnects`, `TestPartnerDuplicateLink`, `TestPartnerUnpair`, `TestPartnerDialSetting`,
    `TestPartnerLinksStopWithTheHub`, `TestPartnerDialRefusalsSaid` (`partner_link_test.go`).
  - `TestPartnerEnvelopeRoundTrip`, `TestPartnerSendWhenItCannotGo`, `TestPartnerTooManyCalls` (`partner_bus_test.go`).
  - `TestPartnerThroughConnectProxy`, `TestPartnerThroughSocksProxy` (credentials, an impostor behind the proxy),
    `TestPartnerBadProxyNeverGoesDirect`, `TestPartnerNoProxy`, `TestPartnerCandidates`, `TestParsePartnerProxy`
    (`partner_proxy_test.go`); the hub's `TestMain` clears the proxy variables.
  - `TestPartnerCommands`, `TestPartnerArguments` (`partner_cli_test.go`).
  - Run: `cd server && go test -count=1 -race -run 'TestPartner|TestParsePartnerProxy' ./internal/hub/`.
- **Database** (Postgres, `PERSONA_TEST_DATABASE_URL`): `TestPartnerHubsAndCodes` (`server/internal/db/partners_test.go`);
  `TestPartnerMessagesInsertOnce`, `TestPartnerMessagesCompareAndSet`, `TestPartnerMessagesClaim`,
  `TestPartnerMessagesListsCountsAndPrefs` (`server/internal/db/partner_messages_test.go`). Run:
  `go test -count=1 -run 'TestPartner' ./internal/db/`.
- **Manager** (`server/internal/assistant/partner_test.go`, a fake hub, Postgres): `TestGoldenPartnerPrompts`,
  `TestPartnerToolsOnlyWhilePaired`, `TestHubSendAsksAndSendsOnAllow`, `TestHubSendDeclinedOrExpiredIsTold`,
  `TestHubSendAlwaysStopsAsking`, `TestHubSendScrubs`, `TestHubSendReplyToAndHopLimit`, `TestHubSendRefusesHealthAndGuests`,
  `TestHubSendRefusesOutwardJustMeAndWork`, `TestPartnerAnswerOnlyTheOwnersYes`, `TestPartnerReceiveDeliversOnceIntoMain`,
  `TestPartnerReceiveRefusals`, `TestPartnerReplyGoesToTheAskingConversation`, `TestPartnerStatusUpdatesTheMessage`,
  `TestPartnerOfflineThenOnline`, `TestPartnerRestartAsksAgain`, `TestPartnerQueueExpires`,
  `TestPartnerReconcilerDeliversWhatWasLeft`, `TestPartnerMessageFollowsTheMainConversation`,
  `TestPartnerCodesAreTheContractsOnly`, `TestPartnerInlineKeepsTheNameInItsQuotes`, `TestPartnerRoutes`,
  `TestManagerCapabilitiesFit`. Run: `go test -count=1 -race -run 'Partner|HubSend|TestManagerCapabilitiesFit' ./internal/assistant/`.
- **Two hubs end to end** (`server/internal/hub/partner_e2e_test.go`: real managers on two in-process hubs, each with
  its delivery loop, a node for its main conversation and an app answering its cards; their partner messages kept in
  memory by `server/internal/hub/partner_e2e_store_test.go`): `TestPartnerE2E` (pairing lists the tools; a message waits
  for the owner's OK, scrubbed on the card, goes once allowed and arrives in the partner's main conversation as another
  hub's request; its status comes back; the answer, sent without a card, arrives in the conversation that asked; a
  message written while the link is down goes once when it is back; a resend is a duplicate; a declined card sends
  nothing and says so; unpairing takes the tools away) and `TestPartnerE2EThroughProxy` (a hub that reaches its partner
  only through an HTTP CONNECT proxy, `proxy_always`). Run: `go test -count=1 -race -run 'TestPartnerE2E' ./internal/hub/`.
- **Web** (vitest, zh-Hans): `web/src/lib/partner-hubs.test.ts`, `web/src/views/settings/PartnerHubs.test.tsx`,
  `web/src/lib/message-source.test.ts`, `web/src/views/chat/SourceMessage.test.tsx`,
  `web/src/views/chat/PermissionBody.hub.test.tsx`, `web/src/lib/tool-detail.test.ts`, `web/src/lib/turns.test.ts`. Run:
  `cd web && npx vitest run src/lib/partner-hubs.test.ts src/views/settings/PartnerHubs.test.tsx`. In a browser, against
  the mock hub with `MOCK_PARTNER_HUBS=1`: `node web/e2e/partner-hubs.mjs <web> --hub <mock> --shots <dir>` and
  `web/e2e/message-sources.mjs` (`web/e2e/README.md`); the mock's own test is `scripts/mock/test_partner_hubs_mock.py`.

## Not implemented / follow-ups

- **iOS and Android settings**: no Partner hubs page yet (pair, switches, unpair); use the web app or the command line.
  Messages from a partner already show on iOS and Android as a source message labeled with the hub's name.
- **Binding approvals to the owner's own device**: the card accepts any answer `UserAllowed()` accepts ("Security
  boundaries"). Requiring the owner's own device (carrying it through the card's answer as the secret hand-off card
  does) would also stop approvals from the watch and from Lark: a product decision.
- **Early stop of a candidate race**: a 401 or a pin mismatch on one address does not stop the others; the dial waits
  for every candidate (as the mesh does), and its error lists each.
- **No relay between three hubs**: each pair is separate, and a hub never passes a partner's message on.
- The manager's message list (`GET /api/v1/partner-messages`) has no screen yet. The delivery loop sends one envelope
  after another, so a partner whose calls time out delays the others in that pass.
