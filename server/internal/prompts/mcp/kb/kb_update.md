Update KB when a conversation settles something. Projects take name, kind, status, goal, role, aliases, links, <machine>:<path> dirs, summary and timeline. Explain future write limits before selecting reference-only storage; change policy only at the owner’s request. Honor refusals. Chats, people, docs, meetings and events take importance, relevance, tags, note and projects. Recordings take tags, pinned and projects; topics take name, note, aliases and tags. Manual tags survive automation. remove_* prevents relinking. Merge duplicate projects with merge_into; undo with unmerge.

```json
{
  "type": "object",
  "required": ["kind", "id"],
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["projects", "chats", "people", "docs", "meetings", "events", "recordings", "topics"]
    },
    "id": {
      "type": "string",
      "description": "id, or a project's name (created when new)"
    },
    "merge_into": {
      "type": "string",
      "description": "Projects: merge into this slug/name. Send alone."
    },
    "unmerge": {
      "type": "boolean",
      "description": "Projects: undo this slug/name’s merge. Send alone."
    },
    "name": {
      "type": "string"
    },
    "project_kind": {
      "type": "string",
      "enum": ["short", "long"]
    },
    "status": {
      "type": "string",
      "enum": ["active", "paused", "done"]
    },
    "storage_policy": {
      "type": "string",
      "enum": ["managed", "reference_only"],
      "description": "Project storage for future writes."
    },
    "source_refs": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Projects: replace explicit source pointers; [] clears them."
    },
    "goal": {
      "type": "string"
    },
    "my_role": {
      "type": "string"
    },
    "note": {
      "type": "string"
    },
    "importance": {
      "type": "string",
      "enum": ["P0", "P1", "P2", "P3"]
    },
    "relevance": {
      "type": "integer"
    },
    "tags": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "summary": {
      "type": "string",
      "description": "Markdown, replaces summary.md"
    },
    "log": {
      "type": "string",
      "description": "one line appended to a project's timeline"
    },
    "aliases": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "remove_aliases": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "projects: aliases that are not this project's (another's name, too generic)"
    },
    "chats": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "remove_chats": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "people": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "remove_people": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "dirs": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "projects": {
      "type": "array",
      "items": {
        "type": "string"
      }
    }
  }
}
```
