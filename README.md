# grep-offer

Стартовый каркас сайта на Go для `grep-offer.ru`.

Сейчас уже есть:

- сервер на `net/http`
- серверный рендер через `html/template`
- `PostgreSQL` как основная база приложения
- регистрация с approve через Telegram и подтверждением по email
- вход и выход
- стартовая визуальная система для рофл-роадмапа в DevOps

Палитра первого захода:

- графитовый фон `#0f1311`
- кислотный лайм `#d7ff64`
- коралл `#ff7a59`
- теплый светлый текст `#f3efe3`

Запуск локально:

```bash
go run ./cmd/grep-offer
```

Переменные окружения:

- `ADDR` по умолчанию `:8080`
- `DATABASE_URL` например `postgres://grep_offer:secret@db.example.com:5432/grep_offer?sslmode=require`
- `APP_BASE_URL` например `https://grep-offer.ru`
- `SMTP_HOST`, `SMTP_PORT`, `SMTP_SECURE`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `MAIL_FROM`
- `TELEGRAM_BOT_TOKEN`, `TELEGRAM_ADMIN_CHAT_ID`, `TELEGRAM_WEBHOOK_SECRET`

Если SMTP и Telegram env не заданы, approval-flow регистрации отключается.

## Регистрация

Текущий flow такой:

1. Пользователь отправляет форму регистрации.
2. Заявка уходит в Telegram-бота админу.
3. Админ жмет approve.
4. Пользователю уходит письмо с ссылкой подтверждения.
5. После перехода по ссылке аккаунт создается и пользователь автоматически входит в кабинет.

## Автодеплой

Схема деплоя сделана через GitHub Actions с `self-hosted runner` на Ubuntu-сервере:

- любой пуш в `main` запускает тесты
- после тестов job `deploy` запускается прямо на сервере
- на сервере локально собирается Linux-бинарь
- переключается `current`-релиз в `/var/www/grep-offer`
- сервис `grep-offer` перезапускается через `systemd`

Почему так, а не `git pull` в `/var/www/grep-offer`:

- код всегда приходит через checkout GitHub Actions, а не через ручной git на проде
- релизы остаются разложенными по папкам `/var/www/grep-offer/releases/<sha>`
- рабочий путь приложения стабилен: `/var/www/grep-offer/current`
- не нужны SSH secrets для копирования артефактов на сервер

Текущий workflow лежит в `.github/workflows/deploy.yml`.

### Первый запуск на сервере

На сервере один раз выполни из репозитория:

```bash
bash deploy/setup-server.sh
```

Скрипт:

- создает `/var/www/grep-offer/releases`
- создает `/var/www/grep-offer/shared/data`, если нужно забрать старый SQLite-файл для одноразовой миграции
- ставит systemd unit
- создает `/etc/grep-offer.env`, если его еще нет

Шаблоны для этого лежат в:

- `deploy/systemd/grep-offer.service.tmpl`
- `deploy/grep-offer.env.example`

После этого проверь `/etc/grep-offer.env`.

По умолчанию там:

```env
ADDR=127.0.0.1:8080
DATABASE_URL=postgres://grep_offer:change-me@db.example.com:5432/grep_offer?sslmode=require
APP_BASE_URL=https://grep-offer.ru
SMTP_HOST=smtp.example.com
SMTP_PORT=465
SMTP_SECURE=true
SMTP_USERNAME=registration@example.com
SMTP_PASSWORD=change-me
MAIL_FROM=registration@example.com
TELEGRAM_BOT_TOKEN=change-me
TELEGRAM_ADMIN_CHAT_ID=123456789
TELEGRAM_WEBHOOK_SECRET=change-me
```

### GitHub Runner

На Ubuntu нужно один раз установить self-hosted runner для этого репозитория.

Базовая схема такая:

1. В GitHub открой `Settings -> Actions -> Runners -> New self-hosted runner`.
2. Выбери `Linux` и `x64`.
3. Выполни на сервере команды, которые даст GitHub для скачивания и регистрации runner.
4. Поставь runner как service, чтобы он стартовал после перезагрузки.

Практически это будет выглядеть примерно так:

