# Word lookup on iOS (#10977): review screenshots

Review evidence for the iOS client of the `text_lookup` extension point (branch `feat/10977-text-lookup-ios`).
Taken on an iPhone 17 Pro simulator (iOS 27.2, Xcode 27.2) against the mock hub with made-up data:

    PORT=18977 MOCK_TEXT_LOOKUP=1 python3 scripts/mock/mock_hub.py
    TEST_RUNNER_PERSONA_TEXT_LOOKUP_HUB=http://127.0.0.1:18977 TEST_RUNNER_PERSONA_SHOTS=<dir> \
      TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark xcodebuild test … -only-testing:PersonaUITests/TextLookupUITests

| File | What it shows |
| --- | --- |
| `text-lookup-chat-{light,dark}.png` | Persona's conversation: a tap on "Deploy" in a reply opens the card (word, pronunciation, two senses, Relingo, Save); the code block under it (`deploy --now`) never opens one |
| `text-lookup-chat-saved-{light,dark}.png` | the same card after Save: Saved, filled bookmark (the hub received `POST /api/v1/text-lookup/words`) |
| `text-lookup-agent-{light,dark}.png` | an Agent's chat: the card over a word of the Agent's reply |
| `text-lookup-inbox-{light,dark}.png` | an Inbox item: the card over a word of the item's AI summary |
| `text-lookup-unavailable-{light,dark}.png` | `available: false` (no extension): the same tap opens nothing and no lookup is sent; the page looks exactly as without the feature |

The app's interface is English (`-AppleLanguages (en)`); the mock's own conversations are in Chinese, the English
replies come from `/mock/say`. Per-surface behavior: `docs/apple/ios.md` "Word lookup (text_lookup)".
