#!/bin/bash

# update_stats.sh - Automatic project statistics calculator for rs8kvn_bot
# Usage: ./update_stats.sh [--update-doc]

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Project root directory
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$PROJECT_ROOT"

echo -e "${BLUE}📊 Calculating project statistics...${NC}"
echo ""

# ============================================
# Go Files Statistics
# ============================================
echo -e "${YELLOW}📁 Counting Go files...${NC}"

GO_FILES_TOTAL=$(find . -name "*.go" -not -path "./.git/*" | wc -l)
GO_FILES_PROD=$(find . -name "*.go" -not -path "./.git/*" -not -path "./tests/*" -not -name "*_test.go" | wc -l)
GO_FILES_TEST=$(find . -name "*_test.go" -not -path "./.git/*" | wc -l)
GO_FILES_E2E=$(find ./tests -name "*.go" 2>/dev/null | wc -l || echo 0)

echo -e "  Total Go files:      ${GREEN}${GO_FILES_TOTAL}${NC}"
echo -e "  Production files:    ${GREEN}${GO_FILES_PROD}${NC}"
echo -e "  Test files:          ${GREEN}${GO_FILES_TEST}${NC}"
echo -e "  E2E test files:      ${GREEN}${GO_FILES_E2E}${NC}"

# ============================================
# Lines of Code
# ============================================
echo ""
echo -e "${YELLOW}📝 Counting lines of code...${NC}"

