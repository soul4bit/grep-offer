# grep-offer

`grep-offer.ru` — это рофл-роадмап до DevOps в формате продукта: с маршрутом, уроками, тестами, прогрессом, админкой и приватным контентом вне публичного репозитория.

## Что уже есть

- регистрация с approve через Telegram и подтверждением по email
- вход, выход и сброс пароля
- маршрут обучения с прогрессом по урокам и тестам
- приватные markdown-уроки на сервере
- редактор уроков в админке
- загрузка картинок в редакторе прямо в `UPLOADS_DIR`
- управление пользователями: admin, ban, delete
- audit log для admin-действий и чувствительных событий
- прод на `PostgreSQL`
- автодеплой через GitHub Actions + self-hosted runner на Ubuntu

## Стек

- `Go`
- `net/http`
- `html/template`
- `PostgreSQL`
- `systemd`
- `nginx`
- `GitHub Actions`

## Локальный запуск

Нужны:

- `Go`
- доступный `PostgreSQL`

Минимальный набор переменных:

```env
ADDR=:8080
DATABASE_URL=postgres://grep_offer:secret@localhost:5432/grep_offer?sslmode=disable
CONTENT_DIR=content/articles
UPLOADS_DIR=shared/uploads
APP_BASE_URL=http://localhost:8080
```

Запуск:

```bash
go run ./cmd/grep-offer
```

Приложение само:

- создаст недостающие таблицы
- создаст директории для контента и uploads
- поднимет HTTP-сервер

## Основные env-переменные

### База и приложение

```env
ADDR=127.0.0.1:8080
DATABASE_URL=postgres://grep_offer:change-me@db.example.com:5432/grep_offer?sslmode=require
APP_BASE_URL=https://grep-offer.ru
CONTENT_DIR=/var/www/grep-offer/shared/content/articles
UPLOADS_DIR=/var/www/grep-offer/shared/uploads
ADMIN_EMAILS=admin@example.com
```

### SMTP

```env
SMTP_HOST=smtp.example.com
SMTP_PORT=465
SMTP_SECURE=true
SMTP_USERNAME=registration@example.com
SMTP_PASSWORD=change-me
MAIL_FROM=registration@example.com
```

### Telegram approve-flow

```env
TELEGRAM_BOT_TOKEN=change-me
TELEGRAM_ADMIN_CHAT_ID=123456789
TELEGRAM_WEBHOOK_SECRET=change-me
```

Полный пример лежит в [deploy/grep-offer.env.example](/d:/work/proj/github/grep-offer/deploy/grep-offer.env.example).

## Регистрация

Текущий flow такой:

1. Пользователь отправляет форму регистрации.
2. Заявка уходит в Telegram-бота админу.
3. Админ жмет `Approve`.
4. Пользователь получает письмо с подтверждением.
5. После перехода по ссылке аккаунт подтверждается и пользователь автоматически входит.

Если SMTP или Telegram не настроены, approval-flow не включается.

## Контент

Публичный GitHub-репозиторий хранит код, но не реальные уроки.

Рабочий контент живет на сервере:

- `CONTENT_DIR` — markdown-уроки
- `UPLOADS_DIR` — картинки и вложения

Типовая прод-схема:

```text
/var/www/grep-offer/
  current/
  releases/
  shared/
    content/articles/
    uploads/
```

Это значит:

- уроки не светятся в открытом репозитории
- картинки не лежат в git
- редактор пишет прямо в server-side storage

## Редактор уроков

Админский редактор доступен по `/admin/articles/new` и `/admin/articles/{slug}/edit`.

Он умеет:

- создавать и редактировать markdown-уроки
- ставить `stage`, `module`, `kind`, порядок модуля и блока
- сохранять `draft` или `published`
- показывать live preview
- загружать картинки в `/uploads/editor/...`
- вставлять путь картинки в markdown
- добавлять вопросы для `test`-уроков

### Frontmatter урока

Каждый урок хранится как `.md` с frontmatter:

```yaml
title: "Название блока"
slug: "slug-for-lesson"
summary: "Короткое описание"
badge: "linux"
stage: "Linux Base"
module: "Название модуля"
module_order: 4
block_order: 1
kind: "theory" # theory | practice | test
published: true
```

В публичной репе оставлен только шаблон: [content/articles/_template.lesson.md](/d:/work/proj/github/grep-offer/content/articles/_template.lesson.md).

## Админка

Админка разбита на три секции:

- `/admin/articles` — уроки, markdown и test-блоки
- `/admin/users` — пользователи, admin, ban, delete
- `/admin/logs` — audit log

Audit log хранит:

- admin-действия
- логины и logout
- события регистрации
- approve/reject через Telegram
- запросы и завершение password reset
- загрузки изображений из редактора

## Деплой

Прод-схема такая:

- push в `main`
- GitHub Actions прогоняет тесты
- deploy job работает на self-hosted runner на Ubuntu
- на сервере собирается релиз
- обновляется `/var/www/grep-offer/current`
- `systemd` перезапускает `grep-offer`

Workflow лежит в [deploy.yml](/d:/work/proj/github/grep-offer/.github/workflows/deploy.yml).

### Первый запуск на сервере

Один раз выполни:

```bash
bash deploy/setup-server.sh
```

Скрипт:

- создает `releases`
- создает `shared/content/articles`
- создает `shared/uploads`
- ставит `systemd` unit
- создает `/etc/grep-offer.env`, если его еще нет

## GitHub Runner

Для прод-деплоя нужен self-hosted runner на Ubuntu-сервере.

Базовый сценарий:

1. `Settings -> Actions -> Runners -> New self-hosted runner`
2. выбрать `Linux` и `x64`
3. выполнить команды GitHub на сервере
4. поставить runner как service

Runner должен иметь labels:

- `self-hosted`
- `Linux`
- `X64`

## sudo для runner

Пользователь runner-а должен без пароля уметь выполнять:

```bash
sudo /usr/bin/systemctl restart grep-offer
sudo /usr/bin/systemctl is-active grep-offer
```

Пример `sudoers`:

```sudoers
deploy ALL=NOPASSWD: /usr/bin/systemctl restart grep-offer, /usr/bin/systemctl is-active grep-offer
```

## Telegram webhook

После того как домен и HTTPS уже работают:

```bash
curl -X POST "https://api.telegram.org/bot<TELEGRAM_BOT_TOKEN>/setWebhook" \
  -d "url=https://grep-offer.ru/telegram/webhook" \
  -d "secret_token=<TELEGRAM_WEBHOOK_SECRET>"
```

Проверка:

```bash
curl "https://api.telegram.org/bot<TELEGRAM_BOT_TOKEN>/getWebhookInfo"
```

## Что важно держать в голове

- рантайм полностью работает через `PostgreSQL`
- реальный контент не должен лежать в публичном git
- uploads сейчас хранятся на сервере, не в S3
- автодеплой срабатывает на `push` в `main`, не на локальный `git commit`
- в `/var/www/grep-offer` должны жить только `current`, `releases`, `shared`
- клон репозитория лучше держать отдельно, например в `/home/deploy/grep-offer`

## Полезные пути

- `/` — главная
- `/dashboard` — кабинет
- `/learn` — маршрут
- `/admin/articles` — уроки и тесты
- `/admin/users` — пользователи
- `/admin/logs` — audit log
- `/healthz` — healthcheck
