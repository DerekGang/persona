package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// claimsTestNode is a node with a runtime socket and a fake hub (runtimeNode) that also follows Codex app-servers, as
// remoteTestNode's does.
func claimsTestNode(t *testing.T) *Node {
	t.Helper()
	n, _ := runtimeNode(t)
	n.codices = newCodexRegistry()
	n.codices.scan = func() []codexProcess { return nil }
	n.dir = filepath.Join(n.home, ".persona")
	n.configureCodex(proto.CodexConfig{})
	return n
}

// claimHello is a runtime's hello that claims the Codex app-server instances claims name.
func claimHello(claims ...map[string]any) map[string]any {
	return map[string]any{"runtime": "codexd", "name": "Codex daemon", "version": "test", "caps": map[string]any{"send": true},
		"codex_claims": claims}
}

// remoteOf is the node's connection to Codex app-server id, nil when it has none.
func remoteOf(n *Node, id string) *codexRemote {
	n.codices.mu.Lock()
	defer n.codices.mu.Unlock()
	return n.codices.remotes[id]
}

// threadCalls is how many thread/* requests the fake app-server got.
func (f *remoteFixture) threadCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := 0
	for _, m := range f.got {
		if strings.HasPrefix(m.Method, "thread/") {
			calls++
		}
	}
	return calls
}

// runtimeConns is how many runtime connections the node has.
func runtimeConns(n *Node) int {
	n.runtimes.mu.Lock()
	defer n.runtimes.mu.Unlock()
	return len(n.runtimes.conns)
}

// A runtime that serves an app-server's chats itself claims the instance in its hello: the node closes its own
// connection to it before the hello is answered, does not dial it again while the claim holds, and the instance's
// chats are listed only as the runtime's rows, the thread the runtime does not name included (#12571).
func TestCodexClaimLeavesTheInstanceToTheRuntime(t *testing.T) {
	n := claimsTestNode(t)
	home := t.TempDir()
	f, c := connectRemoteFixture(t, n, home, "daemon", remoteRaw, remoteOther)
	spec := c.spec
	legacy := n.codexInventory().LegacyHome
	named, unnamed := codexSourceID(spec.Home, remoteRaw, legacy), codexSourceID(spec.Home, remoteOther, legacy)
	waitFor(t, "both threads listed from the app-server", func() bool {
		l := listed(t, n)
		_, a := l[named]
		_, b := l[unnamed]
		return a && b
	})

	// The home as the runtime knows it: macOS's TempDir is under /var -> /private/var, the node's is resolved.
	rt := dialRuntime(t, n, claimHello(map[string]any{"home": home}))
	if features, _ := json.Marshal(rt.helloR["features"]); !strings.Contains(string(features), `"codex_claims"`) {
		t.Fatalf("features %s", features)
	}
	if remoteOf(n, "daemon") != nil {
		t.Fatal("the node still follows the instance once the hello claiming it was answered")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("the connection to the claimed instance is still open")
	}

	time.Sleep(200 * time.Millisecond) // a request already on the wire when the connection closed lands meanwhile
	calls := f.threadCalls()
	for range 3 {
		n.attachCodexServers(context.Background(), []proto.CodexServer{spec})
		if remoteOf(n, "daemon") != nil {
			t.Fatal("the node dialled the claimed instance again")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := f.threadCalls(); got != calls {
		t.Fatalf("the claimed app-server got %d thread/* calls after the claim", got-calls)
	}

	_ = rt.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "codexd-1", "status": "idle", "codex_thread": remoteRaw}}})
	waitFor(t, "the runtime's row", func() bool { _, ok := listed(t, n)["codexd-1"]; return ok })
	l := listed(t, n)
	if _, ok := l[named]; ok {
		t.Fatal("the thread the runtime names is listed from the claimed instance as well")
	}
	if _, ok := l[unnamed]; ok {
		t.Fatal("a thread the runtime does not name is still listed from the claimed instance")
	}
}

