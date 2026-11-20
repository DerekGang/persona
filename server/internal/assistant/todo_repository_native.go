package assistant

import (
	"encoding/json"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"mvdan.cc/sh/v3/syntax"
)

// Match ref updates and new branches in git push's summary. A successful exit can also report deletions or no changes.
var repositoryPushResult = lazyre.New(`(?m)^[ \t]*(?:(?:\+[ \t]+)?[[:xdigit:]]{3,}\.{2,3}[[:xdigit:]]{3,}|\*[ \t]+\[new branch\])[ \t]+[^\s]+[ \t]+->[ \t]+([^\s]+)`)

// Recognize only a directly executed shell command, not a quoted example, echo,
// conditional, script body or read command that happens to contain these words.
func taskRepositoryNativeCommand(it proto.Item) (op, cwd string) {
	switch it.ToolName {
	case "Bash", "bash", "exec_command", "functions.exec_command":
	default:
		return "", ""
	}
	var input struct {
		Command string `json:"command"`
		Cmd     string `json:"cmd"`
		Workdir string `json:"workdir"`
	}
	if json.Unmarshal(it.ToolInput, &input) != nil {
		return "", ""
	}
	command := input.Command
	if command == "" {
		command = input.Cmd
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) != 1 || file.Stmts[0].Background || file.Stmts[0].Negated {
		return "", ""
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) < 2 {
		return "", ""
	}
	if len(call.Args) >= 3 && call.Args[0].Lit() == "gh" && call.Args[1].Lit() == "pr" && call.Args[2].Lit() == "create" {
		for _, arg := range call.Args[3:] {
			flag, _, _ := strings.Cut(arg.Lit(), "=")
			if strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && strings.ContainsAny(flag[1:], "hw") {
				return "", ""
			}
			switch flag {
			case "":
				return "", ""
			case "-h", "--help", "--dry-run", "--web":
				return "", ""
			}
		}
		return "create", input.Workdir
	}
	if call.Args[0].Lit() != "git" || call.Args[1].Lit() != "push" {
		return "", ""
	}
	for _, arg := range call.Args[2:] {
		flag, _, _ := strings.Cut(arg.Lit(), "=")
		if strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && strings.ContainsAny(flag[1:], "nd") {
			return "", ""
		}
		switch flag {
		case "", "-n", "--dry-run", "-d", "--delete", "--mirror":
			return "", ""
		}
		if ref := strings.TrimPrefix(flag, "+"); strings.HasPrefix(ref, ":") && ref != ":" {
			return "", "" // an empty source deletes the destination ref
		}
	}
	return "push", input.Workdir
}
