package assistant

// clientTodo is a to-do as GET /api/v1/todos and /api/v1/todos/{id} show it. A person_reply park's reply watch and
// the recorded reply observation hold raw source identity (chat, thread and message IDs, OpenIDs, the app); they are
// matching machinery kept server-side, never shown. An auto-created Feishu bot to-do also loses its contact link and
// opaque park instance: Agents → Bots already opens that conversation without exposing its topic. Everything is
// copied first, so the in-memory to-do keeps its evidence for exact reply correlation.
func clientTodo(t Todo) Todo {
	if t.Supervision == nil {
		return t
	}
	bot := validLarkPersonTaskID(t.ID)
	sv := *t.Supervision
	sv.Reviews = make([]TaskReview, len(t.Supervision.Reviews))
	copy(sv.Reviews, t.Supervision.Reviews)
	for i := range sv.Reviews {
		sv.Reviews[i].Reply = nil
		sv.Reviews[i].Evidence = clientParkEvidence(sv.Reviews[i].Evidence, bot)
	}
	if sv.Parked != nil {
		parked := *sv.Parked
		parked.Evidence = clientParkEvidence(parked.Evidence, bot)
		sv.Parked = &parked
	}
	t.Supervision = &sv
	return t
}

func clientParkEvidence(e *ParkEvidence, bot bool) *ParkEvidence {
	if e == nil || e.Reply == nil && !bot {
		return e
	}
	out := *e
	out.Reply = nil
	if bot {
		out.ParkInstance = ""
		if out.Contact != nil {
			contact := *out.Contact
			contact.URL = ""
			out.Contact = &contact
		}
	}
	return &out
}
