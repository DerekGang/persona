package node

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/sysmon"
)

const (
	storageCodeBadRequest    = "bad_request"
	storageCodeConfirmNeeded = "confirmation_required"
	storageDefaultTimeout    = 10 * time.Second
	storageMaxTimeout        = 20 * time.Second
	storageMinTimeout        = 500 * time.Millisecond
	storageDefaultMaxDepth   = 4
	storageMaxDepthLimit     = 8
	storageMaxWalkEntries    = 40_000
	storageDockerCmdTimeout  = 5 * time.Second
	storageGitCmdTimeout     = 5 * time.Second
	storageStaleWorktreeAge  = 7 * 24 * time.Hour
	storageMaxCategoryItems  = 12
	storageMaxWorktreeScans  = 48
)

var errStorageOutsideRoot = errors.New("path is outside allowed root")

type storageDeps struct {
	goos         string
	home         string
	dataDir      string
	getenv       func(string) string
	now          func() time.Time
	mounts       func() ([]proto.MountStat, bool)
	available    func(string) (int64, bool)
	runCmd       func(ctx context.Context, name string, args ...string) ([]byte, error)
	lookPath     func(string) (string, error)
	runningProcs func() map[string]bool
}

func (n *Node) defaultStorageDeps() storageDeps {
	home := n.home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	dir := n.dir
	if dir == "" && home != "" {
		dir = filepath.Join(home, ".persona")
	}
	return storageDeps{
		goos:      runtime.GOOS,
		home:      home,
		dataDir:   dir,
		getenv:    os.Getenv,
		now:       time.Now,
		mounts:    sysmon.StorageMounts,
		available: sysmon.AvailableBytes,
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
		lookPath:     exec.LookPath,
		runningProcs: readRunningProcNames,
	}
}

// machineStorage handles machine.storage: fast read-only per-mount capacity and inode stats (statfs only, no du).
func (n *Node) machineStorage(ctx context.Context, _ json.RawMessage) (any, error) {
	return collectMachineStorage(n.defaultStorageDeps()), nil
}

// machineStorageHotspots handles machine.storage.hotspots: explicit on-demand bounded hotspot storage scan.
func (n *Node) machineStorageHotspots(ctx context.Context, params json.RawMessage) (any, error) {
	var p proto.StorageHotspotsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: storageCodeBadRequest, Message: err.Error()}
		}
	}
	return collectMachineStorageHotspots(ctx, n.defaultStorageDeps(), p), nil
}

// machineStorageCleanup handles machine.storage.cleanup: preview (dry_run) or confirmation-gated selective cleanup.
func (n *Node) machineStorageCleanup(ctx context.Context, params json.RawMessage) (any, error) {
	var p proto.StorageCleanupParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: storageCodeBadRequest, Message: err.Error()}
	}
	return runMachineStorageCleanup(ctx, n.defaultStorageDeps(), p)
}

func collectMachineStorage(deps storageDeps) proto.MachineStorageReport {
	start := deps.now()
	mounts, supported := deps.mounts()
	if mounts == nil {
		mounts = []proto.MountStat{}
	}
	return proto.MachineStorageReport{
		At:          start.Unix(),
		OS:          deps.goos,
		Supported:   supported,
		Mounts:      mounts,
		CollectedMS: max(deps.now().Sub(start).Milliseconds(), 0),
	}
}

func clampStorageHotspotsLimits(p proto.StorageHotspotsParams) (time.Duration, int) {
	timeout := storageDefaultTimeout
	if p.TimeoutMS > 0 {
		timeout = time.Duration(p.TimeoutMS) * time.Millisecond
		if timeout < storageMinTimeout {
			timeout = storageMinTimeout
		}
		if timeout > storageMaxTimeout {
			timeout = storageMaxTimeout
		}
	}
	maxDepth := storageDefaultMaxDepth
	if p.MaxDepth > 0 {
		maxDepth = min(p.MaxDepth, storageMaxDepthLimit)
	}
	return timeout, maxDepth
}

