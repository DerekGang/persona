package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func testStorageDeps(t *testing.T) (storageDeps, string, string) {
	t.Helper()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".persona")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	pct := 45.0
	deps := storageDeps{
		goos:    "linux",
		home:    home,
		dataDir: dataDir,
		getenv:  func(string) string { return "" },
		now:     func() time.Time { return now },
		mounts: func() ([]proto.MountStat, bool) {
			return []proto.MountStat{
				{Mount: "/", Device: "/dev/vda2", FSType: "ext4", UsedBytes: 50 << 30, AvailBytes: 50 << 30, TotalBytes: 100 << 30, UsedPercent: 50, InodesTotal: 1000, InodesUsed: 450, InodesFree: 550, InodesPercent: &pct},
			}, true
		},
		available: func(string) (int64, bool) { return 50 << 30, true },
		lookPath:  func(string) (string, error) { return "", errors.New("not found") },
		runCmd:    func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("not called") },
		runningProcs: func() map[string]bool {
			return map[string]bool{}
		},
	}
	return deps, home, dataDir
}

func writeStorageFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectMachineStorage(t *testing.T) {
	deps, _, _ := testStorageDeps(t)
	rep := collectMachineStorage(deps)
	if !rep.Supported || rep.OS != "linux" || len(rep.Mounts) != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if rep.Mounts[0].Mount != "/" || rep.Mounts[0].InodesPercent == nil || *rep.Mounts[0].InodesPercent != 45 {
		t.Fatalf("unexpected mount: %+v", rep.Mounts[0])
	}
}

func TestCollectMachineStorageHotspotsCoversCategories(t *testing.T) {
	deps, home, dataDir := testStorageDeps(t)
	writeStorageFile(t, filepath.Join(home, ".cache", "go-build", "a.obj"), 2048)
	writeStorageFile(t, filepath.Join(home, ".cache", "pip", "wheel.whl"), 1024)
	writeStorageFile(t, filepath.Join(home, "go", "pkg", "mod", "cache.zip"), 4096)
	writeStorageFile(t, filepath.Join(home, ".npm", "_cacache", "tar.tgz"), 1536)
	writeStorageFile(t, filepath.Join(dataDir, "logs", "node.log"), 512)
	writeStorageFile(t, filepath.Join(dataDir, "backups", "snap-1.tar"), 3072)

	// Add a stale broken worktree under ~/.herdr/worktrees/repo/old-branch
	wtDir := filepath.Join(home, ".herdr", "worktrees", "repo", "old-branch")
	writeStorageFile(t, filepath.Join(wtDir, "payload.bin"), 2560)
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: /nonexistent/repo/.git/worktrees/old-branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mock docker system df --format json
	deps.lookPath = func(bin string) (string, error) {
		if bin == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", errors.New("missing")
	}
	deps.runCmd = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "docker" && len(args) >= 2 && args[0] == "system" && args[1] == "df" {
			return []byte(strings.Join([]string{
				`{"Type":"Images","TotalCount":"3","Active":"1","Size":"2GB","Reclaimable":"1GB (50%)"}`,
				`{"Type":"Build Cache","TotalCount":"10","Active":"0","Size":"500MB","Reclaimable":"500MB"}`,
			}, "\n")), nil
		}
		return nil, errors.New("unexpected cmd")
	}

	rep := collectMachineStorageHotspots(context.Background(), deps, proto.StorageHotspotsParams{TimeoutMS: 5000, MaxDepth: 4})
	if rep.Partial {
		t.Fatalf("expected non-partial scan, got partial")
	}
	byID := map[string]proto.StorageHotspotCategory{}
	for _, c := range rep.Categories {
		byID[c.ID] = c
	}
	if byID[proto.StorageCategoryGoBuild].Bytes != 2048 || !byID[proto.StorageCategoryGoBuild].Cleanable {
		t.Errorf("go-build = %+v", byID[proto.StorageCategoryGoBuild])
	}
	if byID[proto.StorageCategoryGoMod].Bytes != 4096 || !byID[proto.StorageCategoryGoMod].Cleanable {
		t.Errorf("go-mod = %+v", byID[proto.StorageCategoryGoMod])
	}
	if byID[proto.StorageCategoryNpmCache].Bytes != 1536 {
		t.Errorf("npm-cache = %+v", byID[proto.StorageCategoryNpmCache])
	}
	if byID[proto.StorageCategoryPipCache].Bytes != 1024 {
		t.Errorf("pip-cache = %+v", byID[proto.StorageCategoryPipCache])
	}
	if byID[proto.StorageCategoryPersonaLogs].Bytes != 512 || !byID[proto.StorageCategoryPersonaLogs].Cleanable {
		t.Errorf("persona-logs = %+v", byID[proto.StorageCategoryPersonaLogs])
	}
	if byID[proto.StorageCategoryPersonaBackups].Bytes != 3072 || !byID[proto.StorageCategoryPersonaBackups].Cleanable {
		t.Errorf("persona-backups = %+v", byID[proto.StorageCategoryPersonaBackups])
	}
	if byID[proto.StorageCategoryUserCache].Cleanable {
		t.Errorf("user-cache must be advisory (cleanable=false)")
	}
	if byID[proto.StorageCategoryDocker].Bytes != 2_500_000_000 || !byID[proto.StorageCategoryDocker].Cleanable || byID[proto.StorageCategoryDocker].CleanupTargetID != proto.StorageTargetDockerBuildCache {
		t.Errorf("docker = %+v", byID[proto.StorageCategoryDocker])
	}
	wt := byID[proto.StorageCategoryGitWorktrees]
	if wt.ItemCount != 1 || wt.CleanupTargetID != proto.StorageTargetGitWorktreesPrune || !wt.Items[0].Stale {
		t.Errorf("git-worktrees = %+v", wt)
	}
}

