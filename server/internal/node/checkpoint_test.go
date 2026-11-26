package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func checkpointRefsNow(t *testing.T, r *reviewRepo, session string) []checkpointRef {
	t.Helper()
	refs, err := checkpointRefs(context.Background(), r.dir, session)
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func checkpointTempNames(t *testing.T, r *reviewRepo) []string {
	t.Helper()
	dir := strings.TrimSpace(r.git("rev-parse", "--absolute-git-dir"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "persona-checkpoint-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func worktreeDigest(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\\x00%s\\x00%d\\x00", filepath.ToSlash(rel), info.Mode(), info.Size())
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestCheckpointCaptureIsReadOnlyAndSnapshotsTheWorkingTree(t *testing.T) {
	r := newReviewRepo(t, map[string]string{
		"a.txt":      "one\n",
		".gitignore": "ignored.txt\n",
	})
	r.write("a.txt", "one\ntwo\n")
	r.git("add", "a.txt")
	r.write("a.txt", "one\ntwo\nthree\n")
	r.write("new.txt", "new\n")
	r.write("ignored.txt", "ignored\n")

	gitDir := strings.TrimSpace(r.git("rev-parse", "--absolute-git-dir"))
	indexPath := filepath.Join(gitDir, "index")
	beforeIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeIndexInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeHead := r.git("rev-parse", "HEAD")
	beforeRefs := r.git("for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags", "refs/remotes", "refs/stash")
	beforeStash := r.git("stash", "list")
	beforeStatus := r.git("status", "--porcelain=v2", "-z", "--untracked-files=all")
	beforeWorktree := worktreeDigest(t, r.dir)

	res, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Turn != 1 || res.Hash == "" {
		t.Fatalf("capture: %+v", res)
	}
	afterIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	afterIndexInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeIndex, afterIndex) || !beforeIndexInfo.ModTime().Equal(afterIndexInfo.ModTime()) {
		t.Fatal("the capture changed the user's index")
	}
	if got := r.git("rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("HEAD changed: %s -> %s", beforeHead, got)
	}
	if got := r.git("for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags", "refs/remotes", "refs/stash"); got != beforeRefs {
		t.Fatalf("user refs changed:\n%s\n---\n%s", beforeRefs, got)
	}
	if got := r.git("stash", "list"); got != beforeStash {
		t.Fatalf("stash changed: %q -> %q", beforeStash, got)
	}
	if got := r.git("status", "--porcelain=v2", "-z", "--untracked-files=all"); got != beforeStatus {
		t.Fatal("the capture changed the working-tree status")
	}
	if got := worktreeDigest(t, r.dir); got != beforeWorktree {
		t.Fatal("the capture changed the working tree")
	}
	if got := r.git("show", res.Ref+":a.txt"); got != "one\ntwo\nthree" {
		t.Fatalf("snapshot a.txt = %q", got)
	}
	if got := r.git("show", res.Ref+":new.txt"); got != "new" {
		t.Fatalf("snapshot new.txt = %q", got)
	}
	if tree := r.git("ls-tree", "-r", "--name-only", res.Ref); strings.Contains(tree, "ignored.txt") {
		t.Fatalf("ignored file was captured:\n%s", tree)
	}
	if temps := checkpointTempNames(t, r); len(temps) != 0 {
		t.Fatalf("temporary checkpoint files were left: %v", temps)
	}
}

func TestCheckpointTurnReviewFindsShellChangesAndLiveWork(t *testing.T) {
	r := newReviewRepo(t, map[string]string{"a.txt": "one\n"})
	first, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "printf 'shell\\n' > shell.txt; printf 'two\\n' >> a.txt")
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shell edit: %v: %s", err, out)
	}
	second, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: r.dir, Scope: proto.ReviewTurn, Session: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Turn != 2 || res.Base.Hash != first.Hash || res.Target == nil || res.Target.Hash != second.Hash {
		t.Fatalf("turn review chose %+v", res)
	}
	fileOf(t, res, "a.txt")
	fileOf(t, res, "shell.txt")

	r.write("live.txt", "live\n")
	live, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: r.dir, Scope: proto.ReviewTurn, Session: "session-1", Live: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if live.Target == nil || !live.Target.Working || live.Base.Hash != second.Hash {
		t.Fatalf("live turn review chose %+v", live)
	}
	fileOf(t, live, "live.txt")

	pinned, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: r.dir, Scope: proto.ReviewTurn, Session: "session-1",
		Base: res.Base.Hash, Target: res.Target.Hash, Paths: []string{"shell.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned.Files) != 1 || pinned.Files[0].Path != "shell.txt" {
		t.Fatalf("pinned turn review: %+v", pathsOf(pinned))
	}
}