func collectMachineStorageHotspots(ctx context.Context, deps storageDeps, params proto.StorageHotspotsParams) proto.MachineStorageHotspotsReport {
	start := deps.now()
	timeout, maxDepth := clampStorageHotspotsLimits(params)
	scanCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	running := deps.runningProcs()
	partial := false

	categories := make([]proto.StorageHotspotCategory, 0, 9)

	// 1. Stale Git worktrees
	wtCat, wtPartial := scanGitWorktreesHotspot(scanCtx, deps, maxDepth)
	partial = partial || wtPartial
	categories = append(categories, wtCat)

	// 2. Docker system df
	dockerCat := scanDockerHotspot(scanCtx, deps)
	categories = append(categories, dockerCat)

	// 3. ~/.cache (or ~/Library/Caches)
	ucCat, ucPartial := scanUserCacheHotspot(scanCtx, deps, maxDepth)
	partial = partial || ucPartial
	categories = append(categories, ucCat)

	// 4. Toolchain caches (Go build, Go mod, npm, pip)
	for _, spec := range storageDirTargets(deps) {
		cat, p := scanDirTargetHotspot(scanCtx, deps, spec, maxDepth, running)
		partial = partial || p
		categories = append(categories, cat)
	}

	slices.SortFunc(categories, func(a, b proto.StorageHotspotCategory) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.ID, b.ID))
	})

	return proto.MachineStorageHotspotsReport{
		At:         start.Unix(),
		OS:         deps.goos,
		Partial:    partial || scanCtx.Err() != nil,
		DurationMS: max(deps.now().Sub(start).Milliseconds(), 0),
		Categories: categories,
	}
}

type storageDirTarget struct {
	categoryID string
	targetID   string
	roots      []string // allowed parent roots for guard check
	paths      []string // one or more target directories
	users      []string // toolchain executables that mark the cache in_use
	action     string
}

func storageDirTargets(deps storageDeps) []storageDirTarget {
	home := deps.home
	dataDir := deps.dataDir
	env := deps.getenv
	cacheBase := storageUserCacheDir(deps.goos, home, env)

	goBuild := env("GOCACHE")
	if goBuild == "" && cacheBase != "" {
		goBuild = filepath.Join(cacheBase, "go-build")
	}
	goMod := env("GOMODCACHE")
	if goMod == "" {
		if gopath := env("GOPATH"); gopath != "" {
			goMod = filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
		} else if home != "" {
			goMod = filepath.Join(home, "go", "pkg", "mod")
		}
	}
	npmCache := ""
	if v := env("npm_config_cache"); v != "" {
		npmCache = filepath.Join(v, "_cacache")
	} else if home != "" {
		npmCache = filepath.Join(home, ".npm", "_cacache")
	}
	pipCache := env("PIP_CACHE_DIR")
	if pipCache == "" && cacheBase != "" {
		pipCache = filepath.Join(cacheBase, "pip")
	}

	var logPaths, backupPaths []string
	var personaRoots []string
	if dataDir != "" {
		personaRoots = append(personaRoots, dataDir)
		logPaths = append(logPaths, filepath.Join(dataDir, "logs"))
		backupPaths = append(backupPaths,
			filepath.Join(dataDir, "backups"),
			filepath.Join(dataDir, "update", "development-backups"),
		)
	}
	if home != "" {
		personaRoots = append(personaRoots, home)
	}

	homeRoots := []string{}
	if home != "" {
		homeRoots = append(homeRoots, home)
	}

	return []storageDirTarget{
		{
			categoryID: proto.StorageCategoryGoBuild,
			targetID:   proto.StorageTargetGoBuild,
			roots:      homeRoots,
			paths:      nonEmptyPaths(goBuild),
			users:      []string{"go", "gopls", "compile"},
			action:     "empty_dir_contents",
		},
		{
			categoryID: proto.StorageCategoryGoMod,
			targetID:   proto.StorageTargetGoMod,
			roots:      homeRoots,
			paths:      nonEmptyPaths(goMod),
			users:      []string{"go", "gopls"},
			action:     "empty_dir_contents",
		},
		{
			categoryID: proto.StorageCategoryNpmCache,
			targetID:   proto.StorageTargetNpmCache,
			roots:      homeRoots,
			paths:      nonEmptyPaths(npmCache),
			users:      []string{"npm", "npx", "node"},
			action:     "empty_dir_contents",
		},
		{
			categoryID: proto.StorageCategoryPipCache,
			targetID:   proto.StorageTargetPipCache,
			roots:      homeRoots,
			paths:      nonEmptyPaths(pipCache),
			users:      []string{"pip", "python"},
			action:     "empty_dir_contents",
		},
		{
			categoryID: proto.StorageCategoryPersonaLogs,
			targetID:   proto.StorageTargetPersonaLogs,
			roots:      personaRoots,
			paths:      logPaths,
			action:     "empty_dir_contents",
		},
		{
			categoryID: proto.StorageCategoryPersonaBackups,
			targetID:   proto.StorageTargetPersonaBackups,
			roots:      personaRoots,
			paths:      backupPaths,
			action:     "empty_dir_contents",
		},
	}
}

