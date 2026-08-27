# ADR-012: Параметры фоновой проверки — батчи, `--strict false`, `--lib esnext`, `--moduleDetection force`, пропуск `.d.ts`, checkJs как advisory

**Статус:** Принято, реализовано
**Дата:** 2026-08-13 (параметры проверки), 2026-08-18 (checkJs)

## Контекст

Фоновая проверка типов ([ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md)) должна быть (а) дешёвой на контроллере, (б) не ругаться на
легитимный код правил (TLA, `require`, глобалы lib.js), (в) ловить реальные ошибки вроде
`dev["buzzer/enabled"] = 123`. Последнее особенно важно для `.js`: большинство правил написано
на JavaScript, и без `checkJs` именно они остаются без проверки — при том, что тот же tsgo умеет
проверять JS против тех же деклараций. Отдельно надо было убедиться, что включение проверки не
меняет разбор `.js` (например, `foo < b > (c)` остаётся сравнением, а не generic call).

## Решение

Командная строка `wbrules/tsloader.go: checkMany`:
`tsgo --noEmit --pretty false --target esnext --lib esnext --strict false --module esnext --moduleDetection force
--allowJs --checkJs <paths…> /usr/share/wb-rules/types/wb-rules.d.ts /tmp/wb-controls-*.d.ts`

- **Батчинг**: запросы в течение `checkBatchDelay` = 300 мс собираются в одну программу
  (WB8: 0.27 с/файл против 0.34 с за 20 файлов). Повтор без файлов с синтаксической ошибкой —
  любая TS1xxx отключает семантику всей программы.
- **`--strict false`** (и на контроллере, и в редакторе homeui): под стиль существующего
  нестрогого кода правил; `strictNullChecks` завалил бы существующие правила null-ошибками.
  Типовой контракт пинится `testrules_ts_typeassert.ts` под `--strict false` **и** `true`.
- **`--lib esnext`** (без DOM): DOM-глобалы вроде `history` конфликтовали с именами в правилах.
- **`--module esnext --moduleDetection force`**: ради top-level await ([ADR-006](ADR-006-top-level-await.md)).
- **Пропуск `.d.ts`**: `transpileModule` в tsgo паникует на них; `/usr/share/wb-rules` — watched dir,
  где лежит `types/wb-rules.d.ts`.
- **checkJs для `.js`** (`--allowJs --checkJs`; в редакторе `checkJs:true`): диагностики для `.js` —
  **warning**; sloppy-JS коды `sloppyJsCodes = {2362, 2363, 2410, 2703}` отбрасываются; грамматические
  (1121 octal, 1108 top-level return) оставлены — ошибка разбора отключает семантическую проверку всей программы, и автор должен видеть причину; `// @ts-nocheck` —
  opt-out. `.js` по-прежнему грузится без транспиляции и без tsgo. Безопасность: TS парсит по
  расширению — `.js` как JS (`(foo<b)>c`), `.ts` как generic call; checkJs включает только
  репортинг, не меняет парсинг.
- Лимит вывода: ≤10 строк `TS check:` на файл в лог правил; полный список — через `Editor.Check`.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Проверять по одному файлу | на WB8 в ~16 раз дороже при массовой загрузке (0.27 с × N) |
| `--strict true` | поток ложных ошибок в существующем loose-коде |
| `--lib dom` / default lib | ложные конфликты имён; правила не браузерный код |
| Не проверять `.js` | большинство правил — `.js`; именно там `dev[...] = 123` остаётся незамеченным |
| checkJs как error | `.js`-файл грузится и работает независимо от диагностик — честнее warning |
| Показывать все диагностики в логе | шум в journal/консоли; лог — сигнал «посмотри в редактор» |

## Последствия

Плюсы:
- Реальные ошибки типов контролов ловятся и в `.js`; нагрузка на контроллер ограничена.

Минусы и риски (шум принят осознанно):
- 172/508 чисто загружающихся файлов корпуса получают ≥1 диагностику (892× TS2304 implicit globals).
- Системные правила тоже получают диагностики; часть из них — пробелы d.ts (`declare module "*"`:
  в `.js` TS трактует ambient `require` как CommonJS-импорт и резолвит его на диске;
  `PersistentStorage/StorableObject` как callable+constructible, [ADR-013](ADR-013-type-system-wb-rules-dts.md)), часть — реальные
  недочёты скриптов (wb-rules-system #65, wb-scenarios #98). Ограничение: системные скрипты
  обязаны оставаться ES5, пока stable 2.46.x работает на Duktape 1.0.2 (нет `Math.trunc/sign`).
- Поведение чекера зависит от версии tsgo (excess-property checks различаются между TS 6.0 и tsgo 7.1).

### Риски и технический долг

- Риск: checkJs шумит на legacy `.js` (172/508 файлов корпуса, 892× TS2304) — пользователи могут начать игнорировать все предупреждения; митигация: advisory-уровень, шумовые коды отброшены, `// @ts-nocheck`, счётчик «N problems» и панель в редакторе ([ADR-016](ADR-016-custom-forgot-await-diagnostics-not-eslint.md)), объяснение в README.

## Ссылки

- [ADR-006](ADR-006-top-level-await.md), [ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md), [ADR-013](ADR-013-type-system-wb-rules-dts.md), [ADR-014](ADR-014-live-controls-registry-wbcontrols.md), [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md).
- `wbrules/tsloader.go` (`checkBatchDelay`, `sloppyJsCodes`, `checkMany`, `tsDiagRx`),
  `wbrules/esengine.go` (`scheduleTsCheck`, `TS_CHECK_*`), `wbrules/testrules_ts_typeassert.ts`,
  `wbrules/testrules_ts_badtypes.ts`.
- PR wirenboard/wb-rules#224; wb-rules-system#65; wb-scenarios#98; homeui#1202.