func TestCheckpointFirstTurnComparesWithHead(t *testing.T) {
	r := newReviewRepo(t, map[string]string{"a.txt": "one\n"})
	parent := r.git("rev-parse", "HEAD")
	r.write("a.txt", "turn change\n")
	res, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	r.write("a.txt", "a later user commit\n")
	r.git("add", "a.txt")
	r.git("commit", "-q", "-m", "later")
	got, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: r.dir, Scope: proto.ReviewTurn, Session: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Turn != 1 || got.Base.Hash != parent || got.Target == nil || got.Target.Hash != res.Hash {
		t.Fatalf("first turn review chose %+v", got)
	}
	pinned, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: r.dir, Scope: proto.ReviewTurn, Session: "session-1",
		Base: got.Base.Hash, Target: got.Target.Hash, Paths: []string{"a.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned.Files) != 1 || pinned.Files[0].Path != "a.txt" {
		t.Fatalf("pinned first turn review: %+v", pathsOf(pinned))
	}
}

func TestCheckpointFirstTurnBeforeAnyCommit(t *testing.T) {
	home := reviewGitEnv(t)
	dir := t.TempDir()
	runPullGit(t, dir, "init", "-q", "--initial-branch=main")
	r := &reviewRepo{t: t, node: &Node{home: home, state: newState()}, dir: dir}
	r.write("new.txt", "new\n")
	res, err := r.node.captureCheckpoint(context.Background(), dir, "session-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: dir, Scope: proto.ReviewTurn, Session: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Base.Empty || got.Target == nil || got.Target.Hash != res.Hash || got.Head != nil {
		t.Fatalf("unborn turn review: %+v", got)
	}
	fileOf(t, got, "new.txt")
	pinned, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: dir, Scope: proto.ReviewTurn, Session: "session-1", Target: res.Hash, Paths: []string{"new.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned.Files) != 1 || pinned.Files[0].Path != "new.txt" {
		t.Fatalf("pinned unborn turn review: %+v", pathsOf(pinned))
	}
}

func TestCheckpointReviewRefusals(t *testing.T) {
	r := newReviewRepo(t, map[string]string{"a.txt": "one\n"})
	_, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{Cwd: r.dir, Scope: proto.ReviewTurn, Session: "session-1"})
	if e, ok := err.(*rpc.Error); !ok || e.Code != proto.CodeNoCheckpoint {
		t.Fatalf("no checkpoint: %v", err)
	}
	if _, err := r.node.gitReview(context.Background(), proto.GitReviewRequest{Cwd: r.dir, Scope: proto.ReviewTurn}); err == nil {
		t.Fatal("turn without a session was accepted")
	}
	one, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	r.write("b.txt", "other\n")
	two, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-2", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.node.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: r.dir, Scope: proto.ReviewTurn, Session: "session-1", Base: one.Hash, Target: two.Hash,
	})
	if e, ok := err.(*rpc.Error); !ok || e.Code != proto.CodeBadBase {
		t.Fatalf("another session's checkpoint: %v", err)
	}
}

