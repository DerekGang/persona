package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// What the memory assembler (memory_assemble.go) is told of the ledger's entries (#13532, docs/assistant/health.md
// "Memory by scene"). This file is the one place that reads an entry's space and categories, so wiring the memory
// ledger's next step (per-entry tagging, a space scope dimension) changes only it.

// memoryEntrySource lists ledger entries newest first (each page the next older one): the manager database, or a
// test's.
type memoryEntrySource interface {
	NewestAssistantEntries(context.Context, proto.AssistantEntriesParams, bool) (proto.AssistantEntriesResult, error)
}

// memoryEntrySrc is where the assembler reads entries: Service.memoryEntries when a test set one, else the manager
// database; nil when there is none.
func (s *Service) memoryEntrySrc() memoryEntrySource {
	if s.memoryEntries != nil {
		return s.memoryEntries
	}
	if s.store == nil {
		return nil
	}
	st, _ := s.store().(memoryEntrySource)
	return st
}

// entryTrackUnknown is the track of an entry whose track cannot be told (as track_kinds.go reads unknown information):
// no conversation is of it and the registry knows no such track, so the entry is in no conversation's own track and
// is not known.
const entryTrackUnknown = "-"

// memoryEntryUses is how usage sees each of entries (an infoUse), read for all of them at once:
//
//   - Its track (SourceTrack) is the one it was said in: the space of the conversation it names as its source, the
//     space dimension of its scope until the entry type has one. An entry that states a track (OriginSpace) other
//     than its conversation's is not known and takes the conversation's; one that states none (the main track, or a
//     lookup that failed when it was written) whose conversation cannot be told now has no track it is used in as its
//     own, and is not known. An entry naming no conversation keeps what it states ("" the main track).
//   - Its categories are the ones it carries (Category) and its track's default categories, which everything from
//     that track carries.
//   - An entry that carries none was not tagged itself (nothing tags entries yet): it takes the categories the
//     conversation it was said in was tagged with, whenever they are kept, and is known only when that conversation
//     was tagged and none of its turns waits. Otherwise it is not known, so it is used as usual in its own track and
//     taken for sensitive everywhere else.
//   - With no model set up for info.tag nothing new is tagged: an entry then carries its track's default categories
//     and those its conversation was tagged with before, as a conversation does (info_use.go), known when the track is
//     registered and the conversation's categories could be read.
//
// An entry naming a category the manager does not know, or a track the registry does not, is not known.
func (s *Service) memoryEntryUses(ctx context.Context, entries []proto.AssistantEntry) func(proto.AssistantEntry) infoUse {
	tagging := s.infoTagging(ctx)
	type floor struct {
		cats []string
		ok   bool
	}
	floors := map[string]floor{}
	floorOf := func(space string) floor {
		if f, ok := floors[space]; ok {
			return f
		}
		cats, ok := s.trackFloor(ctx, space)
		floors[space] = floor{cats, ok}
		return floors[space]
	}
	var sessions, untagged []string
	seen, seenUntagged := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		sid := e.Source.Session
		if sid == "" {
			continue
		}
		if !seen[sid] {
			seen[sid] = true
			sessions = append(sessions, sid)
		}
		if len(e.Category) == 0 && !seenUntagged[sid] {
			seenUntagged[sid] = true
			untagged = append(untagged, sid)
		}
	}
	var spaces map[string]spaceTold
	if len(sessions) > 0 {
		spaces = s.conversationSpaces(ctx, sessions)
	}
	var rows map[string][]db.InfoCategory
	readOK := true
	if st, durable := s.store().(infoCategoryStore); durable && len(untagged) > 0 {
		rctx, cancel := context.WithTimeout(ctx, infoCategoryStoreTimeout)
		var err error
		rows, err = st.InfoCategories(rctx, db.InfoCategoryConversation, untagged)
		cancel()
		if err != nil {
			s.logf("memory: entry categories: %v", err)
			readOK = false
		}
	}
	return func(e proto.AssistantEntry) infoUse {
		sid := e.Source.Session
		track, trackKnown := e.OriginSpace, true
		if sid != "" {
			told := spaces[sid]
			switch {
			case told.known && told.space != track:
				track, trackKnown = told.space, track == ""
			case !told.known && track == "":
				track, trackKnown = entryTrackUnknown, false
			}
		}
		cats, ok := normalCategories(e.Category)
		f := floorOf(track)
		known := ok && f.ok && trackKnown
		if len(cats) == 0 {
			tagged := false
			for _, r := range rows[sid] {
				tagged = true
				if !r.RemovedByUser {
					cats = append(cats, r.Category)
				}
			}
			if tagging {
				known = known && readOK && tagged && !s.infoTagWaiting.has(sid)
			} else {
				known = known && (sid == "" || readOK)
			}
		}
		cats, _ = normalCategories(append(cats, f.cats...))
		return infoUse{Categories: cats, Known: known, SourceTrack: track}
	}
}
