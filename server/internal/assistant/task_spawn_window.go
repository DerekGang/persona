package assistant

import "sync"

// spawnStarts keeps a task_spawn child from passing for a first-layer Agent before it is recorded as spawned
// (#11999). The node sends a new session its first prompt before session.start answers, and the manager handles
// other RPCs meanwhile, so the child can list and call tools while its parent's start is still in flight. A workspace
// linked to the to-do can link it in that time (autoLinkTodos, source space); its own id is not known until the start
// answers, and no runtime marks it beforehand. So from just before a start until its child is linked as spawned, any
// session newly linked on that computer may be the child: it does not see task_spawn and cannot call it. When the
// child is linked, the others are ordinary Agents again and discovery is woken to list the tool for them. When the
// start fails or its result is unknown, a child may run unlinked to its parent: whatever was newly linked meanwhile
// keeps no task_spawn for the rest of this process.
type spawnStarts struct {
	mu      sync.Mutex
	pending map[*spawnStart]struct{}
	// suspect: sessions linked while a start that did not link its child was in flight, by machine and session.
	suspect map[[2]string]bool
}

// spawnStart is one task_spawn start in flight.
type spawnStart struct {
	machine string
	// linkedBefore: the sessions of machine that had an active to-do link when the start began. The child is not
	// among them; it did not exist yet.
	linkedBefore map[string]bool
}

// beginSpawnStart records a task_spawn start on machine before anything reaches the node.
func (s *Service) beginSpawnStart(machine string) *spawnStart {
	p := &spawnStart{machine: machine, linkedBefore: s.linkedSessionsOn(machine)}
	s.spawns.mu.Lock()
	if s.spawns.pending == nil {
		s.spawns.pending = map[*spawnStart]struct{}{}
	}
	s.spawns.pending[p] = struct{}{}
	s.spawns.mu.Unlock()
	return p
}

// endSpawnStart ends p. linked: its child is now linked to the to-do as spawned. Otherwise every session newly linked
// on the machine since p began stays suspect.
func (s *Service) endSpawnStart(p *spawnStart, linked bool) {
	var suspects [][2]string
	if !linked {
		for id := range s.linkedSessionsOn(p.machine) {
			if !p.linkedBefore[id] {
				suspects = append(suspects, [2]string{p.machine, id})
			}
		}
	}
	s.spawns.mu.Lock()
	delete(s.spawns.pending, p)
	if len(suspects) > 0 && s.spawns.suspect == nil {
		s.spawns.suspect = map[[2]string]bool{}
	}
	for _, key := range suspects {
		s.spawns.suspect[key] = true
	}
	s.spawns.mu.Unlock()
	s.mcpChanges.notify() // Agents hidden from task_spawn while p was in flight list it again
}

// spawnAncestryUnknown reports whether machine/session may be a task_spawn child that is not linked as spawned yet.
func (s *Service) spawnAncestryUnknown(machine, session string) bool {
	s.spawns.mu.Lock()
	defer s.spawns.mu.Unlock()
	if s.spawns.suspect[[2]string{machine, session}] {
		return true
	}
	for p := range s.spawns.pending {
		if p.machine == machine && !p.linkedBefore[session] {
			return true
		}
	}
	return false
}

// linkedSessionsOn lists the sessions of machine that have an active to-do link.
func (s *Service) linkedSessionsOn(machine string) map[string]bool {
	ids := map[string]bool{}
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	for key := range s.todos.activeBySession {
		if key[0] == machine {
			ids[key[1]] = true
		}
	}
	return ids
}