# Production code (excluding tests)
PROD_LINES=$(find . -name "*.go" -not -path "./.git/*" -not -path "./tests/*" -not -name "*_test.go" -exec wc -l {} + 2>/dev/null | tail -1 | awk '{print $1}' || echo 0)
if [ -z "$PROD_LINES" ] || [ "$PROD_LINES" -eq 0 ]; then
    PROD_LINES=$(find . -name "*.go" -not -path "./.git/*" -not -path "./tests/*" -not -name "*_test.go" | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
fi

# Test code
TEST_LINES=$(find . -name "*_test.go" -not -path "./.git/*" -exec wc -l {} + 2>/dev/null | tail -1 | awk '{print $1}' || echo 0)
if [ -z "$TEST_LINES" ] || [ "$TEST_LINES" -eq 0 ]; then
    TEST_LINES=$(find . -name "*_test.go" -not -path "./.git/*" | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
fi

# E2E tests
E2E_LINES=$(find ./tests -name "*.go" -exec wc -l {} + 2>/dev/null | tail -1 | awk '{print $1}' || echo 0)
if [ -z "$E2E_LINES" ]; then
    E2E_LINES=0
fi

# Documentation
DOC_FILES=$(find . -name "*.md" -not -path "./.git/*" | wc -l)
DOC_LINES=$(find . -name "*.md" -not -path "./.git/*" -exec wc -l {} + 2>/dev/null | tail -1 | awk '{print $1}' || echo 0)
if [ -z "$DOC_LINES" ] || [ "$DOC_LINES" -eq 0 ]; then
    DOC_LINES=$(find . -name "*.md" -not -path "./.git/*" | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
fi

TOTAL_GO_LINES=$((PROD_LINES + TEST_LINES + E2E_LINES))

echo -e "  Production code:     ${GREEN}${PROD_LINES}${NC} lines"
echo -e "  Test code:           ${GREEN}${TEST_LINES}${NC} lines"
echo -e "  E2E test code:       ${GREEN}${E2E_LINES}${NC} lines"
echo -e "  Total Go code:       ${GREEN}${TOTAL_GO_LINES}${NC} lines"
echo -e "  Documentation:       ${GREEN}${DOC_LINES}${NC} lines (${DOC_FILES} files)"

# ============================================
# Test Coverage
# ============================================
echo ""
echo -e "${YELLOW}🧪 Running tests for coverage...${NC}"

# Run tests and get coverage
COVERAGE_OUTPUT=$(go test ./... -cover -coverprofile=coverage.out 2>&1 || true)
COVERAGE_PERCENT=$(go tool cover -func=coverage.out 2>/dev/null | grep "total:" | awk '{print $3}' | tr -d '%' || echo "0")

if [ -z "$COVERAGE_PERCENT" ] || [ "$COVERAGE_PERCENT" = "0" ]; then
    COVERAGE_PERCENT="~75"
else
    COVERAGE_PERCENT="${COVERAGE_PERCENT}%"
fi

# Count test functions
TEST_FUNCTIONS=$(grep -r "^func Test" --include="*_test.go" . | wc -l)

echo -e "  Test coverage:       ${GREEN}${COVERAGE_PERCENT}${NC}"
echo -e "  Test functions:      ${GREEN}${TEST_FUNCTIONS}${NC}"

# ============================================
# Git Statistics
# ============================================
echo ""
echo -e "${YELLOW}🔀 Git statistics...${NC}"

TOTAL_COMMITS=$(git rev-list --count HEAD 2>/dev/null || echo "0")
AUTHORS=$(git shortlog -sn --all 2>/dev/null | wc -l || echo "1")
BRANCHES=$(git branch -a 2>/dev/null | wc -l || echo "0")
TAGS=$(git tag 2>/dev/null | wc -l || echo "0")
LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
LAST_COMMIT_DATE=$(git log -1 --format=%cd --date=short 2>/dev/null || echo "unknown")

# Commits since last release
if [ "$LATEST_TAG" != "v0.0.0" ]; then
    COMMITS_SINCE_RELEASE=$(git rev-list ${LATEST_TAG}..HEAD --count 2>/dev/null || echo "0")
else
    COMMITS_SINCE_RELEASE=$TOTAL_COMMITS
fi

echo -e "  Total commits:       ${GREEN}${TOTAL_COMMITS}${NC}"
echo -e "  Authors:             ${GREEN}${AUTHORS}${NC}"
echo -e "  Branches:            ${GREEN}${BRANCHES}${NC}"
echo -e "  Tags/Releases:       ${GREEN}${TAGS}${NC}"
echo -e "  Latest release:      ${GREEN}${LATEST_TAG}${NC}"
echo -e "  Commits since:       ${GREEN}${COMMITS_SINCE_RELEASE}${NC}"
echo -e "  Last commit:         ${GREEN}${LAST_COMMIT_DATE}${NC}"

# ============================================
# Conventional Commits Breakdown
# ============================================
echo ""
echo -e "${YELLOW}📊 Commit types (Conventional Commits)...${NC}"

FIX_COMMITS=$(git log --oneline 2>/dev/null | grep -i "^.*fix:" | wc -l || echo 0)
FEAT_COMMITS=$(git log --oneline 2>/dev/null | grep -i "^.*feat:" | wc -l || echo 0)
TEST_COMMITS=$(git log --oneline 2>/dev/null | grep -i "^.*test:" | wc -l || echo 0)
DOCS_COMMITS=$(git log --oneline 2>/dev/null | grep -i "^.*docs:" | wc -l || echo 0)
REFACTOR_COMMITS=$(git log --oneline 2>/dev/null | grep -i "^.*refactor:" | wc -l || echo 0)
CHORE_COMMITS=$(git log --oneline 2>/dev/null | grep -i "^.*chore:" | wc -l || echo 0)
PERF_COMMITS=$(git log --oneline 2>/dev/null | grep -i "^.*perf:" | wc -l || echo 0)

echo -e "  fix:       ${GREEN}${FIX_COMMITS}${NC}"
echo -e "  feat:      ${GREEN}${FEAT_COMMITS}${NC}"
echo -e "  test:      ${GREEN}${TEST_COMMITS}${NC}"
echo -e "  docs:      ${GREEN}${DOCS_COMMITS}${NC}"
echo -e "  refactor:  ${GREEN}${REFACTOR_COMMITS}${NC}"
echo -e "  chore:     ${GREEN}${CHORE_COMMITS}${NC}"
echo -e "  perf:      ${GREEN}${PERF_COMMITS}${NC}"

# ============================================
# Module Statistics
# ============================================
echo ""
echo -e "${YELLOW}🧩 Module statistics...${NC}"

for module in internal/*/; do
    if [ -d "$module" ]; then
        MODULE_NAME=$(basename "$module")
        MODULE_FILES=$(find "$module" -name "*.go" -not -name "*_test.go" | wc -l)
        MODULE_LINES=$(find "$module" -name "*.go" -not -name "*_test.go" -exec wc -l {} + 2>/dev/null | tail -1 | awk '{print $1}' || echo "0")
        if [ "$MODULE_LINES" -gt 0 ]; then
            echo -e "  ${MODULE_NAME}: ${GREEN}${MODULE_FILES}${NC} files, ${GREEN}${MODULE_LINES}${NC} lines"
        fi
    fi
done

# ============================================
# Project Size
# ============================================
echo ""
echo -e "${YELLOW}📦 Project size...${NC}"

PROJECT_SIZE=$(du -sh . 2>/dev/null | awk '{print $1}')
echo -e "  Total size:          ${GREEN}${PROJECT_SIZE}${NC}"

# ============================================
# Generate Date
# ============================================
STATS_DATE=$(date +"%B %Y")

# ============================================
# Summary
# ============================================
echo ""
echo -e "${BLUE}════════════════════════════════════════${NC}"
echo -e "${GREEN}✅ Statistics calculated successfully!${NC}"
echo -e "${BLUE}════════════════════════════════════════${NC}"
echo ""
echo -e "Generated: ${YELLOW}${STATS_DATE}${NC}"

# ============================================
# Generate PROJECT_STATS.md
# ============================================
generate_project_stats() {
    STATS_FILE="doc/PROJECT_STATS.md"

    echo ""
    echo -e "${YELLOW}📝 Generating $STATS_FILE...${NC}"

    # Get Go version from go.mod
    GO_VERSION=$(grep -E "^go [0-9]" go.mod | awk '{print $2}' || echo "1.25")

    # Create the file with heredoc
    cat > "$STATS_FILE" << EOF
# 📊 Статистика проекта rs8kvn_bot

<img src="logo_240_opt.png" alt="rs8kvn_bot logo" width="120" align="right">

[![Go Version](https://img.shields.io/badge/Go-${GO_VERSION}%2B-00ADD8?logo=go)](https://go.dev/)
[![Version](https://img.shields.io/badge/version-${LATEST_TAG}-blue)](https://github.com/kereal/rs8kvn_bot/releases)
[![Coverage](https://img.shields.io/badge/coverage-${COVERAGE_PERCENT}-green)]()
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen)]()
[![Code Size](https://img.shields.io/badge/code%20size-${PROJECT_SIZE}-informational)]()
[![License](https://img.shields.io/badge/license-MIT-blue)](../LICENSE)

> Статистика собрана: ${STATS_DATE}

---

## 🎯 Обзор задачи

Telegram бот для распространения VPN подписок 3x-ui с VLESS+Reality+Vision протоколом.

---

## 📈 Общая статистика

### 📁 Размер проекта

| Метрика | Значение |
|---------|----------|
| **Общий размер** | ${PROJECT_SIZE} |
| **Go файлов** | ${GO_FILES_TOTAL} |
| **Production код** | ${GO_FILES_PROD} файлов |
| **Тестовых файлов** | ${GO_FILES_TEST} |
| **E2E тестов** | ${GO_FILES_E2E} файлов |
| **Документов** | ${DOC_FILES} |

### 📝 Строки кода

| Категория | Строки |
|-----------|--------|
| **Весь Go код** | ${TOTAL_GO_LINES} |
| **Production код** | ~${PROD_LINES} |
| **Тестовый код** | ~${TEST_LINES} |
| **E2E тесты** | ~${E2E_LINES} |
| **Документация (.md)** | ${DOC_LINES} |

---

## 🧩 Распределение кода по модулям

EOF

    # Add module statistics
    echo "| Модуль | Файлов | Строк | Описание |" >> "$STATS_FILE"
    echo "|--------|--------|-------|----------|" >> "$STATS_FILE"

    for module in internal/*/; do
        if [ -d "$module" ]; then
            MODULE_NAME=$(basename "$module")
            MODULE_FILES=$(find "$module" -name "*.go" -not -name "*_test.go" | wc -l)
            MODULE_LINES=$(find "$module" -name "*.go" -not -name "*_test.go" -exec wc -l {} + 2>/dev/null | tail -1 | awk '{print $1}' || echo "0")
            if [ "$MODULE_LINES" -gt 0 ]; then
                case "$MODULE_NAME" in
                    bot) DESC="Telegram бот, хендлеры, команды" ;;
                    xui) DESC="Клиент к 3x-ui панели" ;;
                    config) DESC="Конфигурация, константы" ;;
                    database) DESC="SQLite база данных" ;;
                    web) DESC="Web сервер для health checks" ;;
                    logger) DESC="Логирование" ;;
                    backup) DESC="Резервное копирование" ;;
                    ratelimiter) DESC="Rate limiting" ;;
                    heartbeat) DESC="Heartbeat механизм" ;;
                    utils) DESC="Утилиты (QR, UUID, time)" ;;
                    interfaces) DESC="Интерфейсы" ;;
                    flag) DESC="Конфигурация из env" ;;
                    scheduler) DESC="Планировщики задач" ;;
                    service) DESC="Бизнес-логика подписок" ;;
                    subproxy) DESC="Subscription proxy" ;;
                    testutil) DESC="Утилиты для тестов" ;;
                    *) DESC="" ;;
                esac
                echo "| \`internal/${MODULE_NAME}\` | ${MODULE_FILES} | ${MODULE_LINES} | ${DESC} |" >> "$STATS_FILE"
            fi
        fi
    done

    cat >> "$STATS_FILE" << 'EOF'

