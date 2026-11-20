#!/usr/bin/env python3
"""Synthetic project coverage on the standard mock Hub, for KBProjectEvidenceUITests."""
import copy
import importlib.util
import json
import os
import threading
from pathlib import Path
from urllib.parse import urlsplit

os.environ.setdefault("PORT", "19465")
root = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("project_evidence_mock", root / "scripts/mock/mock_hub.py")
mock = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mock)

PROJECT = "sample-delivery"
OLDER = "sample-archive"
CHAT = "sample-review"
POINTER = "https://example.org/releases/sample"
LIST = "/api/v1/kb/projects"
UPDATED = "2026-11-19T15:08:12.123456789Z"
lock = threading.RLock()
states = {}
writes = []


def initial():
    return {"policy": "managed", "refs": [POINTER]}


def entry(project, state, detail=False):
    old = project == OLDER
    item = {"kind": "projects", "id": project, "name": "Archive index" if old else "Sample delivery",
            "one_line": "All items delivered.", "importance": "P1", "relevance": 78,
            "updated": UPDATED, "extra": "short · active"}
    if old:
        return item
    source = "chats/" + CHAT
    knowledge = {"policy": state["policy"], "position": 3, "needs_review": True, "fields": {
        "summary": {"status": "needs_review", "cutoff": 1, "uncovered": 2, "locked": True,
                    "maintainer": "user", "source_refs": ["projects/" + PROJECT + "/summary"]},
        "one_line": {"status": "current", "cutoff": 3, "writer": "pipeline",
                     "maintainer": "pipeline", "source_refs": [source]}}}
    if state["policy"] == "reference_only":
        for field in knowledge["fields"].values():
            field["maintainer"] = "external"
    if detail:
        knowledge["sources"] = [{"ref": ref} for ref in state["refs"]]
        knowledge["sources"].append({"ref": source, "state": "merged", "state_at": UPDATED})
    item["project_knowledge"] = knowledge
    return item


class Handler(mock.H):
    def do_GET(self):
        path = urlsplit(self.path).path
        owner = self.headers.get("X-Persona-Manager", "")
        with lock:
            if path == "/mock/project-evidence/reset":
                states.clear()
                writes.clear()
                return self.send_json({"fixture": PROJECT})
            if path == "/mock/project-evidence/writes":
                return self.send_json({"writes": copy.deepcopy(writes)})
            state = states.setdefault(owner, initial())
            if path == LIST:
                return self.send_json({"items": [entry(PROJECT, state), entry(OLDER, state)]})
            if path in [LIST + "/" + PROJECT, LIST + "/" + OLDER]:
                item = entry(path.rsplit("/", 1)[1], state, detail=True)
                meta = {"name": item["name"], "one_line": item["one_line"], "kind": "short", "status": "active",
                        "source_refs": state["refs"], "pinned": "Keep the original checklist."}
                detail = {"kind": "projects", "id": item["id"], "path": "projects/" + item["id"], "meta": meta,
                          "summary": "Two items pending.", "log": ["- 2026-11-19 15:08 · Release review · All items delivered."],
                          "links": [{"kind": "chats", "id": CHAT, "name": "Release review", "relevance": 60}]}
                if "project_knowledge" in item:
                    detail["project_knowledge"] = item["project_knowledge"]
                return self.send_json({"detail": detail})
            if path == "/api/v1/kb/chats/" + CHAT:
                return self.send_json({"detail": {"kind": "chats", "id": CHAT, "path": "chats/" + CHAT,
                                                  "meta": {"name": "Release review"}, "summary": "Source delivery history."}})
        return super().do_GET()

    def do_PUT(self):
        if urlsplit(self.path).path != LIST + "/" + PROJECT:
            return super().do_PUT()
        body = json.loads(self.body())
        owner = self.headers.get("X-Persona-Manager", "")
        with lock:
            if set(body) - {"storage_policy", "source_refs"}:
                return self.send_json({"error": "This fixture only accepts project storage updates."}, 400)
            if any(" " in ref for ref in body.get("source_refs", [])):
                return self.send_json({"error": "Invalid source reference"}, 400)
            state = states.setdefault(owner, initial())
            if "storage_policy" in body:
                state["policy"] = body["storage_policy"]
            if "source_refs" in body:
                state["refs"] = body["source_refs"]
            writes.append({"manager": owner, "body": body})
            return self.send_json({"ok": True})


if __name__ == "__main__":
    with mock.ThreadingHTTPServer(("127.0.0.1", mock.PORT), Handler) as server:
        server.daemon_threads = True
        mock.ENDPOINTS.append(mock.endpoint("127.0.0.1", mock.PORT, "http", "lan"))
        server.serve_forever()
