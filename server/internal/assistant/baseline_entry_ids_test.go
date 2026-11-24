package assistant

import "github.com/nwverse/persona/server/internal/proto"

func entryIDList(entries []proto.AssistantEntry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}