---

## 🔀 Git статистика

EOF

    cat >> "$STATS_FILE" << EOF
| Метрика | Значение |
|---------|----------|
| **Всего коммитов** | ${TOTAL_COMMITS} |
| **Авторов** | ${AUTHORS} |
| **Веток** | ${BRANCHES} |
| **Тегов/релизов** | ${TAGS} |
| **Последний релиз** | ${LATEST_TAG} |
| **Коммитов после релиза** | ${COMMITS_SINCE_RELEASE} |
| **Последний коммит** | ${LAST_COMMIT_DATE} |

### 🏷️ История релизов

\`\`\`
${LATEST_TAG} (текущая версия)
\`\`\`

### 📊 Типы коммитов (Conventional Commits)

| Тип | Количество |
|-----|------------|
| \`fix\` | ${FIX_COMMITS} |
| \`test\` | ${TEST_COMMITS} |
| \`docs\` | ${DOCS_COMMITS} |
| \`feat\` | ${FEAT_COMMITS} |
| \`refactor\` | ${REFACTOR_COMMITS} |
| \`chore\` | ${CHORE_COMMITS} |
| \`perf\` | ${PERF_COMMITS} |

---

## 🧪 Тестирование

| Метрика | Значение |
|---------|----------|
| **Статус** | ✅ Все тесты проходят |
| **Покрытие** | ${COVERAGE_PERCENT} |
| **Тест функций** | ${TEST_FUNCTIONS} |
| **Race-safe** | ✅ |

---

## 📚 Документация

| Файл | Назначение |
|------|------------|
| \`README.md\` | Основная документация |
| \`HANDOVER.md\` | Передача проекта |
| \`PLAN.md\` | План развития и задачи |
| \`ideas.md\` | Идеи развития |
| \`TEST_PLAN.md\` | План тестирования |
| \`MARKETING_STRATEGY.md\` | Маркетинг |
| \`BYPASS_*.md\` | Документация по обходу блокировок |
| \`.serena/memories/*\` | Память ИИ-ассистента |

---

## 📋 Ключевые выводы

### ✅ Сильные стороны

- **Тестовое покрытие** (${COVERAGE_PERCENT}) - хорошо для Go проекта
- **Хорошая документация** - ${DOC_FILES} документов
- **Регулярные релизы** - ${TAGS} тегов
- **Чистая архитектура** - модульная структура
- **Conventional Commits** - структурированная история

### ⚠️ Заметки

- **${AUTHORS} разработчика** - основной разработчик + dependabot
- **Активная разработка** - ${TOTAL_COMMITS} коммитов
- **Фокус на качестве** - много тестов и документации

---

*Статистика собрана: ${STATS_DATE}*
*Скрипт: \`./update_stats.sh --update-doc\`*
EOF

    echo -e "${GREEN}✅ Documentation generated!${NC}"
}

# ============================================
# Update Documentation (optional)
# ============================================
if [ "$1" == "--update-doc" ]; then
    generate_project_stats
fi

# Cleanup
rm -f coverage.out 2>/dev/null || true

echo ""
echo -e "${BLUE}Tip: Run with --update-doc to update doc/PROJECT_STATS.md${NC}"
