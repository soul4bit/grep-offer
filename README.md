# grep-offer

<div align="center">
  <p><strong>Путь до DevOps без карго-культа</strong></p>
  <p>Go-платформа с roadmap, уроками, тестами, прогрессом, приватным контентом и админкой.</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.26">
    <img src="https://img.shields.io/badge/PostgreSQL-runtime-4169E1?style=flat-square&logo=postgresql&logoColor=white" alt="PostgreSQL">
    <img src="https://img.shields.io/badge/Rendering-html%2Ftemplate-111827?style=flat-square" alt="html/template">
    <img src="https://img.shields.io/badge/Deploy-GitHub%20Actions-2088FF?style=flat-square&logo=githubactions&logoColor=white" alt="GitHub Actions">
  </p>
</div>

## Что это

`grep-offer.ru` собирает маршрут до DevOps в нормальном порядке: база, доставка, платформа и рынок. Это не просто лендинг, а полноценный продукт с авторизацией, кабинетом, прогрессом, тестами, редактором уроков и продовым деплоем.

Главная идея репозитория: код лежит в открытом GitHub, а реальные уроки и пользовательские загрузки живут на сервере вне публичного репо.

## Скриншоты

<p align="center">
  <img src="docs/screenshots/home.png" alt="Главная страница grep-offer" width="100%">
  <img src="docs/screenshots/login.png" alt="Экран авторизации grep-offer" width="100%">
</p>

## Что внутри

- главная с позиционированием и дорожной картой по этапам
- регистрация с approve через Telegram и подтверждением по email
- вход, выход и сброс пароля
- кабинет с прогрессом, текущим фокусом и следующими чекпоинтами
- маршрут обучения с теорией, практикой и test-уроками
- приватные markdown-уроки на сервере
- редактор уроков в админке с live preview и загрузкой изображений
- управление пользователями: admin, ban, delete
- audit log для admin-действий и чувствительных событий
- deploy lock на время релиза
- автодеплой через GitHub Actions и self-hosted runner на Ubuntu

## Стек

| Слой | Что используется |
| --- | --- |
| Backend | `Go`, `net/http`, `html/template` |
| Data | `PostgreSQL` |
| Content | server-side markdown в `CONTENT_DIR` |
| Assets | uploads в `UPLOADS_DIR` |
| Infra | `nginx`, `systemd`, `GitHub Actions`, self-hosted runner |

## Структура репозитория

```text
cmd/grep-offer/         entrypoint приложения
internal/app/           HTTP-маршруты, auth, dashboard, admin, security
internal/content/       библиотека markdown-уроков
internal/store/         PostgreSQL-слой и инициализация данных
internal/notify/        SMTP и Telegram-уведомления
internal/ui/            шаблоны и статические файлы интерфейса
deploy/                 setup-скрипт, env example, systemd template
content/articles/       шаблон урока для публичного репозитория
shared/                 локальные uploads и служебные директории
```

## Быстрый старт

### Требования

- `Go 1.26+`
- доступный `PostgreSQL`

### Минимальные env-переменные

```env
ADDR=:8080
DATABASE_URL=postgres://grep_offer:secret@localhost:5432/grep_offer?sslmode=disable
CONTENT_DIR=content/articles
UPLOADS_DIR=shared/uploads
DEPLOY_LOCK_PATH=shared/flags/deploy.lock
APP_BASE_URL=http://localhost:8080
```

### Запуск

```bash
go run ./cmd/grep-offer
```

При старте приложение само:

- проверит подключение к `PostgreSQL`
- создаст недостающие таблицы
- создаст директории для контента, uploads и deploy lock
- поднимет HTTP-сервер

Полный пример переменных окружения лежит в [deploy/grep-offer.env.example](deploy/grep-offer.env.example).

## Основные переменные окружения

### Приложение и база

```env
ADDR=127.0.0.1:8080
DATABASE_URL=postgres://grep_offer:change-me@db.example.com:5432/grep_offer?sslmode=require
APP_BASE_URL=https://grep-offer.ru
CONTENT_DIR=/var/www/grep-offer/shared/content/articles
UPLOADS_DIR=/var/www/grep-offer/shared/uploads
DEPLOY_LOCK_PATH=/var/www/grep-offer/shared/flags/deploy.lock
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

Если SMTP и Telegram не настроены, approval-flow регистрации отключается.

## Как работает регистрация

1. Пользователь отправляет форму регистрации.
2. Заявка уходит админу в Telegram.
3. Админ жмет `Approve`.
4. Пользователь получает письмо с подтверждением.
5. После перехода по ссылке аккаунт подтверждается, и пользователь автоматически входит.

## Как хранится контент

Публичный репозиторий хранит код, но не боевые уроки.

Рабочий контент живет на сервере:

- `CONTENT_DIR` — markdown-уроки
- `UPLOADS_DIR` — изображения и вложения
- `DEPLOY_LOCK_PATH` — lock-файл, который включает read-only режим на время деплоя

Типовая прод-схема:

```text
/var/www/grep-offer/
  current/
  releases/
  shared/
    content/articles/
    flags/
    uploads/
```

Это дает три полезных свойства:

- уроки не попадают в публичный git
- картинки не хранятся в репозитории
- редактор пишет напрямую в server-side storage

## Редактор уроков

Админский редактор доступен по:

- `/admin/articles/new`
- `/admin/articles/{slug}/edit`

Он умеет:

- создавать и редактировать markdown-уроки
- задавать `stage`, `module`, `kind`, порядок модуля и блока
- сохранять `draft` или `published`
- показывать live preview
- загружать картинки в `/uploads/editor/...`
- добавлять вопросы для `test`-уроков

Пример frontmatter:

```yaml
title: "Название блока"
slug: "slug-for-lesson"
summary: "Короткое описание"
badge: "linux"
stage: "Фундамент"
module: "Название модуля"
module_order: 4
block_order: 1
kind: "theory" # theory | practice | test
published: true
```

В публичном репо оставлен только шаблон: [content/articles/_template.lesson.md](content/articles/_template.lesson.md).

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

Продовый сценарий такой:

1. Push в `main`.
2. GitHub Actions запускает `go test ./...`.
3. Self-hosted runner на Ubuntu собирает Linux binary.
4. На сервере создается новый release в `/var/www/grep-offer/releases/<commit>`.
5. Ставится deploy lock, обновляется symlink `current`, затем `systemd` перезапускает сервис.
6. Если `healthz` не проходит, release откатывается на предыдущий.

Полезные файлы:

- [deploy/setup-server.sh](deploy/setup-server.sh)
- [deploy/systemd/grep-offer.service.tmpl](deploy/systemd/grep-offer.service.tmpl)
- [.github/workflows/deploy.yml](.github/workflows/deploy.yml)

### Первый запуск на сервере

```bash
bash deploy/setup-server.sh
```

Скрипт создает `releases`, `shared/content/articles`, `shared/flags`, `shared/uploads`, ставит `systemd` unit и подготавливает `/etc/grep-offer.env`.

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

## Полезные пути

- `/` — главная
- `/login` — авторизация
- `/register` — заявка на доступ
- `/dashboard` — кабинет
- `/learn` — маршрут
- `/admin/articles` — уроки и тесты
- `/admin/users` — пользователи
- `/admin/logs` — audit log
- `/healthz` — healthcheck
