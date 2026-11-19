package node

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func nvmVersionsDirs(home string) []string {
	standard := filepath.Join(home, ".nvm", "versions", "node")
	if root := os.Getenv("NVM_DIR"); filepath.IsAbs(root) {
		if configured := filepath.Join(root, "versions", "node"); configured != standard {
			return []string{configured, standard}
		}
	}
	return []string{standard}
}

// nvmLauncherEnv lets `#!/usr/bin/env node` resolve the runtime paired with an NVM-installed npm command even
// when the service has no NVM directory on PATH. npm links bin/<command> to lib/node_modules/<package>/….
func nvmLauncherEnv(exe string, env []string) []string {
	if runtime.GOOS == "windows" {
		return env
	}
	script, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return env
	}
	for _, root := range nvmVersionsDirs(userHome()) {
		versions, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(versions, script)
		version, rest, _ := strings.Cut(rel, "/")
		if err != nil || version == ".." || (filepath.Dir(rest) != "bin" && !strings.HasPrefix(rest, "lib/node_modules/")) {
			continue
		}
		bin := filepath.Join(versions, version, "bin")
		if st, err := os.Stat(filepath.Join(bin, "node")); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
			continue
		}
		if !envNodeScript(script) {
			return env
		}
		path := envValue(env, "PATH")
		sep := string(os.PathListSeparator)
		first, _, _ := strings.Cut(path, sep)
		if first == bin {
			return env
		}
		if path != "" {
			path = sep + path
		}
		return append(withoutEnv(env, []string{"PATH"}), "PATH="+bin+path)
	}
	return env
}

func envNodeScript(path string) bool {
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [64]byte
	n, _ := f.Read(head[:])
	line, _, _ := strings.Cut(string(head[:n]), "\n")
	line, ok := strings.CutPrefix(line, "#!")
	return ok && slices.Equal(strings.Fields(line), []string{"/usr/bin/env", "node"})
}
