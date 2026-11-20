package hub

import (
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// featureApprovalMirrors is the client capability for receiving display copies.
// A client that only knows the authoritative model keeps its pre-mirror counts.
const featureApprovalMirrors = "approval_mirrors"

func reservedDisplayOriginal(ix proto.Interaction) bool {
	return ix.MirrorOf == "" && strings.HasPrefix(ix.ID, proto.ApprovalMirrorIDPrefix)
}

// validDisplayMirror rejects orphaned, nested, private or same-conversation
// copies. A mirror is display state only and never substitutes for its origin.
func validDisplayMirror(origin, mirror proto.Interaction) bool {
	return mirror.ID != "" && mirror.ID == proto.ApprovalMirrorID(origin.ID) && mirror.MirrorOf == origin.ID && mirror.ID != origin.ID &&
		!reservedDisplayOriginal(origin) && origin.MirrorOf == "" && origin.MirrorMain && !mirror.MirrorMain && origin.Kind == proto.IxPermission && !origin.Private && !mirror.Private &&
		origin.Manager == mirror.Manager &&
		(origin.MachineID != mirror.MachineID || origin.SessionID != mirror.SessionID)
}

// refreshedDisplayMirror applies an original card's new deadline, text and
// choices to an existing copy without moving that copy out of its destination.
func refreshedDisplayMirror(origin, mirror proto.Interaction) proto.Interaction {
	out := origin
	out.ID = mirror.ID
	out.MirrorOf = mirror.MirrorOf
	out.MirrorMain = false
	out.MachineID = mirror.MachineID
	out.SessionID = mirror.SessionID
	out.Manager = mirror.Manager
	out.FoldedUnder, out.FoldedAs, out.FoldedSide = mirror.FoldedUnder, mirror.FoldedAs, mirror.FoldedSide
	out.Muted = mirror.Muted
	return out
}

// withNormalizedDisplayMirror makes the authoritative target part of the
// request before an operation receipt hashes its path, not just before relay.
func (h *Hub) withNormalizedDisplayMirror(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.normalizeDisplayMirror(r)
		next(w, r)
	}
}

// normalizeDisplayMirror rewrites an action on a display copy before any owner,
// scoped-token, extension, handoff or clarification gate examines its target.
// A stale or invalid copy is left untouched and therefore cannot reach the
// authoritative operation through this path.
func (h *Hub) normalizeDisplayMirror(r *http.Request) {
	id := r.PathValue("id")
	f := &h.reported
	f.mu.Lock()
	defer f.mu.Unlock()
	mirror, ok := f.interactions[id]
	if !ok || mirror.MirrorOf == "" {
		return
	}
	origin, ok := f.interactions[mirror.MirrorOf]
	if ok && validDisplayMirror(origin, mirror) {
		r.SetPathValue("id", origin.ID)
		prefix := "/api/v1/interactions/" + id
		if suffix := strings.TrimPrefix(r.URL.Path, prefix); suffix != r.URL.Path {
			r.URL.Path = "/api/v1/interactions/" + origin.ID + suffix
			r.URL.RawPath = ""
		}
	}
}
