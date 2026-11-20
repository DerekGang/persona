package assistant

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- which launchers can run the assistant's conversations ----
//
// The main chat, a scheduled task's conversation and the side conversations run headless on their machine: claude -p
// or codex app-server started through the launcher's shell command (node/assistant_start.go), or a runtime that
// declared caps.assistant. The picker (?purpose=assistant) lists assistantLaunchers (modelProviderCompatible), and the
// check before each message (sendModel) holds a launcher to the same list through mainChatFit. On top of that, the
// pickers leave out a launcher whose latest health check failed (launcher_health_check.go, withHealth): health keeps a
// message from being refused as launcher_missing on a wrong read, but a failing launcher is not refused here, and a
// model configuration that names one is left alone.

// mainChatFit is whether launcher name can run an assistant conversation on machine: in the main chat's list (fit),
// one the machine has that can't run it (refused, with why in the user's language), or not there at all (gone).
// Plain claude fits without asking; a list that can't be read fits too, as launchersHere has it: nothing is refused
// or replaced on a guess.
type mainChatFit int

const (
	mainChatFits mainChatFit = iota
	mainChatRefused
	mainChatGone
)

func (s *Service) mainChatFit(ctx context.Context, machine, name string) (mainChatFit, error) {
	fit, refusal, _ := s.mainChatFitState(ctx, machine, name, false)
	return fit, refusal
}