func nonEmptyPaths(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if p != "" && filepath.IsAbs(p) {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

func storageUserCacheDir(goos, home string, env func(string) string) string {
	if home == "" {
		return ""
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Caches")
	}
	if v := env("XDG_CACHE_HOME"); filepath.IsAbs(v) {
		return filepath.Clean(v)
	}
	return filepath.Join(home, ".cache")
}

func scanUserCacheHotspot(ctx context.Context, deps storageDeps, maxDepth int) (proto.StorageHotspotCategory, bool) {
	dir := storageUserCacheDir(deps.goos, deps.home, deps.getenv)
	cat := proto.StorageHotspotCategory{
		ID:        proto.StorageCategoryUserCache,
		Path:      storageTilde(deps.home, dir),
		Status:    proto.StorageStatusEmpty,
		Cleanable: false,
		Note:      "advisory",
	}
	if dir == "" {
		cat.Status = proto.StorageStatusUnavailable
		return cat, false
	}
	realDir, err := guardStorageDir(deps.home, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cat, false
		}
		cat.Status = proto.StorageStatusUnavailable
		return cat, false
	}
	entries, err := os.ReadDir(realDir)
	if err != nil {
		cat.Status = proto.StorageStatusUnavailable
		return cat, false
	}
	var total int64
	partial := false
	items := make([]proto.StorageHotspotItem, 0, len(entries))
	budget := storageMaxWalkEntries
	for _, e := range entries {
		if ctx.Err() != nil {
			partial = true
			break
		}
		childPath := filepath.Join(realDir, e.Name())
		sz, _, done := boundedDirSize(ctx, childPath, max(maxDepth-1, 1), &budget)
		if !done {
			partial = true
		}
		total += sz
		if sz > 0 {
			items = append(items, proto.StorageHotspotItem{
				Label: e.Name(),
				Path:  storageTilde(deps.home, filepath.Join(dir, e.Name())),
				Bytes: sz,
			})
		}
	}
	slices.SortFunc(items, func(a, b proto.StorageHotspotItem) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.Label, b.Label))
	})
	cat.ItemCount = len(items)
	if len(items) > storageMaxCategoryItems {
		items = items[:storageMaxCategoryItems]
	}
	cat.Items = items
	cat.Bytes = total
	if partial {
		cat.Status = proto.StorageStatusPartial
	} else if total > 0 {
		cat.Status = proto.StorageStatusOK
	}
	return cat, partial
}

func scanDirTargetHotspot(ctx context.Context, deps storageDeps, spec storageDirTarget, maxDepth int, running map[string]bool) (proto.StorageHotspotCategory, bool) {
	cat := proto.StorageHotspotCategory{
		ID:              spec.categoryID,
		Status:          proto.StorageStatusEmpty,
		Cleanable:       true,
		CleanupTargetID: spec.targetID,
		InUse:           storageToolchainInUse(spec.users, running),
	}
	if len(spec.paths) > 0 {
		cat.Path = storageTilde(deps.home, spec.paths[0])
	}
	var total int64
	var items []proto.StorageHotspotItem
	partial := false
	budget := storageMaxWalkEntries
	anyFound := false
	for _, p := range spec.paths {
		realDir, err := guardStorageDirAnyRoot(spec.roots, p)
		if err != nil {
			continue
		}
		anyFound = true
		sz, count, done := boundedDirSize(ctx, realDir, maxDepth, &budget)
		if !done {
			partial = true
		}
		total += sz
		cat.ItemCount += count
		if sz > 0 {
			items = append(items, proto.StorageHotspotItem{
				Label: filepath.Base(p),
				Path:  storageTilde(deps.home, p),
				Bytes: sz,
			})
		}
	}
	cat.Bytes = total
	cat.ReclaimableBytes = total
	if len(items) > 1 {
		cat.Items = items
	}
	if partial {
		cat.Status = proto.StorageStatusPartial
	} else if anyFound && total > 0 {
		cat.Status = proto.StorageStatusOK
	} else {
		cat.Status = proto.StorageStatusEmpty
		cat.Cleanable = false
	}
	return cat, partial
}

