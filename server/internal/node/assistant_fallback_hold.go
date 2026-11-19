package node

import (
	"strings"
	"time"
)

// ---- staying on the fallback that answered (#3788) ----
//
// A refusal sticks to a conversation: the refused turn stays in its transcript, so a resend carries it again (on one
// install 43 of 67 refused turns came in runs, #3204). The main conversation and a scheduled task's ask for the
// configured model with every message (the manager's sendModel), so a fallback that answered would last one turn: the
// next message restarts the process on the refused model, is refused, retried, and moved back again, each restart
// writing the whole prompt cache anew. After a model switch answered, the conversation stays on it for a while, and
// a refusal soon after skips the same-model retry.

const (
	// assistantFallbackHold: how long messages asking for the model a recovery moved away from stay on its fallback.
	// A compaction ends it sooner (the refused turns are summarized away), and so does any other model picked.
	assistantFallbackHold = 30 * time.Minute
	// assistantRefusalSticky: a conversation refused again this soon after its last fallback answered skips the
	// same-model retry and goes on to the next model at once.
	assistantRefusalSticky = 2 * time.Hour
)

// assistantFallback is a conversation's last model switch that answered: on launcher, away from what it was asked
// to run (from) to the model that answered (to), at at; until ends the hold (zero once over). newChoice records that
// a later request explicitly chose a model instead of continuing this old recovery chain.
type assistantFallback struct {
	launcher  string
	from, to  modelExecution
	at        time.Time
	until     time.Time
	newChoice bool
}

// sameModelChoice: two requests for the same model, whatever the message around them.
func sameModelChoice(a, b modelExecution) bool {
	return a.Model == b.Model && a.Effort == b.Effort && a.UseEffort == b.UseEffort && a.Fingerprint == b.Fingerprint
}

// holdFallback starts conversation id's hold on to, after a recovery's switch away from from answered there. n.mu is
// held.
func (n *Node) holdFallback(id, launcher string, from, to modelExecution) {
	if n.fallbacks == nil {
		n.fallbacks = map[string]*assistantFallback{}
	}
	now := time.Now()
	n.fallbacks[id] = &assistantFallback{launcher: launcher, from: from, to: to, at: now, until: now.Add(assistantFallbackHold)}
}

// heldFallback is the execution conversation id's message runs on: its fallback while the hold lasts and the message
// asks for the model the switch moved away from, else what it asks for. Another model asked for ends the hold.
func (n *Node) heldFallback(id, launcher string, asked modelExecution) modelExecution {
	execution, _ := n.heldFallbackWithExpiry(id, launcher, asked)
	return execution
}

// fallbackPrevious is the session's execution identity and launcher before the current request. It is optional in
// tests of in-memory holds; cold and stale markers need it to distinguish a same-model edit from continuing the old chain.
type fallbackPrevious struct {
	launcher  string
	execution modelExecution
}