// mainChatFitState also reports whether gone is a discovery miss that may recover. A stored provider outside the
// machine's scope is not a transient miss and must not hold a message for the rediscovery window.
func (s *Service) mainChatFitState(ctx context.Context, machine, name string, refresh bool) (mainChatFit, error, bool) {
	if proto.IsExternalLauncher(name) {
		_, err := s.externalTarget(ctx, machine, name)
		if err != nil {
			return mainChatRefused, err, false
		}
		return mainChatFits, nil, false
	}
	if name == "" || name == "claude" {
		return mainChatFits, nil, false
	}
	ctx = withUnhidden(withSettingsMemo(ctx), name)
	if m, managed := s.managedNamed(ctx, name); managed { // the same on every machine in its scope, except managed agy (needs the agy runtime there)
		if !s.usableOn(ctx, m, machine) {
			return mainChatGone, nil, false
		}
		if m.Kind == "api" || m.Agent != "agy" {
			allowed, err := s.assistantProviders(ctx, machine, nil)
			if err != nil || allowed[name] {
				return mainChatFits, nil, false
			}
			return mainChatRefused, s.mainChatRefusal(ctx, machine, name, ""), false
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if refresh {
		cctx = withLauncherRefresh(cctx)
	}
	picker, err := s.pickerLaunchers(cctx, machine)
	if err != nil {
		if refresh {
			return mainChatGone, nil, true // a failed retry is not evidence that the missing launcher recovered
		}
		return mainChatFits, nil, false // an ordinary unreadable list is not proof that a shell launcher is gone
	}
	if !slices.ContainsFunc(picker, func(l ConfiguredLauncher) bool {
		source := compat.Source(l.Source)
		return l.Source != "" && source != "persona" && source != "builtin"
	}) {
		return mainChatFits, nil, false // the machine's own list did not come: a shell launcher may still be there
	}
	fits, err := s.assistantLaunchersOf(cctx, machine, picker)
	named := func(l ConfiguredLauncher) bool { return l.Name == name }
	switch {
	case err != nil || slices.ContainsFunc(fits, named):
		return mainChatFits, nil, false
	case slices.ContainsFunc(picker, named):
		label := launcherLabel(picker[slices.IndexFunc(picker, named)])
		instead, _ := assistantFor(fits, name)
		return mainChatRefused, s.mainChatRefusal(ctx, machine, label, launcherLabel(instead)), false
	}
	return mainChatGone, nil, true
}

const (
	defaultLauncherRecoveryWait = 30 * time.Second
	maxLauncherRecoveryWait     = 2 * time.Minute
)

var launcherRecoveryPoll = time.Second

func (s *Service) launcherRecovery(ctx context.Context, machine, name string) (time.Duration, []string) {
	cfg := s.hello(ctx).Models.LauncherRecovery[machine]
	wait := defaultLauncherRecoveryWait
	if cfg.WaitSeconds > 0 {
		wait = time.Duration(cfg.WaitSeconds) * time.Second
	}
	if wait > maxLauncherRecoveryWait {
		wait = maxLauncherRecoveryWait
	}
	return wait, cfg.Fallbacks[name]
}

// waitMainChatLauncher retries fresh discovery inside one total budget. Request cancellation wins; exhaustion returns
// the original miss so finishMainChatModel can use an explicitly configured substitute or refuse the send.
func (s *Service) waitMainChatLauncher(ctx context.Context, machine, name string, wait time.Duration) (mainChatFit, error) {
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ticker := time.NewTicker(launcherRecoveryPoll)
	defer ticker.Stop()
	for {
		select {
		case <-wctx.Done():
			if ctx.Err() != nil {
				return mainChatGone, ctx.Err()
			}
			return mainChatGone, nil
		case <-ticker.C:
			fit, refusal, _ := s.mainChatFitState(wctx, machine, name, true)
			if ctx.Err() != nil {
				return mainChatGone, ctx.Err()
			}
			if wctx.Err() != nil {
				return mainChatGone, nil
			}
			if fit != mainChatGone {
				return fit, refusal
			}
		}
	}
}

// finishMainChatModel validates the binding after model resolution, including a default or a legacy fallback that
// was not the caller's explicit pick. A discovery miss gets one bounded rediscovery window. If the launcher still
// cannot run Assistant there, only launchers explicitly named in its recovery policy may replace it, and only ones of
// the same agent kind (mainChatInstead): a Codex conversation started on Claude, or the other way round, would run
// without the configured model or credentials (#4659). Built-in claude has no implicit priority or eligibility. The
// model stays when an approved substitute's list has it. With no approved substitute, nothing is sent and the user is
// told why. note says which launcher runs instead, in the user's language.
func (s *Service) finishMainChatModel(ctx context.Context, machine, purpose string, params map[string]any, picked bool) (note string, err error) {
	name, _ := params["launcher"].(string)
	if proto.IsExternalLauncher(name) {
		if _, ok := ctx.Value(externalPickKey{}).(externalPickScope); !ok {
			return "", i18n.Err("external-adapter.scope_required")
		}
		_, err := s.externalTarget(ctx, machine, name)
		return "", err // never swap an explicit external selection
	}
	fit, refusal, transient := s.mainChatFitState(ctx, machine, name, false)
	wait, fallbacks := s.launcherRecovery(ctx, machine, name)
	if fit == mainChatGone && transient && wait > 0 {
		s.logf("[%s] resolved assistant launcher %s is missing; refreshing discovery for up to %s", machine, name, wait)
		fit, refusal = s.waitMainChatLauncher(ctx, machine, name, wait)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if fit == mainChatFits {
		return "", nil
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	launchers, err := s.assistantLaunchers(withLauncherRefresh(cctx), machine)
	if err != nil {
		return "", err
	}
	kind := s.mainChatLauncherKind(ctx, machine, name)
	d, _ := s.device(ctx, machine)
	where := firstNonEmpty(d.Name, machine)
	to := s.mainChatInstead(ctx, d, name, kind, launchers, fallbacks)
	if to == "" {
		if refusal != nil {
			return "", refusal
		}
		return "", i18n.Err("assistant-start.launcher_no_substitute", i18n.Args{"launcher": name, "machine": where})
	}
	model, _ := params["model"].(string)
	kept := model != "" && s.launcherHasModel(withLauncherPurpose(ctx, "assistant"), machine, to, model) // a list it can't read counts as having it
	s.logf("[%s] resolved assistant launcher %s can't run it, using %s (model kept: %v)", machine, name, to, kept)
	params["launcher"] = to
	if !kept {
		delete(params, "model")
	}
	if picked {
		err = s.applyPickedModel(ctx, params)
	} else {
		err = s.applyModel(ctx, purpose, params)
	}
	if err != nil {
		return "", err
	}
	args := i18n.Args{"launcher": name, "machine": where, "to": to, "model": model}
	key := "assistant-start.launcher_swapped"
	if model != "" && !kept {
		key = "assistant-start.launcher_swapped_model"
	}
	note = i18n.T(s.lang(ctx), key, args)
	leaveLauncherNote(ctx, note)
	return note, nil
}

// leaveLauncherNote puts note where withLauncherNote's caller reads it, if it asked.
func leaveLauncherNote(ctx context.Context, note string) {
	if slot, ok := ctx.Value(launcherNoteKey{}).(*string); ok && note != "" {
		*slot = note
	}
}

type launcherNoteKey struct{}

// withLauncherNote gives finishMainChatModel somewhere to leave its note (which launcher runs instead, and why) for
// the caller that answers the app: the start route and assistant.send_params, whose answers carry it as
// launcher_note for the app to show (#4659).
func withLauncherNote(ctx context.Context) (context.Context, *string) {
	n := new(string)
	return context.WithValue(ctx, launcherNoteKey{}, n), n
}

// mainChatInstead chooses only from names, the resolved launcher's explicitly configured ordered substitutes. Every
// candidate must currently run Assistant on the machine, have the same agent kind, and have no failed health check.
// In particular, plain claude is not implicitly preferred or eligible. "" when no approved substitute fits.
func (s *Service) mainChatInstead(ctx context.Context, d Device, name, kind string, launchers []ConfiguredLauncher, names []string) string {
	for _, n := range names {
		if n == name || proto.IsExternalLauncher(n) {
			continue
		}
		i := slices.IndexFunc(launchers, func(l ConfiguredLauncher) bool { return l.Name == n })
		if i < 0 {
			continue
		}
		l := launchers[i]
		if l.Source == proto.ExternalLauncherSource || agentKind(cmp.Or(l.AgentType, l.Agent)) != kind {
			continue
		}
		if _, bad := s.launcherUnusable(ctx, d.ID, n); !bad {
			return n
		}
	}
	return ""
}

// mainChatLauncherKind keeps the agent identity of a launcher that has just disappeared from discovery. The last
// successful per-machine snapshot is evidence; without it, preserve the historical Claude default for launchers that
// never declared an agent. In particular, a missing shell Codex alias must not become Claude merely because its current
// catalog entry is gone.
func (s *Service) mainChatLauncherKind(ctx context.Context, machine, name string) string {
	if agent := s.launcherAgent(withUnhidden(ctx, name), machine, name); agent != "" {
		return agentKind(agent)
	}
	seen, _ := s.zshrcLaunchersLastSeen(ctx)
	for _, m := range seen {
		if m.MachineID != machine {
			continue
		}
		for _, l := range m.Launchers {
			if l.Name == name && l.Agent != "" {
				return agentKind(l.Agent)
			}
		}
	}
	return agentKind("")
}

// agentKind is the agent a launcher starts: claude when it names none (or is gone: it ran the main chat's kind).
func agentKind(agent string) string { return cmp.Or(agent, "claude") }

// mainChatRefusal says that launcher can't run the assistant on machine, for the user's language, and names instead
// when a launcher there declares it runs the assistant for that one (assistantFor; "" when none does).
func (s *Service) mainChatRefusal(ctx context.Context, machine, launcher, instead string) error {
	d, _ := s.device(ctx, machine)
	args := i18n.Args{"launcher": launcher, "machine": firstNonEmpty(d.Name, machine), "instead": instead}
	if instead != "" {
		return i18n.Err("assistant-start.launcher_not_main_chat_instead", args)
	}
	return i18n.Err("assistant-start.launcher_not_main_chat", args)
}

// assistantFor is the launcher in fits (the main chat's list on a computer) that declares it runs the assistant for
// that computer's Agent launcher name (proto.Launcher.AssistantFor): integrations/grok's grok-chat for the Paseo provider
// grok when its companion was set up so. Only a declaration counts, never a likeness of names or agents: two launchers
// of one agent may run another account or gateway. The picker points name at it; nothing runs on it in name's place.
func assistantFor(fits []ConfiguredLauncher, name string) (ConfiguredLauncher, bool) {
	i := slices.IndexFunc(fits, func(f ConfiguredLauncher) bool { return f.Assistant && f.AssistantFor == name && f.Name != name })
	if i < 0 {
		return ConfiguredLauncher{}, false
	}
	return fits[i], true
}

// launcherLabel is the name people know l by: its display name (a runtime's or Paseo's for its provider) or its own;
// "" for none.
func launcherLabel(l ConfiguredLauncher) string { return cmp.Or(l.DisplayName, l.Name) }

// ---- what can't run the assistant's conversations, and why ----

// Why a launcher or provider is in the main chat picker's unavailable list (GET /api/v1/machines/{mid}/launchers?purpose=
// assistant, #5033), besides proto.CodeLauncherOutOfScope and proto.CodeLauncherOwnService.
const (
	unavailableAPIProtocol = "api_protocol"  // a direct API provider speaking Responses or Chat Completions, not Messages
	unavailableNotMainChat = "not_main_chat" // its agent or engine can't run the assistant (a Paseo provider, managed Codex…)
)

// unavailableLauncher is one launcher or provider the main chat can't run on a machine, and why in the user's words.
type unavailableLauncher struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"` // a Persona-managed provider's own name, or a runtime's for its provider ("" shows Name)
	Kind        string `json:"kind"`                   // launcher | api
	Agent       string `json:"agent,omitempty"`
	API         string `json:"api,omitempty"` // a direct API provider's protocol: messages | responses | chat
	Code        string `json:"code"`
	Reason      string `json:"reason"`
	Instead     string `json:"instead,omitempty"` // the launcher set up to run the assistant for this one here (assistantFor), by name
}

// mainChatUnavailable lists what the main chat's model panel shows as unavailable on machine, with why in lang: the
// machine's launchers (picker, pickerLaunchers) that can't run the assistant (fits, assistantLaunchersOf, are the ones
// that can), then Settings › Model providers' providers that can't run it here: a direct API that speaks Responses or
// Chat Completions, one whose agent can't, one out of the computer's device scope or skipping a computer with its
// own sign-in. Hidden launchers stay out, and so does a provider that would run here but is not listed for another
// reason (a failed health check, a list that has not come yet): nothing here refuses it.
func (s *Service) mainChatUnavailable(ctx context.Context, machine string, picker, fits []ConfiguredLauncher, lang i18n.Lang) []unavailableLauncher {
	ctx = withSettingsMemo(ctx)
	out := []unavailableLauncher{}
	seen := map[string]bool{}
	for _, l := range fits {
		seen[l.Name] = true
	}
	for _, name := range s.hiddenLaunchers(ctx) {
		seen[name] = true
	}
	stored := s.storedLaunchers(ctx)
	managed := func(name string) (storedLauncher, bool) {
		i := slices.IndexFunc(stored, func(l storedLauncher) bool { return l.Name == name })
		if i < 0 {
			return storedLauncher{}, false
		}
		return stored[i], true
	}
	d, _ := s.device(ctx, machine)
	where := firstNonEmpty(d.Name, machine)
	notMainChat := i18n.T(lang, "assistant-model_pick.unavailable_not_main_chat", i18n.Args{"machine": where})
	for _, l := range picker {
		if seen[l.Name] {
			continue
		}
		seen[l.Name] = true
		// named as the picker names it: a runtime's provider by the runtime's name for it (#8261)
		u := unavailableLauncher{Name: l.Name, DisplayName: l.DisplayName, Kind: "launcher", Agent: cmp.Or(l.AgentType, l.Agent), Code: unavailableNotMainChat,
			Reason: notMainChat}
		if proto.IsExternalLauncher(l.Name) || l.Source == proto.ExternalLauncherSource {
			if l.External != nil {
				u.DisplayName = cmp.Or(l.External.Name, l.DisplayName)
			}
			if err := s.externalLauncherRefusal(ctx, l.Launcher); err != nil {
				u.Reason = i18n.Localize(err, lang)
			}
		}
		if m, ok := managed(l.Name); ok {
			u.DisplayName = m.DisplayName
		} else if f, ok := assistantFor(fits, l.Name); ok { // the launcher set up to run the assistant for this one
			u.Instead = f.Name
			u.Reason = i18n.T(lang, "assistant-model_pick.unavailable_not_main_chat_instead", i18n.Args{"machine": where, "instead": launcherLabel(f)})
		}
		out = append(out, u)
	}
	p, err := s.modelPurposeDef(ctx, "assistant")
	p.MachineID = "" // what the provider is, its scope apart: that is the next reason
	for _, l := range stored {
		if seen[l.Name] {
			continue
		}
		seen[l.Name] = true
		kind := cmp.Or(l.Kind, "launcher")
		u := unavailableLauncher{Name: l.Name, DisplayName: l.DisplayName, Kind: kind, Agent: l.Agent}
		if kind == "api" {
			u.Agent, u.API = "", cmp.Or(l.API, "messages")
		}
		switch {
		case kind == "api" && !messagesAPI(l.API):
			u.Code, u.Reason = unavailableAPIProtocol, apiProtocolText(lang, l.API)
		case err == nil && !s.modelProviderCompatible(p, ModelProvider{ID: "launcher." + l.Name, Kind: kind, Agent: l.Agent, API: l.API}, stored):
			u.Code, u.Reason = unavailableNotMainChat, notMainChat
		case s.scopeReason(l, machine) != "":
			u.Code, u.Reason = proto.CodeLauncherOutOfScope, s.scopeText(ctx, lang, s.scopeReason(l, machine), l, machine)
		case !s.usableOn(ctx, l, machine): // its own-service rule, as the node said
			u.Code, u.Reason = proto.CodeLauncherOwnService, s.ownServiceErr(ctx, lang, l, machine).Error()
		default:
			continue
		}
		out = append(out, u)
	}
	return out
}

// ownServiceSkipped lists what New Agent's picker shows as unavailable on machine, with why in lang (#8109): the
// Persona-managed providers in the computer's scope that its own-service rule leaves out there, where the node said
// the harness has a service of its own. A provider added with the defaults (own_service skip) would otherwise just be
// missing from the picker on a computer where Claude Code is signed in. Those out of scope stay out (the scope is
// what the user chose for the provider), and so do hidden names, direct API connections (never in this picker) and
// what the picker lists already (a launcher of the computer's own by the same name).
func (s *Service) ownServiceSkipped(ctx context.Context, machine string, listed []ConfiguredLauncher, lang i18n.Lang) []unavailableLauncher {
	hidden := s.hiddenLaunchers(ctx)
	out := []unavailableLauncher{}
	for _, l := range s.storedLaunchers(ctx) {
		if l.Kind == "api" || !l.SkipsOwnService() || slices.Contains(hidden, l.Name) ||
			slices.ContainsFunc(listed, func(c ConfiguredLauncher) bool { return c.Name == l.Name }) {
			continue
		}
		if s.scopeReason(l, machine) != "" || s.usableOn(ctx, l, machine) {
			continue
		}
		out = append(out, unavailableLauncher{Name: l.Name, DisplayName: l.DisplayName, Kind: "launcher", Agent: l.Agent,
			Code: proto.CodeLauncherOwnService, Reason: s.ownServiceErr(ctx, lang, l, machine).Error()})
	}
	return out
}

// apiProtocolText says in lang why a direct API provider speaking api can't run the assistant's conversations.
func apiProtocolText(lang i18n.Lang, api string) string {
	switch api {
	case "responses":
		return i18n.T(lang, "assistant-model_pick.unavailable_api_responses")
	case "chat":
		return i18n.T(lang, "assistant-model_pick.unavailable_api_chat")
	}
	return i18n.T(lang, "assistant-model_pick.unavailable_api_other", i18n.Args{"api": api})
}