// scanGitWorktreesHotspot inspects Persona/herdr worktree roots for large or stale Git worktrees.
func scanGitWorktreesHotspot(ctx context.Context, deps storageDeps, maxDepth int) (proto.StorageHotspotCategory, bool) {
	roots := worktreeScanRoots(deps)
	cat := proto.StorageHotspotCategory{
		ID:     proto.StorageCategoryGitWorktrees,
		Status: proto.StorageStatusEmpty,
	}
	if len(roots) > 0 {
		cat.Path = storageTilde(deps.home, roots[0])
	}
	var items []proto.StorageHotspotItem
	var totalBytes int64
	var staleBytes int64
	prunableRepos := map[string]bool{}
	partial := false
	budget := storageMaxWalkEntries
	scanned := 0
	cutoff := deps.now().Add(-storageStaleWorktreeAge)

	for _, root := range roots {
		realRoot, err := guardStorageDirAnyRoot([]string{deps.home, deps.dataDir}, root)
		if err != nil {
			continue
		}
		candidates := discoverWorktreeDirs(realRoot, storageMaxWorktreeScans-scanned)
		for _, wt := range candidates {
			if ctx.Err() != nil {
				partial = true
				break
			}
			scanned++
			sz, _, done := boundedDirSize(ctx, wt.path, maxDepth, &budget)
			if !done {
				partial = true
			}
			totalBytes += sz
			stale := wt.broken || wt.modTime.Before(cutoff)
			if stale {
				staleBytes += sz
			}
			if wt.repoRoot != "" {
				prunableRepos[wt.repoRoot] = true
			}
			detail := "active"
			if wt.broken {
				detail = "broken"
			} else if stale {
				detail = "stale"
			}
			items = append(items, proto.StorageHotspotItem{
				Label:  wt.label,
				Path:   storageTilde(deps.home, wt.path),
				Bytes:  sz,
				Stale:  stale,
				Detail: detail,
			})
		}
	}

	slices.SortFunc(items, func(a, b proto.StorageHotspotItem) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.Path, b.Path))
	})
	cat.ItemCount = len(items)
	if len(items) > storageMaxCategoryItems {
		items = items[:storageMaxCategoryItems]
	}
	cat.Items = items
	cat.Bytes = totalBytes
	cat.ReclaimableBytes = staleBytes
	if len(prunableRepos) > 0 {
		cat.Cleanable = true
		cat.CleanupTargetID = proto.StorageTargetGitWorktreesPrune
		cat.Note = "prune_metadata_only"
	}
	if partial {
		cat.Status = proto.StorageStatusPartial
	} else if len(items) > 0 {
		cat.Status = proto.StorageStatusOK
	}
	return cat, partial
}

type discoveredWorktree struct {
	label    string
	path     string
	repoRoot string
	broken   bool
	modTime  time.Time
}

func worktreeScanRoots(deps storageDeps) []string {
	var candidates []string
	if deps.home != "" {
		candidates = append(candidates,
			filepath.Join(deps.home, ".herdr", "worktrees"),
			filepath.Join(deps.home, "worktrees"),
		)
	}
	if deps.dataDir != "" {
		candidates = append(candidates, filepath.Join(deps.dataDir, "worktrees"))
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range candidates {
		clean := filepath.Clean(c)
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out
}

// discoverWorktreeDirs looks 1 or 2 levels deep under a worktrees root (<root>/<wt> or <root>/<repo>/<wt>).
func discoverWorktreeDirs(root string, limit int) []discoveredWorktree {
	if limit <= 0 {
		return nil
	}
	topEntries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []discoveredWorktree
	inspect := func(dir, label string) bool {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return false
		}
		gitPath := filepath.Join(dir, ".git")
		gitInfo, err := os.Lstat(gitPath)
		if err != nil {
			return false
		}
		repoRoot, broken := parseWorktreeGitPointer(dir, gitPath, gitInfo)
		out = append(out, discoveredWorktree{
			label:    label,
			path:     dir,
			repoRoot: repoRoot,
			broken:   broken,
			modTime:  info.ModTime(),
		})
		return true
	}
	for _, e := range topEntries {
		if len(out) >= limit {
			break
		}
		if !e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		p := filepath.Join(root, e.Name())
		if inspect(p, e.Name()) {
			continue
		}
		subEntries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if len(out) >= limit {
				break
			}
			if !sub.IsDir() || sub.Type()&fs.ModeSymlink != 0 {
				continue
			}
			inspect(filepath.Join(p, sub.Name()), e.Name()+"/"+sub.Name())
		}
	}
	return out
}