```bash
mkdir -p ~/actions-runner && cd ~/actions-runner
curl -o actions-runner-linux-x64.tar.gz -L https://github.com/actions/runner/releases/latest/download/actions-runner-linux-x64.tar.gz
tar xzf ./actions-runner-linux-x64.tar.gz
./config.sh --url https://github.com/soul4bit/grep-offer --token <TOKEN>
sudo ./svc.sh install
sudo ./svc.sh start
```

После регистрации runner должен иметь стандартные labels:

- `self-hosted`
- `Linux`
- `X64`

### sudo для runner

Пользователь, под которым работает runner, должен уметь без пароля выполнять:

```bash
sudo /usr/bin/systemctl restart grep-offer
sudo /usr/bin/systemctl is-active grep-offer
```

Пример для `visudo`, если runner крутится от пользователя `deploy`:

```sudoers
deploy ALL=NOPASSWD: /usr/bin/systemctl restart grep-offer, /usr/bin/systemctl is-active grep-offer
```

### Telegram webhook

После того как домен и HTTPS уже работают, один раз выставь webhook для бота:

```bash
curl -X POST "https://api.telegram.org/bot<TELEGRAM_BOT_TOKEN>/setWebhook" \
  -d "url=https://grep-offer.ru/telegram/webhook" \
  -d "secret_token=<TELEGRAM_WEBHOOK_SECRET>"
```

Проверить можно так:

```bash
curl "https://api.telegram.org/bot<TELEGRAM_BOT_TOKEN>/getWebhookInfo"
```

### Что еще важно

- Workflow сейчас ждет runner с labels `self-hosted`, `Linux`, `X64`. Если сервер на ARM, поменяй `GOARCH` в `.github/workflows/deploy.yml` на `arm64` и labels тоже под ARM-раннер.
- Runner должен быть установлен на том же Ubuntu-сервере, где лежит `/var/www/grep-offer`.
- Автодеплой срабатывает на `push` в `main`, а не на локальный `git commit`. Без `git push` GitHub Actions не стартует.
- Для проверки после рестарта добавлен endpoint `/healthz`.
- В `/var/www/grep-offer` должны лежать только `current`, `releases` и `shared`. Клон репозитория лучше хранить отдельно, например в `/home/deploy/grep-offer`.
- Приложение стартует только с `DATABASE_URL`. Старый SQLite нужен только как источник для одноразовой миграции.

### Миграция из SQLite в PostgreSQL

Если у тебя уже была рабочая SQLite-база, после деплоя в релизе появится отдельный бинарь:

```bash
/var/www/grep-offer/current/grep-offer-migrate \
  -sqlite /var/www/grep-offer/shared/data/grep-offer.db \
  -postgres "$DATABASE_URL"
```

Он переносит:

- пользователей
- сессии
- pending-регистрации
- reset-токены
- roadmap progress
- lesson progress
- test questions
- test results

По умолчанию migrator требует пустую PostgreSQL-базу. Если тебе нужно повторно залить данные в уже заполненную БД, запусти его с `-force`.
## Контент и админка

Теперь в проекте есть еще два слоя:

- `shared/content/articles` — приватные markdown-уроки, которые читает приложение
- `shared/uploads` — картинки и другие файлы, которые отдаются с `/uploads/...`

По умолчанию приложение читает:

```env
CONTENT_DIR=/var/www/grep-offer/shared/content/articles
UPLOADS_DIR=/var/www/grep-offer/shared/uploads
ADMIN_EMAILS=admin@example.com
```

Что это значит:

- `CONTENT_DIR` — откуда читать markdown-статьи
- `UPLOADS_DIR` — откуда отдавать изображения и вложения
- `ADMIN_EMAILS` — список email через запятую; если пользователь с таким email существует, ему автоматически поднимается `is_admin`

На сервере `setup-server.sh` создает `/var/www/grep-offer/shared/content/articles` и `/var/www/grep-offer/shared/uploads`, так что контент можно держать вне публичного GitHub-репозитория.

Админка доступна по `/admin` и умеет:

- смотреть список пользователей
- выдавать и снимать админку
- банить и разбанивать
- удалять аккаунт
- создавать и редактировать уроки в markdown

### Как добавлять учебные блоки

Теперь основной способ — через админку. Админ создает урок по `/admin/articles/new`, а приложение сохраняет его как `.md` в `CONTENT_DIR`.

Каждый блок в Linux-маршруте это отдельный `.md`-файл с frontmatter:

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

После этого блок автоматически попадет в `/learn` и встроится в порядок маршрута.