// A claim outlives its runtime's last connection for the keep, so a runtime that restarts does not take its chats off
// the list and bring them back; once the keep passes with no new connection, the node attaches to the instance again,
// with no restart of its own.
func TestCodexClaimOutlivesAShortDisconnect(t *testing.T) {
	n := claimsTestNode(t)
	n.runtimes.mu.Lock()
	n.runtimes.claimKeep = 300 * time.Millisecond
	n.runtimes.mu.Unlock()
	home := t.TempDir()
	f, c := connectRemoteFixture(t, n, home, "daemon", remoteRaw)
	spec := c.spec
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	attach := func() *codexRemote {
		n.attachCodexServers(ctx, []proto.CodexServer{spec})
		return remoteOf(n, "daemon")
	}
	hello := claimHello(map[string]any{"endpoint": spec.Endpoint}) // by its endpoint this time

	rt := dialRuntime(t, n, hello)
	if remoteOf(n, "daemon") != nil {
		t.Fatal("the instance was not left to the runtime")
	}
	_ = rt.conn.Close()
	waitFor(t, "the runtime gone", func() bool { return runtimeConns(n) == 0 })
	if attach() != nil {
		t.Fatal("the claim ended with its runtime's connection")
	}
	rt = dialRuntime(t, n, hello) // back within the keep
	time.Sleep(600 * time.Millisecond)
	if attach() != nil {
		t.Fatal("the claim of a runtime that came back ended")
	}

	_ = rt.conn.Close()
	waitFor(t, "the runtime gone again", func() bool { return runtimeConns(n) == 0 })
	if attach() != nil {
		t.Fatal("the claim ended before the keep passed")
	}
	time.Sleep(600 * time.Millisecond)
	lists := f.count("thread/loaded/list")
	attach()
	waitFor(t, "the node attached again", func() bool {
		r := remoteOf(n, "daemon")
		if r == nil {
			return false
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.ready
	})
	if f.count("thread/loaded/list") <= lists {
		t.Fatal("the instance was not read again once the claim ended")
	}
	if held := n.runtimes.heldCodexClaims(); len(held) != 0 {
		t.Fatalf("an ended claim is still kept: %v", held)
	}
}

// A claim names an instance by its home, its endpoint, or both; one that names no instance here, or that does not
// resolve, changes nothing.
func TestCodexClaimOfAnotherInstanceChangesNothing(t *testing.T) {
	n := claimsTestNode(t)
	home := t.TempDir()
	_, c := connectRemoteFixture(t, n, home, "daemon", remoteRaw)
	spec := c.spec
	dialRuntime(t, n, claimHello(
		map[string]any{"home": t.TempDir()},                                           // another home
		map[string]any{"endpoint": "ws://127.0.0.1:1"},                                // another endpoint
		map[string]any{"home": home, "endpoint": "ws://127.0.0.1:1"},                  // this home, but another endpoint
		map[string]any{"home": "relative/home"},                                       // not a path
		map[string]any{"endpoint": strings.Replace(spec.Endpoint, "ws:", "http:", 1)}, // not an app-server endpoint
	))
	if held := n.runtimes.heldCodexClaims(); len(held) != 5 {
		t.Fatalf("held %v", held)
	}
	n.attachCodexServers(context.Background(), []proto.CodexServer{spec})
	if remoteOf(n, "daemon") != c {
		t.Fatal("a claim that names another instance closed this one")
	}
	select {
	case <-c.done:
		t.Fatal("a claim that names another instance closed this one")
	default:
	}
}

// A daemon's hello claims nothing: its app-servers are another computer's, and the node does not offer it the feature.
func TestCodexClaimIsNotADaemons(t *testing.T) {
	d := startFakeDaemon(t, "tok", map[string]any{"runtime": "acme", "caps": map[string]any{"send": true},
		"codex_claims": []map[string]any{{"home": t.TempDir()}, {"endpoint": "ws://127.0.0.1:1"}}})
	rh, _ := daemonHost(t, map[string]any{"id": "acme", "url": d.url(), "token": "tok"})
	runtimeNodeOn(t, rh)
	waitFor(t, "the daemon's hello", func() bool { res, _, _ := d.hellod(); return res != nil })
	res, _, _ := d.hellod()
	if f, _ := res["features"].([]any); slices.Contains(f, any("codex_claims")) {
		t.Fatalf("a daemon was offered codex_claims: %v", f)
	}
	if held := rh.reg.heldCodexClaims(); len(held) != 0 {
		t.Fatalf("a daemon claimed %v", held)
	}
	// Even a hello that reached the node with claims (the link passes none on) claims nothing for a daemon.
	h := daemonConn(rh, "acme")
	if h == nil {
		t.Fatal("no daemon connection")
	}
	rh.claimCodex(h, []runtimeCodexClaim{{Home: "/"}})
	if held := rh.reg.heldCodexClaims(); len(held) != 0 {
		t.Fatalf("a daemon claimed %v", held)
	}
}

// The node logs when a runtime claims an instance, once, and when that claim ends; a hello without claims ends the
// runtime's claims at once, with no keep.
func TestCodexClaimIsLoggedAndEndsWithAHelloWithoutClaims(t *testing.T) {
	n := remoteTestNode(t)
	logs := &syncBuffer{}
	n.logger = log.New(logs, "", 0)
	n.runtimes = newRuntimeRegistry()
	home := t.TempDir()
	// An endpoint nothing listens on: once the claim ends the node dials it and fails, which is all this test needs.
	spec := proto.CodexServer{ID: "daemon", Home: canonicalCodexHome(home, n.home), Endpoint: "ws://127.0.0.1:1"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := n.runtimes.add(nil)
	h.mu.Lock()
	h.hello.Runtime = "codexd"
	h.mu.Unlock()
	n.runtimes.setCodexClaims(h, cleanCodexClaims([]runtimeCodexClaim{{Home: home}}))
	n.attachCodexServers(ctx, []proto.CodexServer{spec})
	n.attachCodexServers(ctx, []proto.CodexServer{spec})
	if got := strings.Count(logs.String(), "codex daemon: runtime codexd serves it and claims it"); got != 1 {
		t.Fatalf("claim logged %d times:\n%s", got, logs)
	}
	if remoteOf(n, "daemon") != nil {
		t.Fatal("a claimed instance was dialled")
	}
	n.runtimes.setCodexClaims(h, nil) // its next hello claims nothing
	n.attachCodexServers(ctx, []proto.CodexServer{spec})
	if !strings.Contains(logs.String(), "codex daemon: runtime codexd no longer claims it; attaching again") {
		t.Fatalf("the end of the claim was not logged:\n%s", logs)
	}
}

// A runtime whose connection closed while its hello was being answered claims for the keep only, as if it had left
// right after: its claims do not stand for good with no connection to end them.
func TestCodexClaimOfAConnectionAlreadyGoneOnlyKeeps(t *testing.T) {
	b := newRuntimeRegistry()
	live := b.add(nil)
	gone := b.add(nil)
	for h, id := range map[*runtimeConn]string{live: "live", gone: "gone"} {
		h.mu.Lock()
		h.hello.Runtime = id
		h.mu.Unlock()
	}
	b.remove(gone) // before its claims are recorded
	b.setCodexClaims(live, []runtimeCodexClaim{{Home: "/a"}})
	b.setCodexClaims(gone, []runtimeCodexClaim{{Home: "/b"}})
	b.mu.Lock()
	l, g := b.codexClaims["live"], b.codexClaims["gone"]
	b.mu.Unlock()
	if !l.gone.IsZero() || g.gone.IsZero() {
		t.Fatalf("live %+v, gone %+v", l, g)
	}
	b.mu.Lock()
	b.claimKeep = time.Nanosecond
	b.mu.Unlock()
	time.Sleep(time.Millisecond)
	if held := b.heldCodexClaims(); len(held) != 1 || held[0].runtime != "live" {
		t.Fatalf("held %v", held)
	}
}

// A claim's home and endpoint are compared as the node resolves its own instances': one part names the instances that
// have it, both name only the instance that has both.
func TestCodexClaimantNamesHomeEndpointOrBoth(t *testing.T) {
	n, _ := testNode(t)
	home := canonicalCodexHome(t.TempDir(), n.home)
	s := proto.CodexServer{ID: "a", Home: home, Endpoint: "ws://127.0.0.1:4500/"}
	for _, c := range []struct {
		claim runtimeCodexClaim
		want  string
	}{
		{runtimeCodexClaim{Home: home}, "rt"},
		{runtimeCodexClaim{Endpoint: "ws://127.0.0.1:4500"}, "rt"},
		{runtimeCodexClaim{Home: home, Endpoint: "ws://127.0.0.1:4500"}, "rt"},
		{runtimeCodexClaim{Home: home, Endpoint: "ws://127.0.0.1:4501"}, ""},
		{runtimeCodexClaim{Home: home + "-other"}, ""},
		{runtimeCodexClaim{Endpoint: "ws://localhost:4500"}, ""},
	} {
		held := n.resolveCodexClaims([]heldCodexClaim{{runtime: "rt", runtimeCodexClaim: c.claim}})
		if got := n.codexClaimant(s, held); got != c.want {
			t.Errorf("%+v claims %q, want %q", c.claim, got, c.want)
		}
	}
}

func TestCanonicalCodexEndpoint(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "run")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "app.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.sock")
	if err := os.Symlink(sock, alias); err != nil {
		t.Skip("no symlinks here:", err)
	}
	real := realPath(sock)
	for _, c := range []struct{ in, want string }{
		{"unix://" + sock, "unix://" + real},
		{"unix://" + alias, "unix://" + real},
		{"unix://~/run/app.sock", "unix://" + real},
		{"unix://" + filepath.Join(dir, "later.sock"), "unix://" + filepath.Join(filepath.Dir(real), "later.sock")},
		{"unix://run/app.sock", ""},
		{"ws://127.0.0.1:4500", "ws://127.0.0.1:4500"},
		{"ws://127.0.0.1:4500/", "ws://127.0.0.1:4500"},
		{"ws://[::1]:4500", "ws://[::1]:4500"},
		{"ws://localhost:4500", ""},
		{"ws://127.0.0.1", ""},
		{"ws://127.0.0.1:4500/rpc", ""},
		{"ws://me@127.0.0.1:4500", ""},
		{"ws://127.0.0.1:4500/?token=x", ""},
		{"wss://127.0.0.1:4500", ""},
		{"http://127.0.0.1:4500", ""},
		{"", ""},
	} {
		if got := canonicalCodexEndpoint(c.in, base); got != c.want {
			t.Errorf("canonicalCodexEndpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanCodexClaims(t *testing.T) {
	long := strings.Repeat("x", maxCodexClaimText+1)
	got := cleanCodexClaims([]runtimeCodexClaim{{Home: " /a "}, {}, {Home: " "}, {Home: "/a"}, {Endpoint: long}, {Home: "/b", Endpoint: " unix:///s "}})
	want := []runtimeCodexClaim{{Home: "/a"}, {Home: "/b", Endpoint: "unix:///s"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	var many []runtimeCodexClaim
	for i := range maxCodexClaims + 4 {
		many = append(many, runtimeCodexClaim{Home: fmt.Sprintf("/h%d", i)})
	}
	if got := cleanCodexClaims(many); len(got) != maxCodexClaims || got[0].Home != "/h0" {
		t.Fatalf("got %d claims, first %+v", len(got), got[0])
	}
	if cleanCodexClaims([]runtimeCodexClaim{{}}) != nil {
		t.Fatal("claims that name nothing were kept")
	}
}