func parseWorktreeGitPointer(wtDir, gitPath string, info fs.FileInfo) (repoRoot string, broken bool) {
	if info.IsDir() {
		return "", false
	}
	b, err := os.ReadFile(gitPath)
	if err != nil {
		return "", true
	}
	line := strings.TrimSpace(string(b))
	target, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", true
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Clean(filepath.Join(wtDir, target))
	}
	if _, err := os.Stat(target); err != nil {
		broken = true
	}
	// Resolve owning repo from .../.git/worktrees/<name>
	cleanTarget := filepath.ToSlash(filepath.Clean(target))
	if idx := strings.LastIndex(cleanTarget, "/.git/worktrees/"); idx > 0 {
		repoRoot = filepath.FromSlash(cleanTarget[:idx])
	}
	return repoRoot, broken
}

// scanDockerHotspot runs `docker system df --format json` with a strict timeout.
func scanDockerHotspot(ctx context.Context, deps storageDeps) proto.StorageHotspotCategory {
	cat := proto.StorageHotspotCategory{
		ID:     proto.StorageCategoryDocker,
		Status: proto.StorageStatusUnavailable,
	}
	if _, err := deps.lookPath("docker"); err != nil {
		cat.Note = "docker_not_installed"
		return cat
	}
	cmdCtx, cancel := context.WithTimeout(ctx, storageDockerCmdTimeout)
	defer cancel()
	out, err := deps.runCmd(cmdCtx, "docker", "system", "df", "--format", "json")
	if err != nil {
		cat.Note = "docker_unavailable"
		return cat
	}
	items, total, reclaimable, buildCacheReclaimable, ok := parseDockerSystemDF(out)
	if !ok {
		cat.Note = "docker_parse_failed"
		return cat
	}
	cat.Items = items
	cat.ItemCount = len(items)
	cat.Bytes = total
	cat.ReclaimableBytes = reclaimable
	if total > 0 {
		cat.Status = proto.StorageStatusOK
	} else {
		cat.Status = proto.StorageStatusEmpty
	}
	if buildCacheReclaimable > 0 {
		cat.Cleanable = true
		cat.CleanupTargetID = proto.StorageTargetDockerBuildCache
	}
	return cat
}

type dockerDFRow struct {
	Type        string `json:"Type"`
	TotalCount  string `json:"TotalCount"`
	Active      string `json:"Active"`
	Size        string `json:"Size"`
	Reclaimable string `json:"Reclaimable"`
}

func parseDockerSystemDF(raw []byte) (items []proto.StorageHotspotItem, total, reclaimable, buildCacheReclaimable int64, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var row dockerDFRow
		if err := json.Unmarshal([]byte(line), &row); err != nil || row.Type == "" {
			continue
		}
		ok = true
		sz := parseHumanBytes(row.Size)
		rec := parseHumanBytes(row.Reclaimable)
		total += sz
		reclaimable += rec
		if strings.EqualFold(row.Type, "Build Cache") {
			buildCacheReclaimable += rec
		}
		detail := ""
		if row.TotalCount != "" {
			detail = fmt.Sprintf("%s total, %s active", row.TotalCount, cmp.Or(row.Active, "0"))
		}
		items = append(items, proto.StorageHotspotItem{
			Label:            row.Type,
			Bytes:            sz,
			ReclaimableBytes: rec,
			Detail:           detail,
		})
	}
	return items, total, reclaimable, buildCacheReclaimable, ok
}

// parseHumanBytes parses Docker-style sizes like "1.23GB", "12.5MB (100%)", "512kB", "4.2GiB", "0B".
func parseHumanBytes(s string) int64 {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '('); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if s == "" || s == "0" || s == "0B" {
		return 0
	}
	i := 0
	for i < len(s) && ((s[i] >= '0' && s[i] <= '9') || s[i] == '.') {
		i++
	}
	if i == 0 {
		return 0
	}
	val, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val < 0 {
		return 0
	}
	unit := strings.TrimSpace(s[i:])
	var mult float64
	switch strings.ToLower(unit) {
	case "", "b":
		mult = 1
	case "kb":
		mult = 1e3
	case "mb":
		mult = 1e6
	case "gb":
		mult = 1e9
	case "tb":
		mult = 1e12
	case "kib":
		mult = 1 << 10
	case "mib":
		mult = 1 << 20
	case "gib":
		mult = 1 << 30
	case "tib":
		mult = 1 << 40
	default:
		return 0
	}
	return int64(math.Round(val * mult))
}

