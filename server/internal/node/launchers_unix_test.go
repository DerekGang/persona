//go:build !windows

package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A ~/.zshrc that leaves something running in the background, holding the shell's output open (#10952): the launcher
// list comes back once the shell is done, not when that child is (the hub gives up on the computer after 30 s, and
// every ask after it waited behind this one), and it is cached for the next ask within the minute.
func TestLaunchersDoNotWaitForWhatTheRcFileLeavesRunning(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("no zsh")
	}
	home := t.TempDir()
	pidFile := filepath.Join(home, "pid")
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("sleep 300 &\necho $! > "+pidFile+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("SHELL", "/bin/zsh")
	killPidFileAtCleanup(t, pidFile)

	launcherCache.Lock()
	at, list := launcherCache.at, launcherCache.list
	codex, settings, scripts := launcherCache.codex, launcherCache.settings, launcherCache.scripts
	launcherCache.at, launcherCache.list = time.Time{}, nil
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list = at, list
		launcherCache.codex, launcherCache.settings, launcherCache.scripts = codex, settings, scripts
		launcherCache.Unlock()
	})

	type answer struct {
		list []proto.Launcher
		err  error
	}
	ask := func() (answer, time.Duration) {
		t.Helper()
		began := time.Now()
		done := make(chan answer, 1)
		go func() {
			ls, err := (&Node{}).launchers(context.Background())
			done <- answer{ls, err}
		}()
		select {
		case a := <-done:
			return a, time.Since(began)
		case <-time.After(15 * time.Second):
			t.Fatal("the launcher list still waits for the rc file's background child after 15 s")
			return answer{}, 0
		}
	}
	a, took := ask()
	t.Logf("first ask: %d launchers in %v (error %v)", len(a.list), took, a.err)
	if a.err != nil || len(a.list) == 0 {
		t.Fatalf("launchers %v, error %v; want the list (nothing at all: plain claude)", a.list, a.err)
	}
	if took > 10*time.Second {
		t.Errorf("first ask took %v, want within ~3 s of the shell finishing", took)
	}
	if again, took := ask(); took > time.Second || len(again.list) != len(a.list) {
		t.Errorf("second ask: %d launchers in %v, want the cached %d at once", len(again.list), took, len(a.list))
	}
}

func TestLaunchersRefreshReplacesTheCachedList(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("no zsh")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(zsh, filepath.Join(bin, "zsh")); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("alias custom-a='claude'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("SHELL", zsh)
	t.Setenv("PATH", bin)
	launcherCache.Lock()
	at, list := launcherCache.at, launcherCache.list
	codex, settings, scripts := launcherCache.codex, launcherCache.settings, launcherCache.scripts
	launcherCache.at, launcherCache.list = time.Time{}, nil
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list = at, list
		launcherCache.codex, launcherCache.settings, launcherCache.scripts = codex, settings, scripts
		launcherCache.Unlock()
	})

	n := &Node{}
	first, err := n.launchers(context.Background())
	if err != nil || len(first) != 1 || first[0].Name != "custom-a" {
		t.Fatalf("initial discovery: %v, %v", first, err)
	}
	if err := os.WriteFile(rc, []byte("alias custom-b='claude'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cached, err := n.launchers(context.Background())
	if err != nil || len(cached) != 1 || cached[0].Name != "custom-a" {
		t.Fatalf("ordinary read did not use the cache: %v, %v", cached, err)
	}
	refreshed, err := n.launchers(context.Background(), true)
	if err != nil || len(refreshed) != 1 || refreshed[0].Name != "custom-b" {
		t.Fatalf("refresh did not replace discovery: %v, %v", refreshed, err)
	}
	cached, err = n.launchers(context.Background())
	if err != nil || len(cached) != 1 || cached[0].Name != "custom-b" {
		t.Fatalf("refreshed list was not cached: %v, %v", cached, err)
	}
}

func TestLaunchersQueuedRefreshUsesDiscoveryCompletedWhileWaiting(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("no zsh")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, program := range map[string]string{"zsh": zsh, "sleep": sleep} {
		if err := os.Symlink(program, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("alias custom-a='claude'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("SHELL", zsh)
	t.Setenv("PATH", bin)
	launcherCache.Lock()
	at, list, generation := launcherCache.at, launcherCache.list, launcherCache.generation
	codex, settings, scripts := launcherCache.codex, launcherCache.settings, launcherCache.scripts
	launcherCache.at, launcherCache.list = time.Time{}, nil
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list, launcherCache.generation = at, list, generation
		launcherCache.codex, launcherCache.settings, launcherCache.scripts = codex, settings, scripts
		launcherCache.Unlock()
	})

	n := &Node{}
	if _, err := n.launchers(context.Background()); err != nil {
		t.Fatal(err)
	}
	started, release := filepath.Join(home, "discovery-started"), filepath.Join(home, "discovery-release")
	gated := "alias custom-b='claude'\nprint -r -- started > \"$HOME/discovery-started\"\n" +
		"while [[ ! -f \"$HOME/discovery-release\" ]]; do sleep 0.02; done\n"
	if err := os.WriteFile(rc, []byte(gated), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("release\n"), 0o600) })
	type answer struct {
		list []proto.Launcher
		err  error
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan answer, 1)
	go func() {
		list, err := n.launchers(ctx, true)
		firstDone <- answer{list: list, err: err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the first discovery did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel() // The RPC caller may give up; node discovery deliberately finishes and publishes its result.
	if err := os.WriteFile(rc, []byte("alias custom-c='claude'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondDone := make(chan answer, 1)
	go func() {
		list, err := n.launchers(context.Background(), true)
		secondDone <- answer{list: list, err: err}
	}()
	// Keep the first shell blocked until the queued refresh has entered launchers and recorded the old generation.
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, done := range []chan answer{firstDone, secondDone} {
		select {
		case got := <-done:
			if got.err != nil || len(got.list) != 1 || got.list[0].Name != "custom-b" {
				t.Fatalf("discovery %d = %v, %v; want the result completed while queued", i+1, got.list, got.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("discovery %d did not finish", i+1)
		}
	}
}