// heldFallbackWithExpiry also reports whether an existing hold for this exact request expired naturally. A missing
// hold after a restart is not expiry, and another model choice is not expiry either.
func (n *Node) heldFallbackWithExpiry(id, launcher string, asked modelExecution, previous ...fallbackPrevious) (modelExecution, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	asked.NewModelChoice = false
	f := n.fallbacks[id]
	now := time.Now()
	if f != nil && now.Sub(f.at) >= assistantRefusalSticky {
		if f.launcher == launcher && !now.Before(f.until) && sameModelChoice(asked, f.from) {
			delete(n.fallbacks, id)
			return asked, true // this exact old request still expires naturally; its age does not make it a cold restart
		}
		delete(n.fallbacks, id)
		f = nil
	}
	if f == nil {
		// A cold restart or an expired in-memory record can leave only the persisted marker. Compare the complete
		// execution identity and launcher so a same-model edit is not mistaken for continuing the old chain.
		r := n.recoveryMarks[id]
		if r == nil {
			if st, ok := n.state.get(id); ok {
				r = st.ModelRecovery
			}
		}
		if modelRecoverySwitched(r) {
			choice := modelExecution{Model: r.From, Effort: asked.Effort, UseEffort: asked.UseEffort, Fingerprint: asked.Fingerprint}
			oldLauncher := ""
			if r.Choice != nil {
				// The chain's own record is authoritative: recovery sends may have overwritten session model/effort.
				choice = modelExecution{Model: r.From, Effort: r.Choice.Effort, UseEffort: r.Choice.UseEffort, Fingerprint: r.Choice.Fingerprint}
				oldLauncher = r.Choice.Launcher
			} else if len(previous) > 0 {
				// Records from older nodes have no choice snapshot; use the pre-request session as the compatibility path.
				choice = previous[0].execution
				choice.Model = r.From
				oldLauncher = previous[0].launcher
			}
			asked.NewModelChoice = !sameModelChoice(asked, choice) || oldLauncher != "" && oldLauncher != launcher
			if asked.NewModelChoice {
				// Keep the old chain as a superseded record. It supplies no budget to this new pick, but a rejected pick
				// must not erase the old sticky behavior, and a cold marker otherwise has no record to restore.
				if oldLauncher == "" {
					oldLauncher = launcher
				}
				to := choice
				to.Model = r.To
				at := r.At
				if at.IsZero() {
					at = now
				}
				if n.fallbacks == nil {
					n.fallbacks = map[string]*assistantFallback{}
				}
				n.fallbacks[id] = &assistantFallback{launcher: oldLauncher, from: choice, to: to, at: at, newChoice: true}
			}
		}
		return asked, false
	}
	if !now.Before(f.until) || f.launcher != launcher || !sameModelChoice(asked, f.from) {
		expired := !now.Before(f.until) && f.launcher == launcher && sameModelChoice(asked, f.from)
		f.newChoice = f.launcher != launcher || !sameModelChoice(asked, f.from)
		asked.NewModelChoice = f.newChoice
		f.until = time.Time{} // over; a refusal soon after still skips the same-model retry
		return asked, expired
	}
	out := f.to
	out.IsMain, out.Interject, out.Ultracode = asked.IsMain, asked.Interject, asked.Ultracode // the message's own
	out.SupervisionInstructions = asked.SupervisionInstructions
	out.NewModelChoice = false // the old hold selected this execution, not a new pick
	f.newChoice = false
	return out, false
}

// recentFallback: conversation id's last fallback answered within assistantRefusalSticky, and what the conversation
// was asked to run before it (the model a hold keeps it away from). newChoice says whether a later request superseded
// the record; newAssistantRecovery decides whether this execution belongs to the old chain. n.mu is held.
func (n *Node) recentFallback(id string) (from modelExecution, launcher string, ok bool) {
	f := n.fallbacks[id]
	if f == nil || time.Since(f.at) >= assistantRefusalSticky {
		return modelExecution{}, "", false
	}
	return f.from, f.launcher, true
}

// assistantUnavailableFor: how long a model a recovery found unavailable on a launcher is not picked again, by any
// conversation (#5234). A gateway that drops a model keeps it dropped for longer than one chain; one that adds it
// back is tried again after this.
const assistantUnavailableFor = 6 * time.Hour

// assistantAnsweredRecently: a model that answered this conversation this recently and now "does not exist" is the
// gateway's passing failure, not the user's choice (#5234).
const assistantAnsweredRecently = time.Hour

// noteUnavailable records that launcher's model is unavailable, so later chains skip it. n.mu is held.
func (n *Node) noteUnavailable(launcher, model string) {
	if model == "" {
		return
	}
	if n.unavailable == nil {
		n.unavailable = map[string]time.Time{}
	}
	n.unavailable[launcher+"\x00"+model] = time.Now().Add(assistantUnavailableFor)
}

// markUnavailableUsed marks every model of launcher found unavailable, and not expired, as used by chain r. n.mu is
// held.
func (n *Node) markUnavailableUsed(launcher string, used map[string]bool) {
	now := time.Now()
	for k, until := range n.unavailable {
		if now.After(until) {
			delete(n.unavailable, k)
			continue
		}
		if l, m, ok := strings.Cut(k, "\x00"); ok && l == launcher {
			used[m] = true
		}
	}
}

// forgetFallback ends conversation id's hold and its sticky window: it compacted, so the refused turns are gone.
func (n *Node) forgetFallback(id string) {
	n.mu.Lock()
	delete(n.fallbacks, id)
	n.mu.Unlock()
}