func runMachineStorageCleanup(ctx context.Context, deps storageDeps, params proto.StorageCleanupParams) (proto.StorageCleanupResult, error) {
	if len(params.Targets) == 0 {
		return proto.StorageCleanupResult{}, &rpc.Error{Code: storageCodeBadRequest, Message: "no cleanup targets specified"}
	}
	for _, id := range params.Targets {
		if !slices.Contains(proto.StorageCleanupTargetIDs, id) {
			return proto.StorageCleanupResult{}, &rpc.Error{Code: storageCodeBadRequest, Message: fmt.Sprintf("unknown cleanup target %q", id)}
		}
	}
	if !params.DryRun && !params.Confirm {
		return proto.StorageCleanupResult{}, &rpc.Error{Code: storageCodeConfirmNeeded, Message: "selective cleanup requires dry_run=true or confirm=true"}
	}

	dirTargets := map[string]storageDirTarget{}
	for _, spec := range storageDirTargets(deps) {
		dirTargets[spec.targetID] = spec
	}
	running := deps.runningProcs()

	var before int64
	if !params.DryRun && deps.home != "" {
		before, _ = deps.available(deps.home)
	}

	out := proto.StorageCleanupResult{
		DryRun:          params.DryRun,
		AvailableBefore: before,
		Items:           make([]proto.StorageCleanupItem, 0, len(params.Targets)),
	}

	seen := map[string]bool{}
	var estimated int64
	var reportedCleaned int64

	for _, id := range params.Targets {
		if seen[id] {
			continue
		}
		seen[id] = true
		var item proto.StorageCleanupItem
		switch id {
		case proto.StorageTargetDockerBuildCache:
			item = cleanupDockerBuildCache(ctx, deps, params.DryRun)
		case proto.StorageTargetGitWorktreesPrune:
			item = cleanupGitWorktreesPrune(ctx, deps, params.DryRun)
		default:
			spec, ok := dirTargets[id]
			if !ok {
				item = proto.StorageCleanupItem{TargetID: id, Status: proto.StorageCleanupMissing}
			} else {
				item = cleanupDirTarget(ctx, deps, spec, running, params.DryRun)
			}
		}
		if item.Status == proto.StorageCleanupPreview || item.Status == proto.StorageCleanupCleaned {
			estimated += item.Bytes
		}
		if item.Status == proto.StorageCleanupCleaned {
			reportedCleaned += item.Bytes
		}
		out.Items = append(out.Items, item)
	}

	out.EstimatedBytes = estimated
	if !params.DryRun {
		if deps.home != "" {
			after, ok := deps.available(deps.home)
			if ok {
				out.AvailableAfter = after
				out.FreedBytes = max(after-before, 0)
			}
		}
		if out.FreedBytes == 0 && reportedCleaned > 0 {
			out.FreedBytes = reportedCleaned
		}
	}
	return out, nil
}

func cleanupDirTarget(ctx context.Context, deps storageDeps, spec storageDirTarget, running map[string]bool, dryRun bool) proto.StorageCleanupItem {
	item := proto.StorageCleanupItem{
		TargetID: spec.targetID,
		Action:   spec.action,
		Status:   proto.StorageCleanupMissing,
	}
	if len(spec.paths) > 0 {
		item.Path = storageTilde(deps.home, spec.paths[0])
	}
	var guarded []string
	for _, p := range spec.paths {
		realDir, err := guardStorageDirAnyRoot(spec.roots, p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			item.Status = proto.StorageCleanupFailed
			item.Error = sanitizeStorageErr(deps.home, err)
			return item
		default:
			guarded = append(guarded, realDir)
		}
	}
	if len(guarded) == 0 {
		return item
	}
	sizeCtx, cancel := context.WithTimeout(ctx, storageDefaultTimeout)
	var totalBytes int64
	budget := storageMaxWalkEntries
	nonEmpty := false
	for _, d := range guarded {
		if !isStorageDirEmpty(d) {
			nonEmpty = true
		}
		sz, _, _ := boundedDirSize(sizeCtx, d, storageMaxDepthLimit, &budget)
		totalBytes += sz
	}
	cancel()
	if !nonEmpty && totalBytes == 0 {
		return item
	}
	item.Bytes = totalBytes
	if storageToolchainInUse(spec.users, running) {
		item.Status = proto.StorageCleanupInUse
		return item
	}
	if dryRun {
		item.Status = proto.StorageCleanupPreview
		return item
	}
	for _, d := range guarded {
		if err := emptyStorageDirContents(ctx, d); err != nil {
			item.Status = proto.StorageCleanupFailed
			item.Error = sanitizeStorageErr(deps.home, err)
			return item
		}
	}
	item.Status = proto.StorageCleanupCleaned
	return item
}

