# grep-offer

Стартовый каркас сайта на Go для `grep-offer.ru`.

Сейчас уже есть:

- сервер на `net/http`
- серверный рендер через `html/template`
- `SQLite` для пользователей и сессий
- регистрация, вход и выход
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
- `DB_PATH` по умолчанию `data/grep-offer.db`

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
- создает `/var/www/grep-offer/shared/data`
- ставит systemd unit
- создает `/etc/grep-offer.env`, если его еще нет

Шаблоны для этого лежат в:

- `deploy/systemd/grep-offer.service.tmpl`
- `deploy/grep-offer.env.example`

После этого проверь `/etc/grep-offer.env`.

По умолчанию там:

```env
ADDR=127.0.0.1:8080
DB_PATH=/var/www/grep-offer/shared/data/grep-offer.db
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
sudo systemctl restart grep-offer
sudo systemctl is-active grep-offer
```

Пример для `visudo`, если runner крутится от пользователя `deploy`:

```sudoers
deploy ALL=NOPASSWD: /bin/systemctl restart grep-offer, /bin/systemctl is-active grep-offer
```

### Что еще важно

- Workflow сейчас ждет runner с labels `self-hosted`, `Linux`, `X64`. Если сервер на ARM, поменяй `GOARCH` в `.github/workflows/deploy.yml` на `arm64` и labels тоже под ARM-раннер.
- Runner должен быть установлен на том же Ubuntu-сервере, где лежит `/var/www/grep-offer`.
- Автодеплой срабатывает на `push` в `main`, а не на локальный `git commit`. Без `git push` GitHub Actions не стартует.
- Для проверки после рестарта добавлен endpoint `/healthz`.
