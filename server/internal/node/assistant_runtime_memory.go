package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	toolMemoryRead  = "memory_read"
	toolMemoryWrite = "memory_write"
	memoryToolCap   = 64 * 1024
)

func isMemoryTool(name string) bool {
	return name == toolMemoryRead || name == toolMemoryWrite
}

func (n *Node) memoryTools() []proto.Tool {
	return []proto.Tool{
		{
			Name:        toolMemoryRead,
			Description: "Read a markdown note from the assistant's shared memory directory (for example MEMORY.md).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Filename in the memory directory, ending in .md (no path separators)."}},"required":["name"],"additionalProperties":false}`),
		},
		{
			Name:        toolMemoryWrite,
			Description: "Create or overwrite a markdown note in the assistant's shared memory directory. Refused in plan mode.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Filename in the memory directory, ending in .md (no path separators)."},"content":{"type":"string","description":"Markdown content to write (at most 64 KiB)."}},"required":["name","content"],"additionalProperties":false}`),
		},
	}
}

func (n *Node) assistantMemoryTool(_ context.Context, tool, session string, raw json.RawMessage) proto.ToolResult {
	if n == nil || session == "" {
		return proto.TextResult("Memory tools require an assistant session", true)
	}
	if n.guestOf(session) != nil {
		return proto.TextResult("Memory tools are not available in guest sessions", true)
	}
	st, _ := n.state.get(session)
	cwd := n.runtimeAssistantCwd(session, st.Cwd, st)
	if cwd == "" || !identity.IsAssistantDir(filepath.Clean(cwd)) {
		return proto.TextResult("Memory tools are available only in the assistant workspace", true)
	}

	var args struct {
		Name    string  `json:"name"`
		Content *string `json:"content"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return proto.TextResult("Invalid memory tool arguments: "+err.Error(), true)
	}
	if dec.More() {
		return proto.TextResult("Invalid memory tool arguments: unexpected trailing data", true)
	}
	if !memoryNameRe.MatchString(args.Name) {
		return proto.TextResult("Invalid memory filename: must match ^[A-Za-z0-9][A-Za-z0-9._-]{0,100}\\.md$", true)
	}

	if tool == toolMemoryWrite {
		if args.Content == nil {
			return proto.TextResult("memory_write requires content", true)
		}
		if len(*args.Content) > memoryToolCap {
			return proto.TextResult(fmt.Sprintf("Memory note exceeds %d bytes", memoryToolCap), true)
		}
		mode, _ := n.modesOf(session)
		if mode == proto.ModePlan {
			return proto.TextResult("memory_write is refused in plan permission mode", true)
		}
	} else if args.Content != nil {
		return proto.TextResult("memory_read does not accept content", true)
	}

	n.memoryMu.Lock()
	defer n.memoryMu.Unlock()

	dir := n.mergedMemoryDir(cwd)
	if dir == "" {
		return proto.TextResult("Assistant memory directory is unavailable", true)
	}
	if err := rejectSymlink(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return proto.TextResult("Invalid memory directory: "+err.Error(), true)
	}

	target := filepath.Join(dir, args.Name)
	if err := rejectSymlink(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return proto.TextResult("Invalid memory file: "+err.Error(), true)
	}
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		cleanTarget := filepath.Clean(filepath.Join(resolvedDir, args.Name))
		rel, rerr := filepath.Rel(resolvedDir, cleanTarget)
		if rerr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) || rel == "." {
			return proto.TextResult("Invalid memory file path", true)
		}
	}

	if tool == toolMemoryRead {
		info, err := os.Lstat(target)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return proto.TextResult("Memory file not found: "+args.Name, true)
			}
			return proto.TextResult("Cannot read memory file: "+err.Error(), true)
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			return proto.TextResult("Invalid memory file target", true)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return proto.TextResult("Cannot read memory file: "+err.Error(), true)
		}
		if len(data) > memoryToolCap {
			cut := memoryToolCap
			for cut > 0 && !utf8.RuneStart(data[cut]) {
				cut--
			}
			return proto.TextResult(string(data[:cut])+"\n...(truncated)", false)
		}
		return proto.TextResult(string(data), false)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return proto.TextResult("Cannot create memory directory: "+err.Error(), true)
	}
	if err := rejectSymlink(dir); err != nil {
		return proto.TextResult("Invalid memory directory: "+err.Error(), true)
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			return proto.TextResult("Invalid memory file target", true)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return proto.TextResult("Cannot inspect memory file: "+err.Error(), true)
	}

	tmp, err := os.CreateTemp(dir, ".mem-*.tmp")
	if err != nil {
		return proto.TextResult("Cannot stage memory file: "+err.Error(), true)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return proto.TextResult("Cannot set memory file permissions: "+err.Error(), true)
	}
	if _, err := tmp.WriteString(*args.Content); err != nil {
		_ = tmp.Close()
		return proto.TextResult("Cannot write memory file: "+err.Error(), true)
	}
	if err := tmp.Close(); err != nil {
		return proto.TextResult("Cannot flush memory file: "+err.Error(), true)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return proto.TextResult("Cannot commit memory file: "+err.Error(), true)
	}
	keep = true
	return proto.TextResult(fmt.Sprintf("Wrote %s (%d bytes).", args.Name, len(*args.Content)), false)
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlinks are not allowed")
	}
	return nil
}
