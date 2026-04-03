#!/bin/bash

# remove_coauthor.sh - Remove all Co-authored-by lines from git history
# WARNING: This rewrites git history! Use with caution.
# Usage: ./remove_coauthor.sh [--dry-run] [--push]

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Flags
DRY_RUN=false
DO_PUSH=false

for arg in "$@"; do
    case $arg in
        --dry-run) DRY_RUN=true ;;
        --push) DO_PUSH=true ;;
    esac
done

echo -e "${BLUE}════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}  Co-authored-by Removal Script${NC}"
echo -e "${BLUE}════════════════════════════════════════════════════${NC}"
echo ""

# Check if we're in a git repo
if [ ! -d ".git" ]; then
    echo -e "${RED}❌ Error: Not a git repository!${NC}"
    exit 1
fi

# Get current branch
CURRENT_BRANCH=$(git branch --show-current)
echo -e "${YELLOW}📌 Current branch: ${GREEN}${CURRENT_BRANCH}${NC}"
echo ""

# Check for Co-authored-by commits
echo -e "${YELLOW}🔍 Searching for commits with Co-authored-by...${NC}"
COAUTHOR_COMMITS=$(git log --format="%H %s" --grep="Co-authored-by" 2>/dev/null || true)
COAUTHOR_COUNT=$(echo "$COAUTHOR_COMMITS" | grep -c "Co-authored-by" || echo "0")

if [ "$COAUTHOR_COUNT" -eq 0 ] || [ -z "$COAUTHOR_COMMITS" ]; then
    echo -e "${GREEN}✅ No Co-authored-by commits found! History is clean.${NC}"
    exit 0
fi

echo -e "${RED}Found ${COAUTHOR_COUNT} commits with Co-authored-by:${NC}"
echo ""
echo "$COAUTHOR_COMMITS" | head -10
if [ "$COAUTHOR_COUNT" -gt 10 ]; then
    echo -e "${YELLOW}... and $((COAUTHOR_COUNT - 10)) more${NC}"
fi
echo ""

# Dry run mode
if [ "$DRY_RUN" = true ]; then
    echo -e "${YELLOW}🔍 DRY RUN MODE - No changes will be made${NC}"
    echo ""
    echo -e "Would remove Co-authored-by from ${COAUTHOR_COUNT} commits"
    echo -e "Run without --dry-run to apply changes"
    exit 0
fi

# Warning
echo -e "${RED}⚠️  WARNING: This will rewrite git history!${NC}"
echo -e "${RED}   All commit hashes will change.${NC}"
echo -e "${RED}   You will need to force push after this.${NC}"
echo ""
read -p "Continue? (y/N): " confirm
if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
    echo -e "${YELLOW}Cancelled.${NC}"
    exit 0
fi

# Backup refs
echo ""
echo -e "${YELLOW}📦 Creating backup refs...${NC}"
git for-each-ref --format='%(refname)' refs/heads/ | while read ref; do
    backup_ref="refs/original/$ref"
    git update-ref "$backup_ref" "$ref" 2>/dev/null || true
done

# Remove Co-authored-by from all commits
echo -e "${YELLOW}🔧 Rewriting commit history...${NC}"
echo ""

# Use filter-branch to remove Co-authored-by lines
FILTER_SHELL='
if echo "$GIT_COMMIT" | grep -q "Co-authored-by"; then
    sed "/Co-authored-by:/d" | sed "/Co-authored-by: /d"
else
    cat
fi
'

git filter-branch --force --msg-filter 'sed "/Co-authored-by:/d"' -- --all

echo ""
echo -e "${GREEN}✅ History rewritten successfully!${NC}"
echo ""

# Verify
echo -e "${YELLOW}🔍 Verifying results...${NC}"
REMAINING=$(git log --format="%H %s" --grep="Co-authored-by" 2>/dev/null | grep -c "Co-authored-by" || echo "0")

if [ "$REMAINING" -eq 0 ]; then
    echo -e "${GREEN}✅ All Co-authored-by lines removed!${NC}"
else
    echo -e "${RED}⚠️  Still found ${REMAINING} commits with Co-authored-by${NC}"
fi

# Show stats
echo ""
echo -e "${BLUE}════════════════════════════════════════${NC}"
echo -e "${GREEN}📊 Summary:${NC}"
echo -e "  Commits processed: ${GREEN}${COAUTHOR_COUNT}${NC}"
echo -e "  Remaining:         ${GREEN}${REMAINING}${NC}"
echo -e "${BLUE}════════════════════════════════════════${NC}"

# Push instructions
echo ""
if [ "$DO_PUSH" = true ]; then
    echo -e "${YELLOW}🚀 Force pushing to remote...${NC}"

    # Push all branches
    git push origin --all --force

    # Push all tags
    git push origin --tags --force

    echo -e "${GREEN}✅ Pushed to remote!${NC}"
else
    echo -e "${YELLOW}📋 Next steps:${NC}"
    echo ""
    echo -e "  1. Review the changes:"
    echo -e "     ${BLUE}git log --oneline -20${NC}"
    echo ""
    echo -e "  2. Force push to remote:"
    echo -e "     ${BLUE}git push origin --all --force${NC}"
    echo -e "     ${BLUE}git push origin --tags --force${NC}"
    echo ""
    echo -e "  3. Or run this script with --push flag to auto-push"
fi

# Cleanup backup refs (optional)
echo ""
echo -e "${YELLOW}💡 Tip: To remove backup refs, run:${NC}"
echo -e "   ${BLUE}git for-each-ref --format='%(refname)' refs/original/ | xargs -r -n 1 git update-ref -d${NC}"

echo ""
echo -e "${GREEN}✨ Done!${NC}"