func cleanupDockerBuildCache(ctx context.Context, deps storageDeps, dryRun bool) proto.StorageCleanupItem {
	item := proto.StorageCleanupItem{
		TargetID: proto.StorageTargetDockerBuildCache,
		Action:   "docker builder prune -f",
		Status:   proto.StorageCleanupMissing,
	}
	if _, err := deps.lookPath("docker"); err != nil {
		item.Status = proto.StorageCleanupSkipped
		item.Error = "docker not installed"
		return item
	}
	dfCtx, cancelDF := context.WithTimeout(ctx, storageDockerCmdTimeout)
	raw, err := deps.runCmd(dfCtx, "docker", "system", "df", "--format", "json")
	cancelDF()
	if err != nil {
		item.Status = proto.StorageCleanupSkipped
		item.Error = "docker daemon unavailable"
		return item
	}
	_, _, _, buildReclaimable, ok := parseDockerSystemDF(raw)
	if !ok || buildReclaimable == 0 {
		return item
	}
	item.Bytes = buildReclaimable
	if dryRun {
		item.Status = proto.StorageCleanupPreview
		return item
	}
	pruneCtx, cancelPrune := context.WithTimeout(ctx, 60*time.Second)
	out, err := deps.runCmd(pruneCtx, "docker", "builder", "prune", "-f")
	cancelPrune()
	if err != nil {
		item.Status = proto.StorageCleanupFailed
		item.Error = "docker builder prune failed"
		return item
	}
	if reclaimed := parseDockerReclaimedLine(out); reclaimed > 0 {
		item.Bytes = reclaimed
	}
	item.Status = proto.StorageCleanupCleaned
	return item
}

func parseDockerReclaimedLine(out []byte) int64 {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "Total reclaimed space:"); ok {
			return parseHumanBytes(strings.TrimSpace(rest))
		}
	}
	return 0
}

func cleanupGitWorktreesPrune(ctx context.Context, deps storageDeps, dryRun bool) proto.StorageCleanupItem {
	item := proto.StorageCleanupItem{
		TargetID: proto.StorageTargetGitWorktreesPrune,
		Action:   "git worktree prune",
		Status:   proto.StorageCleanupMissing,
	}
	if _, err := deps.lookPath("git"); err != nil {
		item.Status = proto.StorageCleanupSkipped
		item.Error = "git not installed"
		return item
	}
	repoSet := map[string]bool{}
	for _, root := range worktreeScanRoots(deps) {
		realRoot, err := guardStorageDirAnyRoot([]string{deps.home, deps.dataDir}, root)
		if err != nil {
			continue
		}
		for _, wt := range discoverWorktreeDirs(realRoot, storageMaxWorktreeScans) {
			if wt.repoRoot != "" {
				repoSet[wt.repoRoot] = true
			}
		}
	}
	if len(repoSet) == 0 {
		return item
	}
	repos := make([]string, 0, len(repoSet))
	for r := range repoSet {
		repos = append(repos, r)
	}
	slices.Sort(repos)
	item.Path = storageTilde(deps.home, repos[0])
	if dryRun {
		item.Status = proto.StorageCleanupPreview
		return item
	}
	for _, repo := range repos {
		cmdCtx, cancel := context.WithTimeout(ctx, storageGitCmdTimeout)
		_, err := deps.runCmd(cmdCtx, "git", "-C", repo, "worktree", "prune")
		cancel()
		if err != nil {
			item.Status = proto.StorageCleanupFailed
			item.Error = sanitizeStorageErr(deps.home, err)
			return item
		}
	}
	item.Status = proto.StorageCleanupCleaned
	return item
}

