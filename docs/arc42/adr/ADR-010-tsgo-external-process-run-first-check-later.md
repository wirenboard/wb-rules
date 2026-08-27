# ADR-010: tsgo (Microsoft typescript-go) как внешний процесс; «run first, check later»

**Статус:** Принято, реализовано
**Дата:** 2026-08-13

## Контекст

Поддержка TypeScript в правилах требует двух разных операций: транспиляции `.ts` → JS
(на критическом пути загрузки файла) и проверки типов (медленной, информационной). Требование:
скорость загрузки не должна страдать — сначала транспиляция и запуск, проверка потом, медленные
проверки никогда не задерживают исполнение. На контроллере нет Node; рантайм — Go + QuickJS.
Основной выбор — между внешним бинарём компилятора и библиотекой, слинкованной в wb-rules.

## Решение

- Компилятор — `tsgo` (typescript-go, Go-порт TypeScript 7), внешний бинарь `/usr/bin/tsgo`
  (пакет `wb-tsgo`, [ADR-011](ADR-011-wb-tsgo-hard-dependency.md)). Всё за интерфейсом `TSCompiler` в `wbrules/tsloader.go`.
- **Транспиляция**: персистентный дочерний процесс `tsgo --api --async` (LSP-framed JSON-RPC,
  метод `transpileModule`, target ESNext, sourceMap); ~0.3–1.3 мс на файл «тёплым». Файл
  запускается сразу. Source map V3 → `lineMaps[path]`, строки `.ts` в трейсбеках
  (`TranslateLine`); синтаксическая ошибка → `TSSyntaxError{Line}` → ошибка загрузки у строки `.ts`.
- **Проверка**: транзиентный процесс `tsgo --noEmit --pretty false --target esnext --lib esnext --strict false
  --module preserve --moduleDetection force --esModuleInterop --allowJs --checkJs <files> wb-rules.d.ts
  /tmp/wb-controls-*.d.ts` в фоне (`checkSem` = 2 конкурентных, 60 с таймаут, `Pdeathsig=SIGKILL`),
  результат — `TS check: file:line:col: msg` в лог правил (≤10 строк/файл) + кэш для `Editor.Check`
  ([ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)). Детали параметров — [ADR-012](ADR-012-background-check-parameters-checkjs.md).
- Устойчивость: `Available()` = stateless `LookPath` на каждую операцию; respawn child только при
  транспортной ошибке; 15 с I/O watchdog; guard по тексту на TS5112/TS6053 (ошибки конфигурации/отсутствующего файла без позиции — по коду возврата их не отличить от «нет диагностик»).

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Линковать typescript-go как Go-библиотеку | весь компилятор под `internal/` (ограничение Go toolchain). Форк с экспортирующей обёрткой: сопровождение форка быстро движущегося компилятора, Go ≥ 1.26 навсегда для wb-rules, потеря изоляции (краш/OOM чекера убьёт движок правил), ~28 MB вне RSS wb-rules, когда чекер не работает |
| `tsc` на Node | второй рантайм на контроллере (89+ MB флеша, ~50 MB RSS); всерьёз не обсуждался |
| esbuild / swc | только транспиляция, без проверки типов; подробно не рассматривались |
| `tsc` (typescript.js) исходником внутри самого QuickJS | измерено на WB8: eval бандла 8.8 MB — 4.0 с на каждый старт движка, transpile 52 мс против ~1 мс tsgo, полный check 9.3 с cold / 3.0 с с кэшем lib, heap 42 MB внутри процесса правил. Тормозной на всех операциях — не подходит |
| то же, но скомпилированный в байткод QuickJS (`.qbc`) | лечит только старт (eval 287 мс вместо 4.0 с) — исполнение остаётся интерпретируемым, transpile и check не ускоряются ни на миллисекунду. И выигрыша в размере нет: `.qbc` — 28.2 MB, столько же или больше, чем весь отдельный бинарь tsgo (27.0 MB arm64 / 28.3 MB amd64) |
| Проверка только в браузере (homeui) | значительная часть правил пишется и заливается вне редактора (ssh/scp, внешние IDE) — для них строка `TS check:` в journal/консоли единственная обратная связь |

Против обоих вариантов с typescript.js есть и стратегический аргумент: Microsoft явно
объявил, что компилятор TypeScript переезжает на Go — typescript-go и есть будущий TS 7
(официальный нативный порт, анонс «A 10x Faster TypeScript»). Встраивать и сопровождать
в прошивке «старый» JS-tsc значит привязаться к ветке, которую upstream сворачивает.

## Последствия

Плюсы:
- Загрузка `.ts` практически не медленнее `.js`; проверка не блокирует исполнение.
- Изоляция: падение/OOM чекера не трогает движок; память чекера не в RSS wb-rules.
- Решение обратимо (один интерфейс `TSCompiler`).

Минусы и риски:
- Внешний бинарь и отдельный пакет — порядок установки, доступность ([ADR-011](ADR-011-wb-tsgo-hard-dependency.md)).
- Дочерние процессы переживают аварийную смерть родителя — отсюда `Pdeathsig=SIGKILL`.
- tsgo печатает cwd-относительные пути и UTF-16-оффсеты; `.d.ts` нужно пропускать
  (transpileModule паникует; `/usr/share/wb-rules` — watched dir).
- На armhf (WB6/7) скорость tsgo не измерена.
- Version skew: tsgo 7.1-dev на контроллере против TS 6.0.3 в браузере ([ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)).

### Риски и технический долг

- Риск: зависимость от версии tsgo (typescript-go 7.1-dev): API `--api` нестабилен, обновление может сломать LSP-framing и формат диагностик (`path(line,col): error TSnnnn`), ошибки без позиции (TS5112/TS6053) неотличимы по коду возврата от чистого прогона — митигация: пин в `go.mod` wb-tsgo, guard по тексту вывода, всё за `TSCompiler`, тесты `TestRuleTypeScript*`; предлагается smoke-тест wb-rules в CI wb-tsgo перед релизом.

## Ссылки

- [ADR-011](ADR-011-wb-tsgo-hard-dependency.md), [ADR-012](ADR-012-background-check-parameters-checkjs.md), [ADR-013](ADR-013-type-system-wb-rules-dts.md), [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md).
- `wbrules/tsloader.go` (`TSCompiler`, `Transpile`, `CheckAsync/checkMany`, `CheckAsync`),
  `wbrules/esengine.go` (`preprocessRuleSource`, `scheduleTsCheck`), `wbrules/rule_typescript_test.go`,
  `wbrules/rule_no_tsgo_test.go`, `main.go` (`-tsgo`, `-ts-types`).
- PR wirenboard/wb-rules#224 (quickjs-ts), репозиторий wirenboard/wb-tsgo.
