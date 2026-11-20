"""The web Starred page's loopback fixture. Separate assistants get separate collections."""
import os
import threading
import uuid


BASE = "/api/v1/assistant/starred"
FIELDS = ("machine_id", "session_id", "item_id", "conversation")


def register(d):
    hub = d.hub
    d.help("MOCK_STARRED=1|old  Enable starred-snapshot routes (1) or simulate an older assistant (old)")
    mode = os.environ.get("MOCK_STARRED", "old")
    if mode == "1":
        d.feature("starred_messages")
    books = {}
    lock = threading.Lock()

    def route(h, req):
        if mode != "1":
            h.send_json({"error": "Not found"}, 404)
            return True
        manager = hub.MANAGERS["default"]
        other = hub.other_manager(h)
        if other is not None:
            manager, problem = other
            if problem is not None:
                h.send_json(*problem)
                return True
        changed = False
        with lock:
            stars = books.setdefault(manager, [])
            if req.method == "GET":
                selected = stars[:]
                if session := (req.query.get("session") or [""])[0]:
                    selected = [s for s in selected if f"{s['machine_id']}/{s['session_id']}" == session]
                if req.query.get("index") == ["1"]:
                    body = {"refs": [{k: s[k] for k in (*FIELDS, "id")} for s in selected], "can_write": True}
                else:
                    body = {"stars": selected, "can_write": True}
            elif req.method == "POST":
                item = req.json()
                key = tuple(item.get(k) for k in FIELDS)
                if not all(key) or not str(item.get("text", "")).strip():
                    h.send_json({"error": "A star needs a message"}, 400)
                    return True
                star = next((s for s in stars if tuple(s[k] for k in FIELDS) == key), None)
                if star is None:
                    star = dict(item, id="sm" + uuid.uuid4().hex, starred_at=hub.ago())
                    stars.insert(0, star)
                    changed = True
                body = {"star": star}
            else:
                identifier = req.match.group(1)
                removed = next((star for star in stars if star["id"] == identifier), None)
                if removed is not None:
                    stars.remove(removed)
                    changed = True
                body = {"removed": removed}
        if changed:
            event = {"type": "assistant_starred"}
            if manager != "assistant":
                event["manager"] = manager
            hub.broadcast(event)
        h.send_json(body)
        return True

    d.route(("GET", "POST"), BASE, route)
    d.route(("DELETE",), BASE + r"/([^/]+)", route)
