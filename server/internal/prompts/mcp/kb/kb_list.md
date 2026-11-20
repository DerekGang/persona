List entities by importance (P0 first), relevance and recency. Optional tag. Projects (short: feature/launch, long: team mission) flag conclusions with newer evidence. Also lists chats, people, docs, meetings and events. Phone/watch recordings come newest first with minutes, day recap state and transcript expiry. Transcripts last recording_days (default 14), minutes/recaps minutes_days (default forever). Meetings, events and recordings give the start in the user’s time zone. Topics rank by size, organizing timeline/chat knowledge into concepts, decisions and reasons, practices, pitfalls, facts and open questions.

```json
{
  "type": "object",
  "required": ["kind"],
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["projects", "chats", "people", "docs", "meetings", "events", "recordings", "topics"]
    },
    "tag": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "description": "default 30"
    }
  }
}
```