// boundedDirSize walks root up to maxDepth and entry budget, never following symlinks.
func boundedDirSize(ctx context.Context, root string, maxDepth int, budget *int) (bytes int64, entries int, complete bool) {
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 {
		return 0, 0, true
	}
	if !info.IsDir() {
		return info.Size(), 1, true
	}
	type frame struct {
		path  string
		depth int
	}
	stack := []frame{{path: root, depth: 0}}
	complete = true
	for len(stack) > 0 {
		if ctx.Err() != nil {
			return bytes, entries, false
		}
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		dirEntries, err := os.ReadDir(cur.path)
		if err != nil {
			continue
		}
		for _, e := range dirEntries {
			if ctx.Err() != nil {
				return bytes, entries, false
			}
			if *budget <= 0 {
				return bytes, entries, false
			}
			*budget--
			if e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			entries++
			if fi.IsDir() {
				if cur.depth < maxDepth {
					stack = append(stack, frame{path: filepath.Join(cur.path, e.Name()), depth: cur.depth + 1})
				} else {
					complete = false
				}
			} else {
				bytes += fi.Size()
			}
		}
	}
	return bytes, entries, complete
}

func guardStorageDirAnyRoot(roots []string, p string) (string, error) {
	var lastErr error = errStorageOutsideRoot
	for _, r := range roots {
		if r == "" {
			continue
		}
		real, err := guardStorageDir(r, p)
		if err == nil {
			return real, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		lastErr = err
	}
	return "", lastErr
}

// guardStorageDir resolves p and ensures it is a real directory strictly inside root with no symlink traversal.
func guardStorageDir(root, p string) (string, error) {
	if root == "" || p == "" || !filepath.IsAbs(root) || !filepath.IsAbs(p) {
		return "", errStorageOutsideRoot
	}
	rel, ok := storageInsideRoot(root, p)
	if !ok {
		return "", errStorageOutsideRoot
	}
	info, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return "", errStorageOutsideRoot
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	if realPath != filepath.Join(realRoot, rel) {
		return "", errStorageOutsideRoot
	}
	if _, ok := storageInsideRoot(realRoot, realPath); !ok {
		return "", errStorageOutsideRoot
	}
	return realPath, nil
}

func storageInsideRoot(root, p string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func storageTilde(home, p string) string {
	if home == "" || p == "" {
		return p
	}
	if rel, ok := storageInsideRoot(home, p); ok {
		return "~/" + filepath.ToSlash(rel)
	}
	if filepath.Clean(p) == filepath.Clean(home) {
		return "~"
	}
	return p
}

func sanitizeStorageErr(home string, err error) string {
	if err == nil {
		return ""
	}
	if home == "" {
		return err.Error()
	}
	return strings.ReplaceAll(err.Error(), filepath.Clean(home), "~")
}

func isStorageDirEmpty(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return true
	}
	defer f.Close()
	names, _ := f.Readdirnames(1)
	return len(names) == 0
}

func emptyStorageDirContents(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var firstErr error
	failed := 0
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := filepath.Join(dir, e.Name())
		err := os.RemoveAll(p)
		if err != nil && e.IsDir() {
			makeStorageDirWritable(p)
			err = os.RemoveAll(p)
		}
		if err != nil {
			firstErr = cmp.Or(firstErr, err)
			failed++
		}
	}
	if failed > 1 {
		return fmt.Errorf("%w (and %d more)", firstErr, failed-1)
	}
	return firstErr
}

func makeStorageDirWritable(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if d == nil || !d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			_ = os.Chmod(p, info.Mode().Perm()|0o700)
		}
		return nil
	})
}

func storageToolchainInUse(users []string, running map[string]bool) bool {
	if len(users) == 0 || len(running) == 0 {
		return false
	}
	for proc := range running {
		for _, u := range users {
			rest, ok := strings.CutPrefix(proc, u)
			if ok && strings.Trim(rest, "0123456789.") == "" {
				return true
			}
		}
	}
	return false
}

// readRunningProcNames reads /proc/<pid>/comm on Linux (lightweight, no argv/env). Returns empty map on other OSes.
func readRunningProcNames() map[string]bool {
	if runtime.GOOS != "linux" {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) == 0 || name[0] < '0' || name[0] > '9' {
			continue
		}
		if b, err := os.ReadFile(filepath.Join("/proc", name, "comm")); err == nil {
			if comm := strings.TrimSpace(string(b)); comm != "" {
				out[comm] = true
			}
		}
	}
	return out
}