func TestCheckpointLimitsSkipAndLeaveNothing(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*reviewRepo)
		limit checkpointLimits
		want  string
	}{
		{
			name:  "tracked files",
			setup: func(r *reviewRepo) { r.write("b.txt", "b\n"); r.git("add", "b.txt") },
			limit: checkpointLimits{maxTracked: 1},
			want:  "too many tracked files",
		},
		{
			name:  "repository too large",
			setup: func(r *reviewRepo) { r.write("b.txt", "b\n"); r.git("add", "b.txt") },
			limit: checkpointLimits{disableAbove: 1, maxTracked: 100},
			want:  "repository too large",
		},
		{
			name:  "snapshot bytes",
			setup: func(r *reviewRepo) { r.write("a.txt", "0123456789\n") },
			limit: checkpointLimits{maxSnapshotBytes: 1},
			want:  "snapshot is too large",
		},
		{
			name:  "changed bytes",
			setup: func(r *reviewRepo) { r.write("a.txt", "0123456789\n") },
			limit: checkpointLimits{maxChangedBytes: 1},
			want:  "changed content is too large",
		},
		{
			name:  "timeout",
			setup: func(r *reviewRepo) { r.write("a.txt", "changed\n") },
			limit: checkpointLimits{timeout: time.Nanosecond},
			want:  "timed out",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newReviewRepo(t, map[string]string{"a.txt": "one\n"})
			tc.setup(r)
			r.node.checkpoints.limits = tc.limit
			if _, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-1", 1); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("capture error = %v, want %q", err, tc.want)
			}
			if refs := checkpointRefsNow(t, r, "session-1"); len(refs) != 0 {
				t.Fatalf("a skipped capture left refs: %+v", refs)
			}
			if temps := checkpointTempNames(t, r); len(temps) != 0 {
				t.Fatalf("a skipped capture left temporary files: %v", temps)
			}
		})
	}
}

func TestCheckpointPruneKeepsRefsReachableAcrossGC(t *testing.T) {
	r := newReviewRepo(t, map[string]string{"a.txt": "start\n"})
	r.node.checkpoints.limits = checkpointLimits{keep: 3}
	var refs []checkpointRef
	for turn := int64(1); turn <= 4; turn++ {
		r.write("a.txt", fmt.Sprintf("turn %d\n", turn))
		if _, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-1", turn); err != nil {
			t.Fatal(err)
		}
	}
	refs = checkpointRefsNow(t, r, "session-1")
	if len(refs) != 3 || refs[0].Turn != 2 || refs[2].Turn != 4 {
		t.Fatalf("refs after prune: %+v", refs)
	}
	r.git("gc", "--prune=now", "-q")
	if got := r.git("rev-parse", refs[2].Ref); got != refs[2].Hash {
		t.Fatalf("kept ref after gc: %s, want %s", got, refs[2].Hash)
	}
}

func TestCheckpointEndIsCapturedOffTheCallingPath(t *testing.T) {
	r := newReviewRepo(t, map[string]string{"a.txt": "one\n"})
	r.write("a.txt", "working tree\n")
	start := time.Now()
	r.node.noteCheckpointEnds([]proto.Session{{SessionID: "session-1", Cwd: r.dir, Status: proto.StatusWorking}})
	r.node.noteCheckpointEnds([]proto.Session{{SessionID: "session-1", Cwd: r.dir, Status: proto.StatusDone}})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("turn-end handling blocked for %s", elapsed)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(checkpointRefsNow(t, r, "session-1")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	refs := checkpointRefsNow(t, r, "session-1")
	if len(refs) != 1 {
		t.Fatalf("turn-end refs: %+v", refs)
	}
	r.node.noteCheckpointEnds([]proto.Session{{SessionID: "session-1", Cwd: r.dir, Status: proto.StatusDone}})
	if len(checkpointRefsNow(t, r, "session-1")) != 1 {
		t.Fatal("the same turn was captured twice")
	}
}

func TestCheckpointRefsSurviveANewNode(t *testing.T) {
	r := newReviewRepo(t, map[string]string{"a.txt": "one\n"})
	first, err := r.node.captureCheckpoint(context.Background(), r.dir, "session-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &Node{home: r.node.home, state: newState()}
	r.write("a.txt", "after restart\n")
	second, err := restarted.captureCheckpoint(context.Background(), r.dir, "session-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if second.Turn != 2 {
		t.Fatalf("turn after restart = %d, want 2", second.Turn)
	}
	got, err := restarted.gitReview(context.Background(), proto.GitReviewRequest{
		Cwd: r.dir, Scope: proto.ReviewTurn, Session: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Base.Hash != first.Hash || got.Target == nil || got.Target.Hash != second.Hash {
		t.Fatalf("restarted turn review: %+v", got)
	}
}
