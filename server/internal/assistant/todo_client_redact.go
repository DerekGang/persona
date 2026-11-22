package assistant

import "encoding/json"

// MarshalJSON writes supervision as every client sees a to-do (GET /api/v1/todos, /todos/{id}, seen, thread, marks,
// route and the pushes). A person_reply park's reply watch and the recorded reply observation hold raw source identity
// (chat, thread and message IDs, OpenIDs, the app); they are matching machinery kept server-side, never shown. A bot
// conversation's park (one with an opaque park instance, ParkLarkPersonTask) also loses its contact link and that
// instance: Agents → Bots opens the conversation without exposing its topic. Other person_reply parks keep their
// owner-visible link (#11497).
//
// Storage never marshals a whole Supervision: the ledger row marshals its Reviews, AutoReview and Waits apart
// (Supervision.row), so the stored evidence keeps everything exact reply correlation needs.
func (v Supervision) MarshalJSON() ([]byte, error) {
	type plain Supervision
	return json.Marshal(plain(clientSupervision(v)))
}

func clientSupervision(v Supervision) Supervision {
	reviews := make([]TaskReview, len(v.Reviews))
	copy(reviews, v.Reviews)
	for i := range reviews {
		reviews[i].Reply = nil
		reviews[i].Evidence = clientParkEvidence(reviews[i].Evidence)
	}
	v.Reviews = reviews
	if v.Parked != nil {
		parked := *v.Parked
		parked.Evidence = clientParkEvidence(parked.Evidence)
		v.Parked = &parked
	}
	return v
}

func clientParkEvidence(e *ParkEvidence) *ParkEvidence {
	if e == nil || e.Reply == nil && e.ParkInstance == "" {
		return e
	}
	out := *e
	out.Reply = nil
	if out.ParkInstance != "" {
		out.ParkInstance = ""
		if out.Contact != nil {
			contact := *out.Contact
			contact.URL = ""
			out.Contact = &contact
		}
	}
	return &out
}
