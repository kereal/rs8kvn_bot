#!/bin/bash
# Git Workflow Verification Script
# Checks repository for git workflow best practices
set -e

REPO_DIR="${1:-.}" ERRORS=0 WARNINGS=0

cat <<EOF
=== Git Workflow Verification ===
Repository: $REPO_DIR

EOF

cd "$REPO_DIR"

[ -d .git ] || { echo "❌ Not a git repository"; exit 1; }

echo "=== Branch Naming Convention ==="
BRANCHES=$(git branch -a 2>/dev/null | sed 's/^[* ]*//; /HEAD/d; s/remotes\/origin\///' | sort -u)
INVALID=$(echo "$BRANCHES" | grep -vE '^(main|master|develop|feature/|fix/|bugfix/|hotfix/|release/|chore/|docs/|test/|refactor/)' | tr '\n' ' ')

if [ -n "$INVALID" ]; then
  echo "⚠️  Non-standard branch names found:"
  echo "  $INVALID"
  echo "   Expected: main, develop, feature/*, fix/*, release/*, hotfix/*"
  ((WARNINGS++))
else
  echo "✅ All branch names follow conventions"
fi

echo -e "\n=== Commit Message Format ==="
RECENT=$(git log --oneline -20 2>/dev/null | head -20)
VALID=$(echo "$RECENT" | grep -cE '^[a-f0-9]+ (feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\(.+\))?(!)?: .+' || true)
MERGES=$(echo "$RECENT" | grep -c '^[a-f0-9]+ Merge' || true)
TOTAL=$(echo "$RECENT" | wc -l)
CHECKED=$((TOTAL - MERGES))

if [ "$CHECKED" -gt 0 ]; then
  PERCENT=$((VALID * 100 / CHECKED))
  [ "$PERCENT" -ge 80 ] && echo "✅ $PERCENT% of commits follow Conventional Commits format" || {
    echo "⚠️  $PERCENT% of commits follow Conventional Commits format"
    ((WARNINGS++))
  }
fi

echo -e "\n=== .gitignore Check ==="
if [ -f .gitignore ]; then
  echo "✅ .gitignore exists"
  MISSING=""
  for p in node_modules .env "*.log" dist build .DS_Store; do
    grep -q "$p" .gitignore 2>/dev/null || MISSING="$MISSING $p"
  done
  [ -n "$MISSING" ] && echo "   ℹ️  Consider adding:$MISSING"
else
  echo "⚠️  No .gitignore file found"
  ((WARNINGS++))
fi

echo -e "\n=== Git Hooks ==="
ACTIVE_HOOKS=$(find .git/hooks -type f ! -name '*.sample' 2>/dev/null | wc -l)
[ "$ACTIVE_HOOKS" -gt 0 ] && echo "✅ Found $ACTIVE_HOOKS active hook(s)" && find .git/hooks -type f ! -name '*.sample' -exec basename {} \; 2>/dev/null | sed 's/^/   /' || echo "ℹ️  No active git hooks"

[ -d .husky ] && echo "✅ Husky hooks directory found"
[ -f commitlint.config.js ] || [ -f .commitlintrc ] || [ -f .commitlintrc.json ] && echo "✅ Commitlint configuration found"

echo -e "\n=== Code Ownership ==="
[ -f CODEOWNERS ] || [ -f .github/CODEOWNERS ] || [ -f docs/CODEOWNERS ] && echo "✅ CODEOWNERS file found" || echo "ℹ️  No CODEOWNERS file (optional)"

echo -e "\n=== PR Templates ==="
[ -f .github/PULL_REQUEST_TEMPLATE.md ] || [ -d .github/PULL_REQUEST_TEMPLATE ] && echo "✅ PR template(s) found" || echo "ℹ️  No PR template (recommended)"

echo -e "\n=== CI/CD Configuration ==="
CI_FOUND=0
[ -d .github/workflows ] && {
  WF=$(find .github/workflows -name '*.yml' -o -name '*.yaml' 2>/dev/null | wc -l)
  [ "$WF" -gt 0 ] && echo "✅ GitHub Actions: $WF workflow(s)" && CI_FOUND=1
}
[ -f .gitlab-ci.yml ] && echo "✅ GitLab CI configuration found" && CI_FOUND=1
[ -f Jenkinsfile ] && echo "✅ Jenkinsfile found" && CI_FOUND=1
[ -f .circleci/config.yml ] && echo "✅ CircleCI configuration found" && CI_FOUND=1
[ -f azure-pipelines.yml ] && echo "✅ Azure Pipelines configuration found" && CI_FOUND=1

[ "$CI_FOUND" = 0 ] && echo "⚠️  No CI/CD configuration found" && ((WARNINGS++))

echo -e "\n=== Release Configuration ==="
[ -f .releaserc ] || [ -f .releaserc.json ] || [ -f .releaserc.yml ] || [ -f release.config.js ] && echo "✅ Semantic release configuration found"
[ -f CHANGELOG.md ] || [ -f CHANGELOG ] && echo "✅ CHANGELOG found" || echo "ℹ️  No CHANGELOG (recommended for releases)"

[ -f package.json ] && VERSION=$(grep '"version"' package.json | head -1 | sed 's/.*: *"\([^"]*\)".*/\1/') && [ -n "$VERSION" ] && echo "✅ Package version: $VERSION"

echo -e "\n=== Current State ==="
CURRENT_BRANCH=$(git branch --show-current 2>/dev/null)
echo "Current branch: $CURRENT_BRANCH"

git diff --quiet 2>/dev/null && git diff --cached --quiet 2>/dev/null && echo "✅ Working directory clean" || {
  CHANGES=$(git status --porcelain 2>/dev/null | wc -l)
  echo "⚠️  $CHANGES uncommitted change(s)"
}

if git remote | grep -q origin 2>/dev/null; then
  git fetch origin --quiet 2>/dev/null || true
  LOCAL=$(git rev-parse "$CURRENT_BRANCH" 2>/dev/null)
  REMOTE=$(git rev-parse "origin/$CURRENT_BRANCH" 2>/dev/null || true)
  [ -n "$REMOTE" ] && {
    [ "$LOCAL" = "$REMOTE" ] && echo "✅ Up to date with origin/$CURRENT_BRANCH" || {
      BEHIND=$(git rev-list --count "$LOCAL..$REMOTE" 2>/dev/null || echo 0)
      AHEAD=$(git rev-list --count "$REMOTE..$LOCAL" 2>/dev/null || echo 0)
      echo "ℹ️  Branch is $AHEAD ahead, $BEHIND behind origin/$CURRENT_BRANCH"
    }
  }
fi

echo -e "\n=== Conflict Markers ==="
CONFLICT=$(grep -rln '<<<<<<< \|======= \|>>>>>>> ' --include='*.js' --include='*.ts' --include='*.php' --include='*.py' . 2>/dev/null | grep -v node_modules | grep -v vendor | head -5)
if [ -n "$CONFLICT" ]; then
  echo "❌ Conflict markers found in files:"
  echo "$CONFLICT" | sed 's/^/   /'
  ((ERRORS++))
else
  echo "✅ No conflict markers found"
fi

echo -e "\n=== Summary ==="
echo "Errors: $ERRORS"
echo "Warnings: $WARNINGS"

if [ "$ERRORS" -gt 0 ]; then
  echo "❌ Verification FAILED"
  exit 1
elif [ "$WARNINGS" -gt 3 ]; then
  echo "⚠️  Verification completed with warnings"
  exit 0
else
  echo "✅ Verification PASSED"
  exit 0
fi