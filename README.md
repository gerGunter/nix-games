# nix-games

Широкая база **игровых процессов Windows** для группы «Игры» в Mihomo, дополняющая
[RoscomVPN](https://raw.githubusercontent.com/roscomvpn/custom-category/release/mihomo/games.yaml).
Цель — покрывать все распознаваемые игры, а не выбирать только популярные или сетевые.
Офлайн-игры тоже включаются: правило действует лишь на их сетевые соединения.

Рабочий provider: `https://raw.githubusercontent.com/gerGunter/nix-games/main/nix-games.yaml`,
`behavior: classical`, `format: yaml`. RoscomVPN остаётся отдельным провайдером.

## Как отделяются игры от программ

1. [Discord Detectable Applications](https://discord.com/api/v10/applications/detectable)
   даёт application ID, Windows-пути `.exe`, `is_launcher` и `third_party_skus`.
2. По точному Steam app ID выполняются пакетные запросы к
   [Steam Store Browse GetItems](https://api.steampowered.com/IStoreBrowseService/GetItems/v1/).
   Принимаются только успешные записи с явными `item_type: 0`, `type: 0` (игра),
   совпадающими `id`/`appid`. `type: 6` (ПО), другие типы, конфликтующие ссылки,
   недоступные карточки и перенаправления не допускаются к публикации.
3. Удаляются общие/системные имена, лаунчеры, обновляторы, античиты, помощники,
   браузеры и имена, совпадающие у нескольких приложений Discord.
4. Проверяются точные имена и RE2-выражения RoscomVPN, существующий локальный список
   и `review/rejected.json`. Уже покрытые процессы повторно не добавляются.

Старая Steam Store `appdetails` не используется для классификации: при проверке
SRPG Studio она вернула `type: game`, тогда как Store Browse правильно вернул ПО.
Не используем наличие Steam-ссылки, жанра Discord или слово «game» как доказательство.
В каждой принятой записи отчёта есть Steam ID, название и ссылка на карточку.
Идентификаторы SKU с внешними пробелами нормализуются; пустые/некорректные ссылки
не считаются подтверждением. Сам каталог Discord и его связи также могут ошибаться:
это классификация по источникам, а не проверка каждого установленного бинарного файла.

## Полнота и границы

Отчёт включает **весь** отфильтрованный набор, без старого ограничения 100 процессов.
Игры без подходящей Steam-карточки показаны в `unverified_not_for_publication`;
они не принимаются командой массового добавления. Для них нужен отдельный проверенный
источник классификации — отсутствие Steam не означает, что это не игра.
Ни обычные приложения, ни неподтверждённые записи не подмешиваются ради количества.

100% покрытия по именам процессов невозможно: `game.exe`/`javaw.exe` могут принадлежать
разным программам; новые игры могут отсутствовать в Discord; часть имён не уникальна.
Автоматический фильтр консервативен и принимает только безопасные ASCII `.exe`.
Наш список не очищает RoscomVPN: там уже есть лаунчеры, `javaw.exe`, широкое выражение
`Win64-Shipping` и другие вспомогательные процессы. Поэтому группа с обоими провайдерами
не становится строго «только игры». Более ранние правила Mihomo также имеют приоритет.

## Предложения и ручная публикация

- GitHub Actions **Propose game candidates** запускается вручную или по понедельникам
  в 06:23 UTC / 10:23 Самара. Нужна настройка Settings → Actions → General →
  **Allow GitHub Actions to create and approve pull requests**.
- Workflow выполняет тесты, получает Discord/RoscomVPN/Steam и создаёт draft PR только
  с `review/candidates.json`. **Слияние отчёта не меняет маршрутизацию.**
- В отчёте: все новые классифицированные игровые процессы, происхождение, ссылки,
  SHA-256 источников, отдельные неподтверждённые записи и причины исключения.
- При открытом `bot/game-candidates-*` PR новые предложения не создаются; ручные
  изменения не перезаписываются. Ненужные/ошибочные имена заносите в `review/rejected.json`.
- PR новой версии может включать первоначальное массовое наполнение
  `nix-games.yaml`; его слияние является ручным разрешением публикации этой базы.

Для последующих массовых обновлений (Go 1.23+, CI 1.26.5):

```sh
git switch main
git pull --ff-only
git switch -c review/classified-games
go test ./...
go run ./cmd/catalog
# Проверить классификацию в review/candidates.json, затем явно подготовить ВСЕ игры:
go run ./cmd/catalog -approve-games
git diff --stat
go test ./...
git add nix-games.yaml
git commit -m 'Add classified game processes'
git push -u origin review/classified-games
gh pr create --base main --title 'Add classified game processes' --body 'Review the game catalog update before publishing.'
```

`-approve-games` заново проверяет источники и меняет только локальный YAML; он не
делает commit/push/merge. Для частичного отбора остаётся `-approve 'verified.exe'`.
Только ручное слияние PR, меняющего `nix-games.yaml`, публикует правила клиентам.
Существующие одобренные записи не удаляются автоматически при изменении каталогов.

## Проверяемость и безопасность

Нет PAT и сторонних Go-зависимостей. Actions закреплены по commit SHA, checkout не
сохраняет токен. PR CI имеет read-only права, `pull_request_target` отсутствует.
Workflow предложений работает только на `main`, не пишет рабочий YAML и не делает
auto-merge. Крупный отчёт передаётся GitHub API через файл, не через аргументы оболочки.
Для обязательного ручного слияния включите защиту `main` от прямых push и обхода правил.
Для PR, созданных `GITHUB_TOKEN`, GitHub может запросить **Approve workflows to run**.

Источник ограничен 64 MiB и таймаутом 60 секунд; редиректы запрещены. Steam запрашивается
пакетами до 100 ID с паузами. Ошибка сети, неполный пакет, изменённая обязательная схема
или неподдержанный синтаксис RoscomVPN останавливают генерацию до записи отчёта/YAML.
YAML-парсер поддерживает только проверенный plain-scalar формат PROCESS-NAME/REGEX;
неизвестные правила не пропускаются молча. Данные не исполняются как код.

```sh
go test ./...
go vet ./...
# Сохранить компактный снимок классификации Steam:
go run ./cmd/catalog -save-steam /path/to/steam.json
# Повторить сбор без сети на тех же источниках:
go run ./cmd/catalog -discord-file /path/to/discord.json -roscom-file /path/to/games.yaml -steam-file /path/to/steam.json
```

Store Browse и Discord endpoint проверены живыми запросами, но не обещают стабильный
публичный контракт. При изменении формата сначала исправляются адаптер и тесты.
