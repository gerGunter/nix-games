# nix-games

Широкая база **игровых процессов Windows** для группы «Игры» в Mihomo, дополняющая
[RoscomVPN](https://raw.githubusercontent.com/roscomvpn/custom-category/release/mihomo/games.yaml).
Цель — покрывать все распознаваемые игры, а не выбирать только популярные или сетевые.
Офлайн-игры тоже включаются: правило действует лишь на их сетевые соединения.

Рабочие providers (`behavior: classical`, `format: yaml`); RoscomVPN остаётся отдельным:

- `https://raw.githubusercontent.com/gerGunter/nix-games/main/nix-games.yaml` — `PROCESS-NAME`;
- `https://raw.githubusercontent.com/gerGunter/nix-games/main/nix-games-paths.yaml` —
  `PROCESS-PATH-REGEX` для игр с безликим exe (`game.exe`, `nw.exe`, `client.exe`, имена
  из 1–2 символов). Правило привязано к уникальной папке игры из пути Discord, например
  `(?i)(^|[\\/])mark_of_the_ninja[\\/]bin[\\/]game\.exe$`. Подключается по желанию.

Mihomo сравнивает `PROCESS-NAME` без учёта регистра (`strings.EqualFold`), а regex
компилирует с IgnoreCase, поэтому имена хранятся в нижнем регистре.

## Как отделяются игры от программ

1. [Discord Detectable Applications](https://discord.com/api/v10/applications/detectable)
   даёт application ID, Windows-пути `.exe`, `is_launcher` и `third_party_skus`.
2. Каждое приложение классифицируется по **всем** своим Steam-ссылкам через
   [Steam Store Browse GetItems](https://api.steampowered.com/IStoreBrowseService/GetItems/v1/)
   (только успешные записи с `item_type: 0` и совпадающими `id`/`appid`):
   - доказательство игры: `type` 0 игра, 1 демо, 2 мод, 4 DLC, 12 плейтест/бета,
     14 сетевой компонент/издание. Discord часто ссылается на DLC рядом с базовой игрой —
     так раньше терялась Darktide;
   - вето: 3/7 видео, 6 ПО, 13 инструмент — приложение считается программой;
   - недоступные карточки, перенаправления и прочие типы ничего не доказывают и
     **не отменяют** валидную карточку той же игры (Rocket League, FFXIV, GW2).
3. Приложения без вердикта Steam проверяются по Xbox/Microsoft Store SKU через
   [display catalog](https://displaycatalog.mp.microsoft.com/v7.0/products):
   `ProductKind: Game` — игра, `Application` — вето (Sea of Thieves, Minecraft Bedrock…).
4. У ~9 тыс. подтверждённых Steam-игр Discord не знает ни одного Windows-exe. Для них
   берётся launch-конфигурация Steam (PICS: `installdir` + `config.launch.*.executable`
   для Windows) через публичное зеркало [steamcmd.net](https://api.steamcmd.net/v1/info/413150).
   Запрашиваются только базовые игры (`type 0`) таких приложений, `common.type` должен быть
   `Game`; пути проходят те же фильтры, в отчёте у них `source: steam_pics:<appid>`.
5. Имя процесса решается по **всем** приложениям, где оно встречается: любое
   приложение-программа запрещает имя, иначе достаточно одного подтверждённого игрового.
   Игра и её демо/издание/переиздание больше не отбрасываются как «неоднозначные».
   Имя, общее для 5 и более приложений, считается движком/обёрткой и уходит в path-правила.
6. Фильтр проверяет только имя файла (запятая в папке «Warhammer 40,000» больше не
   ломает запись). Разрешены Unicode, `'`, `&`, `@`; запрещены `,`, `#`, `:`, кавычки.
   Удаляются лаунчеры, обновляторы, установщики, античиты, crash-репортёры, редакторы,
   конфигураторы, выделенные серверы, артбуки/саундтреки, общие утилиты
   (`autorun`, `4gb_patch`, `x360ce`, `dosbox`, `config*`…), имена из 1–2 символов и всё из
   [`review/system-binaries.txt`](review/system-binaries.txt) — exe Windows, Git for Windows и
   dev-инструментов (`sh.exe`, `gh.exe`, `sc.exe`, `cl.exe`…). Безликие имена с уникальной
   папкой уходят в path-правила.
7. Проверяются точные имена и regex RoscomVPN, существующие локальные списки и
   `review/rejected.json` (имя процесса или путь Discord вида `folder/game.exe`).

Старая Steam Store `appdetails` не используется для классификации: при проверке
SRPG Studio она вернула `type: game`, тогда как Store Browse правильно вернул ПО.
Не используем наличие ссылки, жанра Discord или слово «game» как доказательство.
В каждой принятой записи отчёта есть источник, ID, название, тип и ссылка на карточку.
Сам каталог Discord и его связи также могут ошибаться: это классификация по источникам,
а не проверка каждого установленного бинарного файла.

## Полнота и границы

Отчёт включает **весь** отфильтрованный набор. Игры без карточки Steam/Xbox показаны в
`unverified_not_for_publication` и не принимаются массовым добавлением.

Охват ограничен списком приложений Discord (~25 тыс.): Steam-игры, которых там нет, не
рассматриваются. Launch-конфигурация Steam иногда указывает на лаунчер (Baldur's Gate 3),
а не на сам процесс игры; такие имена отсеиваются фильтром, но реальный exe тогда не найден.
100% покрытия по именам невозможно: часть имён не уникальна, новые игры появляются с задержкой.
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
git add nix-games.yaml nix-games-paths.yaml
git commit -m 'Add classified game processes'
git push -u origin review/classified-games
gh pr create --base main --title 'Add classified game processes' --body 'Review the game catalog update before publishing.'
```

`-approve-games` заново проверяет источники и меняет только локальный YAML; он не
делает commit/push/merge. Частичный отбор: `-approve 'verified.exe,folder/game.exe'`.
Только ручное слияние PR, меняющего YAML, публикует правила клиентам.
Существующие записи не удаляются при изменении каталогов; удаляются только те, что
перестали проходить фильтры безопасности (команда печатает каждую).

## Проверяемость и безопасность

Нет PAT и сторонних Go-зависимостей. Actions закреплены по commit SHA, checkout не
сохраняет токен. PR CI имеет read-only права, `pull_request_target` отсутствует.
Workflow предложений работает только на `main`, не пишет рабочий YAML и не делает
auto-merge. Крупный отчёт передаётся GitHub API через файл, не через аргументы оболочки.
Для обязательного ручного слияния включите защиту `main` от прямых push и обхода правил.
Для PR, созданных `GITHUB_TOKEN`, GitHub может запросить **Approve workflows to run**.

Источник ограничен 64 MiB и таймаутом 60 секунд; редиректы запрещены; 429/5xx повторяются
до 4 раз. Steam запрашивается пакетами до 100 ID, Microsoft Store — до 50, steamcmd.net — по одному ID в 4 потока (~7 минут). Ошибка сети, неполный пакет, изменённая обязательная схема
или неподдержанный синтаксис RoscomVPN останавливают генерацию до записи отчёта/YAML.
YAML-парсер поддерживает только проверенный plain-scalar формат PROCESS-NAME/REGEX;
неизвестные правила не пропускаются молча. Данные не исполняются как код.

```sh
go test ./...
go vet ./...
# Сохранить снимки классификации Steam, Microsoft Store и Steam PICS:
go run ./cmd/catalog -save-steam /path/to/steam.json -save-xbox /path/to/xbox.json -save-pics /path/to/pics.json
# Повторить сбор без сети на тех же источниках (-no-pics пропускает PICS):
go run ./cmd/catalog -discord-file /path/to/discord.json -roscom-file /path/to/games.yaml -steam-file /path/to/steam.json -xbox-file /path/to/xbox.json -pics-file /path/to/pics.json
```

Store Browse, display catalog, steamcmd.net и Discord endpoint проверены живыми запросами, но не обещают стабильный
публичный контракт. При изменении формата сначала исправляются адаптер и тесты.
