# Terminal Tool Usage

## Important: Directory Path Format

When using the `terminal` tool, the `cd` parameter must use the **basename** of the project root directory, NOT the absolute path.

### Correct Format:
```
cd: "tgvpn_go"  # ✅ CORRECT - use basename
```

### Incorrect Format:
```
cd: "/home/kereal/tgvpn_go"  # ❌ WRONG - absolute path causes "not in any of the project's worktrees" error
cd: "rs8kvn_bot"  # ❌ WRONG - project name, not directory basename
cd: "."  # ❌ WRONG - ambiguous in multi-root workspaces
```

## Project Information
- Project name: `rs8kvn_bot`
- Project root absolute path: `/home/kereal/tgvpn_go`
- **Terminal cd parameter**: `tgvpn_go` (basename of root directory)

## Available CLI Tools
- `git` - version control
- `gh` - GitHub CLI (version 2.46.0)

## Git Workflow Skill
The project has a git-workflow-skill at `.agents/skills/git-workflow-skill/` that provides best practices for:
- Branching strategies
- Conventional Commits
- Pull Request workflow
- CI/CD integration
- Release management