func TestMachineStorageCleanupValidationAndPreviewAndExecute(t *testing.T) {
	deps, home, dataDir := testStorageDeps(t)
	writeStorageFile(t, filepath.Join(home, ".cache", "pip", "a.whl"), 2048)
	writeStorageFile(t, filepath.Join(dataDir, "logs", "old.log"), 1024)

	// Reject empty targets
	if _, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{DryRun: true}); err == nil {
		t.Fatal("expected error for empty targets")
	}
	// Reject arbitrary path / non-allowlisted target
	if _, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{Targets: []string{"/etc"}, Confirm: true}); err == nil {
		t.Fatal("expected error for unknown target")
	} else {
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != storageCodeBadRequest {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// Reject unconfirmed live cleanup
	if _, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{Targets: []string{proto.StorageTargetPipCache}}); err == nil {
		t.Fatal("expected confirmation_required error")
	} else {
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != storageCodeConfirmNeeded {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Dry-run preview must NOT delete files
	prev, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{proto.StorageTargetPipCache, proto.StorageTargetPersonaLogs},
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if !prev.DryRun || prev.EstimatedBytes != 3072 || prev.FreedBytes != 0 || len(prev.Items) != 2 {
		t.Fatalf("unexpected preview result: %+v", prev)
	}
	if _, err := os.Stat(filepath.Join(home, ".cache", "pip", "a.whl")); err != nil {
		t.Fatalf("dry run deleted pip cache file: %v", err)
	}

	// Confirmed execution deletes contents while keeping parent directories
	res, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{proto.StorageTargetPipCache, proto.StorageTargetPersonaLogs},
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if res.DryRun || res.FreedBytes != 3072 {
		t.Fatalf("unexpected cleanup result: %+v", res)
	}
	for _, it := range res.Items {
		if it.Status != proto.StorageCleanupCleaned {
			t.Errorf("item %+v not cleaned", it)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".cache", "pip", "a.whl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pip file still exists after cleanup")
	}
	if st, err := os.Stat(filepath.Join(home, ".cache", "pip")); err != nil || !st.IsDir() {
		t.Fatalf("pip dir itself should be preserved: %v", err)
	}
}

func TestMachineStorageCleanupRefusesSymlinkEscapeAndRespectsInUse(t *testing.T) {
	deps, home, _ := testStorageDeps(t)
	outside := t.TempDir()
	writeStorageFile(t, filepath.Join(outside, "secret.txt"), 1024)
	if err := os.MkdirAll(filepath.Join(home, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".cache", "pip")); err != nil {
		t.Fatal(err)
	}
	writeStorageFile(t, filepath.Join(home, ".cache", "go-build", "build.o"), 1024)
	deps.runningProcs = func() map[string]bool { return map[string]bool{"go": true} }

	res, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{proto.StorageTargetPipCache, proto.StorageTargetGoBuild},
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("cleanup returned err: %v", err)
	}
	if res.Items[0].Status != proto.StorageCleanupFailed {
		t.Errorf("symlink target status = %q, want failed", res.Items[0].Status)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatalf("symlink escape removed outside file!")
	}
	if res.Items[1].Status != proto.StorageCleanupInUse {
		t.Errorf("in-use target status = %q, want in_use", res.Items[1].Status)
	}
}

func TestParseHumanBytes(t *testing.T) {
	cases := map[string]int64{
		"0B":            0,
		"512kB":         512_000,
		"12.5MB (100%)": 12_500_000,
		"2GB":           2_000_000_000,
		"1.5GiB (50%)":  1610612736,
		"bad":           0,
	}
	for in, want := range cases {
		if got := parseHumanBytes(in); got != want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNodeHandleMachineStorageDispatch(t *testing.T) {
	n := &Node{home: t.TempDir()}
	out, err := n.handle(context.Background(), proto.MethodMachineStorage, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("machine.storage failed: %v", err)
	}
	if _, ok := out.(proto.MachineStorageReport); !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if _, err := n.handle(context.Background(), proto.MethodMachineStorageCleanup, json.RawMessage(`{"targets":["/"]}`)); err == nil {
		t.Fatal("expected bad_request for non-allowlisted target via handle")
	}
}
