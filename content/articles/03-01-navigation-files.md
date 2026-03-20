---
title: "Навигация и создание файлов"
slug: "linux-navigation-files"
summary: "Первый рабочий быт в Linux: ходить по каталогам, создавать файлы и не путаться."
badge: "linux"
stage: "Linux Base"
module: "Первые шаги в Linux"
module_order: 3
block_order: 1
kind: "theory"
published: true
---

# Навигация и создание файлов

Это уже не обзор, а обычная рабочая рутина. Здесь надо добить базовые действия до уровня “не думаю, просто делаю”.

## Минимум руками

- создать каталог
- создать файл
- скопировать файл
- переместить файл
- удалить файл и каталог

```bash
mkdir -p ~/lab/linux
touch ~/lab/linux/notes.txt
cp ~/lab/linux/notes.txt ~/lab/linux/notes.bak
mv ~/lab/linux/notes.bak ~/lab/linux/archive.txt
```
