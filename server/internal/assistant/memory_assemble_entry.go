package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// What the memory assembler (memory_assemble.go) is told of the ledger's entries (#13532, docs/assistant/health.md
// "Memory by scene"). This file is the one place that reads an entry's space and categories, so wiring the memory
// ledger's next step (per-entry tagging, a space scope dimension) changes only it.

// memoryEntrySource lists ledger entries: the manager database (entriesStore), or a test's.
type memoryEntrySource interface {
	ListAssistantEntries(context.Context, proto.AssistantEntriesParams, bool) (proto.AssistantEntriesResult, error)
}

// memoryEntrySrc is where the assembler reads entries: Service.memoryEntries when a test set one, else the manager
// database; nil when there is none.
func (s *Service) memoryEntrySrc() memoryEntrySource {
	if s.memoryEntries != nil {
		return s.memoryEntries
	}
	if st := s.entriesDB(); st != nil {
		return st
	}
	return nil
}

// memoryEntryUses is how usage sees each of entries (an infoUse), read for all of them at once:
//
//   - Its track (SourceTrack) is the one it was said in (OriginSpace, "" the main track): the space dimension of its
//     scope until the entry type has one.
//   - Its categories are the ones it carries (Category) and its origin track's default categories, which everything
//     from that track carries.
//   - An entry that carries none was not tagged itself (nothing tags entries yet): it takes the categories of the
//     conversation it was said in, and is known only when that conversation was tagged and none of its turns waits.
//     Otherwise it is not known, so it is used as usual in its own track and taken for sensitive everywhere else.
//   - With no model set up for info.tag nothing is tagged: an entry then carries its origin track's default
//     categories, as a conversation does (info_use.go), known when the track is registered.
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
	var rows map[string][]db.InfoCategory
	readOK := true
	if tagging {
		var sids []string
		seen := map[string]bool{}
		for _, e := range entries {
			if sid := e.Source.Session; len(e.Category) == 0 && sid != "" && !seen[sid] {
				seen[sid] = true
				sids = append(sids, sid)
			}
		}
		if st, durable := s.store().(infoCategoryStore); durable && len(sids) > 0 {
			rctx, cancel := context.WithTimeout(ctx, infoCategoryStoreTimeout)
			var err error
			rows, err = st.InfoCategories(rctx, db.InfoCategoryConversation, sids)
			cancel()
			if err != nil {
				s.logf("memory: entry categories: %v", err)
				readOK = false
			}
		}
	}
	return func(e proto.AssistantEntry) infoUse {
		cats, ok := normalCategories(e.Category)
		f := floorOf(e.OriginSpace)
		known := ok && f.ok
		if tagging && len(cats) == 0 {
			sid, tagged := e.Source.Session, false
			for _, r := range rows[sid] {
				tagged = true
				if !r.RemovedByUser {
					cats = append(cats, r.Category)
				}
			}
			known = known && readOK && tagged && !s.infoTagWaiting.has(sid)
		}
		cats, _ = normalCategories(append(cats, f.cats...))
		return infoUse{Categories: cats, Known: known, SourceTrack: e.OriginSpace}
	}
}
