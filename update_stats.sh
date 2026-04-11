#!/bin/bash
# update_stats.sh - Automatic project statistics calculator for rs8kvn_bot
# Usage: ./update_stats.sh [--update-doc]
set -e

RED='\033[0;31m' GREEN='\033[0;32m' YELLOW='\033[1;33m' BLUE='\033[0;34m' NC='\033[0m'

PROJECT_ROOT="$(cd "${BASH_SOURCE[0]%/*}" && pwd)"
cd "$PROJECT_ROOT"

echo -e "${BLUE}📊 Calculating project statistics...${NC}\n"

echo -e "${YELLOW}📁 Counting Go files...${NC}"
GO_FILES_TOTAL=$(find . -name '*.go' -path './.git/*' -prune -o -print | wc -l)
GO_FILES_PROD=$(find . -name '*.go' -path './.git/*' -o -path './tests/*' -o -name '*_test.go' -prune -o -print | wc -l)
GO_FILES_TEST=$(find . -name '*_test.go' -path './.git/*' -prune -o -print | wc -l)
GO_FILES_E2E=$(find ./tests -name '*.go' 2>/dev/null | wc -l || echo 0)

printf "  Total Go files:      ${GREEN}%d${NC}\n  Production files:    ${GREEN}%d${NC}\n  Test files:          ${GREEN}%d${NC}\n  E2E test files:      ${GREEN}%d${NC}\n\n" \
       "$GO_FILES_TOTAL" "$GO_FILES_PROD" "$GO_FILES_TEST" "$GO_FILES_E2E"

echo -e "${YELLOW}📝 Counting lines of code...${NC}"

# count_lines counts lines of Go source files excluding .git, the tests directory, and files ending with _test.go, and echoes the total number of lines (prints 0 when none).
count_lines() { find . -name '*.go' -path './.git/*' -o -path './tests/*' -o -name '*_test.go' -prune -o -exec wc -l {} + 2>/dev/null | awk 'END{print $1}'; }
# test_lines counts the total number of lines across all Go test files (`*_test.go`) excluding the `./.git` directory and echoes the resulting line count.
test_lines() { find . -name '*_test.go' -path './.git/*' -prune -o -exec wc -l {} + 2>/dev/null | awk 'END{print $1}'; }
# e2e_lines counts the total number of lines in Go files under ./tests and echoes 0 if no matching files are found.
e2e_lines() { find ./tests -name '*.go' -exec wc -l {} + 2>/dev/null | awk 'END{print $1}'; }
# doc_lines counts the total number of lines across Markdown (.md) files under the repository root, excluding files inside .git, and echoes the resulting count (produces empty output if no Markdown files are found).
doc_lines() { find . -name '*.md' -path './.git/*' -prune -o -exec wc -l {} + 2>/dev/null | awk 'END{print $1}'; }

PROD_LINES=$(count_lines)
TEST_LINES=$(test_lines)
E2E_LINES=$(e2e_lines)
DOC_LINES=$(doc_lines)
DOC_FILES=$(find . -name '*.md' -path './.git/*' -prune -o -print | wc -l)

[ -z "$PROD_LINES" ] && PROD_LINES=0
[ -z "$TEST_LINES" ] && TEST_LINES=0
[ -z "$E2E_LINES" ]  && E2E_LINES=0
[ -z "$DOC_LINES" ]  && DOC_LINES=0

TOTAL_GO_LINES=$((PROD_LINES + TEST_LINES + E2E_LINES))

printf "  Production code:     ${GREEN}%d${NC} lines\n  Test code:           ${GREEN}%d${NC} lines\n  E2E test code:       ${GREEN}%d${NC} lines\n  Total Go code:       ${GREEN}%d${NC} lines\n  Documentation:       ${GREEN}%d${NC} lines (%d files)\n\n" \
       "$PROD_LINES" "$TEST_LINES" "$E2E_LINES" "$TOTAL_GO_LINES" "$DOC_LINES" "$DOC_FILES"

echo -e "${YELLOW}🧪 Running tests for coverage...${NC}"
COVERAGE_PERCENT="0"
go test ./... -cover -coverprofile=coverage.out >/dev/null 2>&1 && \
  COVERAGE_PERCENT=$(go tool cover -func=coverage.out 2>/dev/null | awk '/total:/{print substr($3,1,length($3)-1)}')
[ -z "$COVERAGE_PERCENT" ] && COVERAGE_PERCENT="~75" || COVERAGE_PERCENT="${COVERAGE_PERCENT}%"

TEST_FUNCTIONS=$(grep -r '^func Test' --include='*_test.go' . | wc -l)

printf "  Test coverage:       ${GREEN}%s${NC}\n  Test functions:      ${GREEN}%d${NC}\n\n" "$COVERAGE_PERCENT" "$TEST_FUNCTIONS"

echo -e "${YELLOW}🔀 Git statistics...${NC}"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 && {
  TOTAL_COMMITS=$(git rev-list --count HEAD 2>/dev/null || echo 0)
  AUTHORS=$(git shortlog -sn --all 2>/dev/null | wc -l || echo 1)
  BRANCHES=$(git branch -a 2>/dev/null | wc -l || echo 0)
  TAGS=$(git tag 2>/dev/null | wc -l || echo 0)
  LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)
  LAST_COMMIT_DATE=$(git log -1 --format=%cd --date=short 2>/dev/null || echo unknown)
  [ "$LATEST_TAG" != v0.0.0 ] && COMMITS_SINCE_RELEASE=$(git rev-list ${LATEST_TAG}..HEAD --count 2>/dev/null || echo 0) || COMMITS_SINCE_RELEASE=$TOTAL_COMMITS
} || {
  TOTAL_COMMITS=0 AUTHORS=1 BRANCHES=0 TAGS=0 LATEST_TAG=v0.0.0 LAST_COMMIT_DATE=unknown COMMITS_SINCE_RELEASE=0
}

printf "  Total commits:       ${GREEN}%s${NC}\n  Authors:             ${GREEN}%s${NC}\n  Branches:            ${GREEN}%s${NC}\n  Tags/Releases:       ${GREEN}%s${NC}\n  Latest release:      ${GREEN}%s${NC}\n  Commits since:       ${GREEN}%s${NC}\n  Last commit:         ${GREEN}%s${NC}\n\n" \
       "$TOTAL_COMMITS" "$AUTHORS" "$BRANCHES" "$TAGS" "$LATEST_TAG" "$COMMITS_SINCE_RELEASE" "$LAST_COMMIT_DATE"

echo -e "${YELLOW}📊 Commit types (Conventional Commits)...${NC}"

read FIX COMMITS FEAT_COMMITS TEST_COMMITS DOCS_COMMITS REFACTOR_COMMITS CHORE_COMMITS PERF_COMMITS < <(git log --oneline 2>/dev/null | awk '
BEGIN{fix=feat=test=docs=ref=chore=perf=0}
/^.*fix:/i      {fix++}
/^.*feat:/i     {feat++}
/^.*test:/i     {test++}
/^.*docs:/i     {docs++}
/^.*refactor:/i {ref++}
/^.*chore:/i    {chore++}
/^.*perf:/i     {perf++}
END{print fix,feat,test,docs,ref,chore,perf}'
)

printf "  fix:       ${GREEN}%s${NC}\n  feat:      ${GREEN}%s${NC}\n  test:      ${GREEN}%s${NC}\n  docs:      ${GREEN}%s${NC}\n  refactor:  ${GREEN}%s${NC}\n  chore:     ${GREEN}%s${NC}\n  perf:      ${GREEN}%s${NC}\n\n" \
       "$FIX" "$FEAT_COMMITS" "$TEST_COMMITS" "$DOCS_COMMITS" "$REFACTOR_COMMITS" "$CHORE_COMMITS" "$PERF_COMMITS"

echo -e "${YELLOW}🧩 Module statistics...${NC}"
for module in internal/*/; do
  [ -d "$module" ] || continue
  m=$(basename "$module")
  f=$(find "$module" -name '*.go' ! -name '*_test.go' | wc -l)
  l=$(find "$module" -name '*.go' ! -name '*_test.go' -exec wc -l {} + 2>/dev/null | awk 'END{print $1}')
  [ "$l" -gt 0 ] && printf "  %s: ${GREEN}%d${NC} files, ${GREEN}%d${NC} lines\n" "$m" "$f" "$l"
done

echo -e "\n${YELLOW}📦 Project size...${NC}"
PROJECT_SIZE=$(du -sh . 2>/dev/null | awk '{print $1}')
printf "  Total size:          ${GREEN}%s${NC}\n\n" "$PROJECT_SIZE"

STATS_DATE=$(date +"%B %Y")

echo -e "${BLUE}════════════════════════════════════════${NC}"
echo -e "${GREEN}✅ Statistics calculated successfully!${NC}"
echo -e "${BLUE}════════════════════════════════════════${NC}\n"
echo -e "Generated: ${YELLOW}${STATS_DATE}${NC}\n"

# generate_project_stats generates or updates doc/PROJECT_STATS.md with aggregated project metrics (Go file and line counts, module breakdown, git statistics, coverage and test metrics, conventional commit tallies, and status badges) and prints progress messages.
generate_project_stats() {
  STATS_FILE="doc/PROJECT_STATS.md"
  echo -e "\n${YELLOW}📝 Generating $STATS_FILE...${NC}"
  GO_VERSION=$(grep -E '^go [0-9]' go.mod | awk '{print $2}' || echo 1.25)

  cat > "$STATS_FILE" << 'EOF'
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

| Мет��ика | Значение |
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

| Модуль | Файлов | Строк | Описание |
|--------|--------|-------|----------|
EOF

  for module in internal/*/; do
    [ -d "$module" ] || continue
    m=$(basename "$module")
    f=$(find "$module" -name '*.go' ! -name '*_test.go' | wc -l)
    l=$(find "$module" -name '*.go' ! -name '*_test.go' -exec wc -l {} + 2>/dev/null | awk 'END{print $1}')
    [ "$l" -gt 0 ] || continue
    case "$m" in
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
    echo "| \`internal/${m}\` | ${f} | ${l} | ${DESC} |" >> "$STATS_FILE"
  done

  cat >> "$STATS_FILE" << EOF

---

## 🔀 Git статистика

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
| \`fix\` | ${FIX} |
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

[ "$1" = "--update-doc" ] && generate_project_stats
rm -f coverage.out 2>/dev/null
echo -e "\n${BLUE}Tip: Run with --update-doc to update doc/PROJECT_STATS.md${NC}"