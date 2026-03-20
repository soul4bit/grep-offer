---
title: "Оболочка Bash"
slug: "linux-bash-shell"
summary: "Пайпы, редиректы, env и первые команды, без которых дальше будет больно."
badge: "linux"
stage: "Linux Base"
module: "Этапы загрузки ОС Linux"
module_order: 2
block_order: 4
kind: "theory"
published: true
---

# Оболочка Bash

Bash не обязан нравиться. Но без него у тебя нет нормальной скорости в инфраструктуре.

## Что надо закрыть

- `pwd`, `cd`, `ls`, `cat`, `less`
- `grep`, `find`, `tail`, `head`
- пайпы `|`
- редиректы `>`, `>>`, `2>&1`
- переменные окружения

## Минимум руками

1. Найди все `.log` файлы.
2. Отфильтруй строки с `error`.
3. Сохрани результат в новый файл.

```bash
find /var/log -type f | head
grep -Ri "error" /var/log 2>/dev/null | head
```
