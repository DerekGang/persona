package extmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const extensionTargetToken = "extension-target-test-token"

// targetFakeHub answers the CLI's preview and write calls without touching a real hub or packages. It counts every
// authenticated request so a refused target can prove that nothing was sent.
func targetFakeHub(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+extensionTargetToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/extensions/-/preview":
			_, _ = io.WriteString(w, `{"preview_id":"p1","permissions_digest":"d1","manifest":{"id":"example","name":"Example","version":"1.0.0"}}`)
		case r.URL.Path == "/api/v1/extensions/-/install", r.URL.Path == "/api/v1/extensions/example/-/upgrade":
			_, _ = io.WriteString(w, `{"id":"example","version":"1.0.0","status":"running"}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/-/") &&
			strings.HasPrefix(r.URL.Path, "/api/v1/extensions/"):
			_, _ = io.WriteString(w, `{"id":"example","config":{}}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

func writeTargetHub(t *testing.T, home, listen string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]string{"listen": listen, "token": extensionTargetToken})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hub.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runTargetCommand(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := ExtensionCommand(context.Background(), args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), err
}

func requireTarget(t *testing.T, out, home, hub string) {
	t.Helper()
	want := "PERSONA_HOME: " + home + "\nHub: " + hub + "\n"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("target prefix:\n%s\nwant:\n%s", out, want)
	}
	if strings.Contains(out, extensionTargetToken) {
		t.Fatalf("target output exposed the hub token:\n%s", out)
	}
}

func TestExtensionTargetInheritedHome(t *testing.T) {
	t.Setenv("PERSONA_LANG", "en")
	t.Setenv("PERSONA_LISTEN", "")
	srv, requests := targetFakeHub(t)
	home := t.TempDir()
	writeTargetHub(t, home, strings.TrimPrefix(srv.URL, "http://"))
	otherHome := t.TempDir()
	t.Setenv("HOME", otherHome)
	writeTargetHub(t, filepath.Join(otherHome, ".persona"), strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("PERSONA_HOME", home)

	out, err := runTargetCommand(t, "", "install", "dir:/tmp/example", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	requireTarget(t, out, home, srv.URL)
	if strings.Contains(out, "Continue?") {
		t.Fatalf("--yes asked for confirmation:\n%s", out)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want preview + install", requests.Load())
	}
}

func TestExtensionTargetHomeOnly(t *testing.T) {
	t.Setenv("PERSONA_LANG", "en")
	t.Setenv("PERSONA_LISTEN", "")
	t.Setenv("PERSONA_HOME", "")
	t.Setenv("JARVIS_HOME", "")
	srv, _ := targetFakeHub(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	state := filepath.Join(home, ".persona")
	writeTargetHub(t, state, strings.TrimPrefix(srv.URL, "http://"))

	out, err := runTargetCommand(t, "", "install", "dir:/tmp/example", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	requireTarget(t, out, state, srv.URL)
}

func TestExtensionTargetLegacyHomeName(t *testing.T) {
	t.Setenv("PERSONA_LANG", "en")
	t.Setenv("PERSONA_LISTEN", "")
	t.Setenv("PERSONA_HOME", "")
	if err := os.Unsetenv("PERSONA_HOME"); err != nil {
		t.Fatal(err)
	}
	srv, _ := targetFakeHub(t)
	home := t.TempDir()
	t.Setenv("JARVIS_HOME", home)
	writeTargetHub(t, home, strings.TrimPrefix(srv.URL, "http://"))

	out, err := runTargetCommand(t, "", "install", "dir:/tmp/example", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	requireTarget(t, out, home, srv.URL)
}

func TestExtensionTargetMatchingFlags(t *testing.T) {
	t.Setenv("PERSONA_LANG", "en")
	t.Setenv("PERSONA_LISTEN", "")
	srv, requests := targetFakeHub(t)
	home := t.TempDir()
	writeTargetHub(t, home, strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("PERSONA_HOME", home)
	hub := strings.TrimPrefix(srv.URL, "http://")

	out, err := runTargetCommand(t, "", "install", "dir:/tmp/example", "--yes",
		"--expect-home", home, "--expect-hub="+hub)
	if err != nil {
		t.Fatal(err)
	}
	requireTarget(t, out, home, srv.URL)
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want preview + install", requests.Load())
	}
}

func TestExtensionTargetMismatchStopsBeforeHub(t *testing.T) {
	t.Setenv("PERSONA_LANG", "en")
	t.Setenv("PERSONA_LISTEN", "")
	srv, requests := targetFakeHub(t)
	home := t.TempDir()
	writeTargetHub(t, home, strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("PERSONA_HOME", home)
	other := t.TempDir()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"home", []string{"install", "dir:/tmp/example", "--yes", "--expect-home", other}, other},
		{"hub", []string{"install", "dir:/tmp/example", "--yes", "--expect-hub", "127.0.0.1:1"}, "127.0.0.1:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests.Store(0)
			before, err := os.ReadDir(home)
			if err != nil {
				t.Fatal(err)
			}
			out, err := runTargetCommand(t, "", tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
			if !strings.Contains(out, "PERSONA_HOME: "+home) || !strings.Contains(out, "Hub: "+srv.URL) {
				t.Fatalf("target line missing:\n%s", out)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("requested the hub %d times after target mismatch", got)
			}
			after, err := os.ReadDir(home)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("target mismatch changed %s", home)
			}
		})
	}
}

func TestExtensionTargetFlagsOnWrites(t *testing.T) {
	t.Setenv("PERSONA_LANG", "en")
	t.Setenv("PERSONA_LISTEN", "")
	srv, _ := targetFakeHub(t)
	home := t.TempDir()
	writeTargetHub(t, home, strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("PERSONA_HOME", home)
	hub := strings.TrimPrefix(srv.URL, "http://")
	flags := []string{"--expect-home=" + home, "--expect-hub", hub}

	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"install", "", []string{"install", "dir:/tmp/example", "--yes"}},
		{"upgrade", "", []string{"upgrade", "example", "dir:/tmp/example", "--yes"}},
		{"enable", "", []string{"enable", "example"}},
		{"disable", "", []string{"disable", "example"}},
		{"config", "", []string{"config", "example", "a=1"}},
		{"secret", "s3cret\n", []string{"secret", "example", "api_key"}},
		{"uninstall", "", []string{"uninstall", "example"}},
		{"market add", "", []string{"market", "add", "github:example/plugins"}},
		{"market set-spec", "", []string{"market", "set-spec", "example", "github:example/moved"}},
		{"market remove", "", []string{"market", "remove", "example"}},
		{"market refresh", "", []string{"market", "refresh", "example"}},
		{"market token", "", []string{"market", "token", "example", "--clear"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runTargetCommand(t, tc.stdin, append(tc.args, flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			requireTarget(t, out, home, srv.URL)
		})
	}
}
