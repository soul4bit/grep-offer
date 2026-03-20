---
title: "Работа с логами и journalctl"
slug: "linux-logs-journalctl"
summary: "Если что-то сломалось, сначала лог. Не гипотеза, не интуиция, не гороскоп."
badge: "linux"
stage: "Linux Base"
module: "Первые шаги в Linux"
module_order: 3
block_order: 2
kind: "theory"
published: true
---

# Работа с логами и `journalctl`

Без чтения логов весь DevOps очень быстро превращается в фольклор.

## База

- `journalctl -u service`
- `journalctl -b`
- `tail -f /var/log/...`
- фильтрация по времени и приоритету

## Минимум руками

1. Выбери сервис.
2. Посмотри его последние 50 строк.
3. Найди последние warning или error.
4. Запиши, что ты понял по симптомам.
