//go:build !windows

package node

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// What codex_terminal_modes.go counts on, against a real Codex TUI (opt-in): the /model line it builds is taken between
// turns, Codex records it in the rollout as thread_settings_applied with the values typed, a model Codex does not have
// is refused on the screen with nothing recorded, and config.toml keeps its model and effort. Codex runs in a private
// pty with a temporary CODEX_HOME and HOME, on a provider nothing listens to, so no model is called:
//
//	PERSONA_LIVE_CODEX_TUI     the codex binary (0.169 or later)
//	PERSONA_LIVE_CODEX_MODELS  two models its catalog has, comma-separated: the one it starts on, then another
func TestCodexTerminalLive(t *testing.T) {
	bin := os.Getenv("PERSONA_LIVE_CODEX_TUI")
	if bin == "" {
		t.Skip("PERSONA_LIVE_CODEX_TUI (a codex 0.169 or later) not set")
	}
	first, second := "gpt-6.2-astra", "gpt-6.2-sol"
	if m := strings.Split(os.Getenv("PERSONA_LIVE_CODEX_MODELS"), ","); len(m) == 2 {
		first, second = strings.TrimSpace(m[0]), strings.TrimSpace(m[1])
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, work := filepath.Join(dir, "codex-home"), filepath.Join(dir, "work")
	for _, d := range []string{home, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("model = %q\nmodel_reasoning_effort = \"medium\"\nmodel_provider = \"offline\"\ncheck_for_update_on_startup = false\n\n"+
		"[model_providers.offline]\nname = \"offline\"\nbase_url = \"http://127.0.0.1:9/v1\"\nwire_api = \"responses\"\n\n"+
		"[projects.%q]\ntrust_level = \"trusted\"\n", first, work)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	tui := startLiveCodexTUI(t, bin, dir, home, work)
	tui.until(t, 60*time.Second, "its input box", func() bool { return strings.Contains(tui.text(), "Ask Codex") })

	// The first message starts the thread and its rollout; nothing answers it, and Esc ends the turn.
	tui.typeLine(t, "say hi")
	var rollout string
	tui.until(t, 30*time.Second, "its rollout", func() bool {
		found, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*.jsonl"))
		if len(found) == 1 {
			rollout = found[0]
		}
		return rollout != ""
	})
	if v := codexRolloutVersion(rollout); !versionAtLeast(v, codexModelArgsMinVersion) {
		t.Skipf("codex %q has no /model arguments", v)
	}
	tui.write(t, "\x1b")
	tui.until(t, 30*time.Second, "the turn to end", func() bool {
		b, _ := os.ReadFile(rollout)
		return bytes.Contains(b, []byte(`"turn_aborted"`)) || bytes.Contains(b, []byte(`"task_complete"`))
	})

	for _, c := range []struct{ model, effort string }{{"", "high"}, {second, "low"}, {first, "xhigh"}} {
		cmd, err := codexModelArgsCommand(c.model, c.effort)
		if err != nil {
			t.Fatal(err)
		}
		from := rolloutSize(rollout)
		tui.typeLine(t, cmd)
		tui.until(t, 15*time.Second, "the rollout to record "+cmd, func() bool { return codexSettingsRecorded(rollout, from, c.model, c.effort) })
		t.Logf("LIVE %s: recorded", cmd)
	}

	cmd, _ := codexModelArgsCommand("no-such-model", "")
	from, shown := rolloutSize(rollout), len(tui.text())
	tui.typeLine(t, cmd)
	tui.until(t, 15*time.Second, "Codex's refusal", func() bool {
		out := tui.text()[shown:]
		return strings.Contains(out, "'no-such-model' is unavailable") || strings.Contains(out, "include `no-such-model`")
	})
	if codexSettingsRecorded(rollout, from, "no-such-model", "") {
		t.Error("a refused model was recorded")
	}
	t.Logf("LIVE %s: refused", cmd)

	if model, effort := readCodexConfigDefault(home); model != first || effort != "medium" {
		t.Errorf("config.toml now says %q / %q", model, effort)
	}
}

// liveCodexTUI is a Codex TUI in a private pty, its output kept, the terminal queries crossterm sends answered.
type liveCodexTUI struct {
	tty *os.File
	mu  sync.Mutex
	out []byte
}

// ansiRE matches the escape sequences a TUI writes (CSI, OSC, charset and single-character ones).
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?<=>]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[()][A-Za-z0-9]|\x1b[=>78DEHMNOc]")

// liveTerminalAnswers are the answers a terminal gives the queries a Codex TUI sends at start.
var liveTerminalAnswers = [][2]string{
	{"\x1b[6n", "\x1b[1;1R"}, {"\x1b[?u", "\x1b[?0u"}, {"\x1b[c", "\x1b[?62;22c"}, {"\x1b[0c", "\x1b[?62;22c"},
	{"\x1b]10;?\x07", "\x1b]10;rgb:ffff/ffff/ffff\x07"}, {"\x1b]11;?\x07", "\x1b]11;rgb:0000/0000/0000\x07"},
	{"\x1b]10;?\x1b\\", "\x1b]10;rgb:ffff/ffff/ffff\x1b\\"}, {"\x1b]11;?\x1b\\", "\x1b]11;rgb:0000/0000/0000\x1b\\"},
}

func startLiveCodexTUI(t *testing.T, bin, dir, home, work string) *liveCodexTUI {
	t.Helper()
	cmd := exec.Command(bin, "--no-daemon", "-C", work)
	cmd.Dir = work
	cmd.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin", "TERM=xterm-256color", "LANG=en_US.UTF-8", "CODEX_HOME=" + home}
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 150, Rows: 45})
	if err != nil {
		t.Fatal(err)
	}
	tui := &liveCodexTUI{tty: tty}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64<<10)
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				for _, qa := range liveTerminalAnswers {
					if strings.Contains(chunk, qa[0]) {
						_, _ = tty.WriteString(qa[1])
					}
				}
				tui.mu.Lock()
				tui.out = append(tui.out, buf[:n]...)
				tui.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_, _ = tty.WriteString("\x03\x03")
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = tty.Close()
		<-done
	})
	return tui
}

// text is everything the TUI printed, without escape sequences.
func (c *liveCodexTUI) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ansiRE.ReplaceAllString(string(c.out), "")
}

func (c *liveCodexTUI) write(t *testing.T, s string) {
	t.Helper()
	if _, err := c.tty.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// typeLine pastes text into the box as herdr's agent.prompt does and, once the box shows it, submits it.
func (c *liveCodexTUI) typeLine(t *testing.T, text string) {
	t.Helper()
	shown := len(c.text())
	c.write(t, pasteStart+text+pasteEnd)
	squeeze := func(s string) string { return strings.Join(strings.Fields(s), "") }
	c.until(t, 10*time.Second, "the box to show "+text, func() bool { return strings.Contains(squeeze(c.text()[shown:]), squeeze(text)) })
	c.write(t, "\r")
}

func (c *liveCodexTUI) until(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for deadline := time.Now().Add(d); !ok(); <-tick.C {
		if time.Now().After(deadline) {
			text := c.text()
			t.Fatalf("timed out after %s waiting for %s; the screen ends with:\n%s", d, what, text[max(0, len(text)-2000):])
		}
	}
}

func rolloutSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}
