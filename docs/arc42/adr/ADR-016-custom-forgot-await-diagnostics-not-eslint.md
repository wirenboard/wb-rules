# ADR-016: Кастомные диагностики «forgot await» вместо ESLint в браузере

**Статус:** Принято, реализовано (homeui#1202)
**Дата:** 2026-08-18

## Контекст

Типовая async-ошибка — забытый `await`: `while (sleep(1000)) {}` или `for(;;){sleep(1000);}` —
синхронный бесконечный цикл, который блокирует движок ([ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md)). Редактор должен предупреждать о
нём до сохранения, но `tsc` не имеет диагностики floating-promise, а TS2801 (Promise в условии)
срабатывает только при `strict` и только в `if`. Нужна защита от типичных async-ошибок в
редакторе ([ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)), соразмерная по стоимости — очевидный кандидат ESLint дорог в браузере.

## Решение

Четыре AST-проверки поверх `getSemanticDiagnostics` в языковом сервисе homeui
(`autocomplete/ts-language-service.ts`), коды 990001–990004, каждая в `try/catch`:

1. Promise как условие (`if (sleep(…))`, `while (changed(…))`).
2. Floating Promise **только** в бесконечном цикле (`for(;;)`, `while(true)`) — именно такой цикл
   блокирует движок.
3. `await` не-Promise (union-aware; пропускает `any`/`unknown`/type params).
4. Promise, присваиваемый в `dev[...] =`.

UX, не зависящий от наведения мыши: lens после строки + lint gutter + панель «N problems».
Runtime-ошибки из `/wbrules/log/error` с `path:line` (в т. ч. «write ignored … at file:line»,
[ADR-009](ADR-009-mistyped-control-write-log-not-throw.md), и «async rule error», [ADR-005](ADR-005-microtask-pump-rejection-tracker.md)) — тоже lint-диагностики у строки.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| ESLint + typescript-eslint в браузере | +2–4 MB к lazy-chunk (UI часто открывают с телефона; уже есть ~1 MB TS, [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)); ручная сборка `Linter` + parser в браузере («use at your own risk»); пересчёт type-aware правил на каждое нажатие; version skew ESLint ↔ typescript-eslint ↔ TS 6.0.3; `no-floating-promises` по умолчанию ругается на благословлённые `scenario();` и `(async()=>{})()` ⇒ та же ручная настройка |
| ESLint на контроллере | tsgo — tsc-based, без lint; Node + ESLint на контроллер — не вариант ([ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md)) |
| `strict: true` ради TS2801 | поток ложных ошибок ([ADR-012](ADR-012-background-check-parameters-checkjs.md)) и покрывает только `if` |
| Ругаться на любой floating Promise | ложные срабатывания на ограниченных циклах параллельного диспатча (`for (z of zones) { runShellCommand(...) }`) — легитимная идиома; опасен только бесконечный цикл |

Пересмотреть, если понадобится полноценная lint-платформа (пользовательские правила стиля,
массовые проверки).

## Последствия

Плюсы:
- Нулевая стоимость по размеру бандла; проверки прицельно закрывают реальные классы ошибок.
- Единый список проблем: статические (локальные + контроллер) и runtime — в одном месте редактора.

Минусы и риски:
- Проверки editor-only: на контроллере (ssh/scp) их нет — там защищает watchdog ([ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md)).
- Самописный AST-код требует сопровождения при обновлении TS в браузере.
- Нет общего механизма подавления, кроме `// @ts-nocheck`/`@ts-ignore` для TS-диагностик.

### Риски и технический долг

- Риск: ESLint добавил бы ещё 2–4 МБ к TS-чанку — причина отказа; кастомные проверки требуют ручного расширения под новые идиомы.

## Ссылки

- [ADR-005](ADR-005-microtask-pump-rejection-tracker.md), [ADR-007](ADR-007-promise-native-lib-js.md), [ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md), [ADR-009](ADR-009-mistyped-control-write-log-not-throw.md), [ADR-012](ADR-012-background-check-parameters-checkjs.md), [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md).
- homeui `frontend/src/.../autocomplete/ts-language-service.ts` (коды 990001–990004),
  `controller-diagnostics.ts`, `runtime-errors.ts`, `lint-refresh.ts`.
- homeui#1202.
