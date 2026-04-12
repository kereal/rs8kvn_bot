## Git Workflow — rs8kvn_bot

### Terminal Tool
- **Всегда используй `cd: "."`** — абсолютные пути типа `/home/kereal/rs8kvn_bot` НЕ работают
- Терминал работает в контексте проекта, `.` = корень проекта

### Стандартный рабочий процесс

```bash
# 1. Перед началом — пульнуть изменения
git stash              # если есть локальные изменения
git pull --ff-only     # пульнуть без мерджа
git stash pop          # вернуть локальные изменения

# 2. Создать ветку
git checkout -b <type>/<short-description>

# 3. Закоммитить
git add -A
git commit -m "<type>: <description>"

# 4. Запушить
git push -u origin <branch-name>
```

### Ветвление
- `main` — продакшн (только через merge из dev)
- `dev` — основная ветка разработки
- Фича-ветки: `<type>/<description>` (например: `perf/test-speed-optimization`, `docs/fix-readme`, `feat/payments`)
- Типы: `feat`, `fix`, `perf`, `docs`, `refactor`, `test`, `chore`

### Коммит-сообщения
- Формат: `<type>: <description>` (lowercase)
- Подробные списки изменений через `-` в теле коммита

### Pull Requests
- После пуша GitHub предложит создать PR по ссылке из вывода
- Влить в `dev` → потом `dev` в `main`

### Stash при конфликтах
Если `git pull` конфликтует с локальными изменениями:
```bash
git stash
git pull --ff-only
git stash pop    # auto-merges если возможно
```

### Полезные команды
```bash
git status                    # состояние
git diff --stat               # список изменённых файлов
git log --oneline -10         # последние коммиты
git branch -a                 # все ветки
git checkout dev              # переключиться на dev
git branch -d <name>         # удалить локальную ветку
git push origin --delete <name>  # удалить удалённую ветку
```

### Примечания
- В проекте только `dev` и `main` ветки (остальные удалены)
- SSH ключ настроен для GitHub
- Remote: `origin` → `https://github.com/kereal/rs8kvn_bot.git`

---

**Обновлено:** 2026-04-13
