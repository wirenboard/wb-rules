# ADR-015: Редактор homeui — TS language service в браузере + вердикт контроллера через `Editor.Check`/`GetTypes`

**Статус:** Принято, реализовано (homeui#1202)
**Дата:** 2026-08-14

## Контекст

Результат проверки типов должен быть виден в UI, а не только в логе; редактор должен
использовать типы wb-rules для подсказок; часть валидации можно выполнять прямо в редакторе.
Нужны одновременно мгновенные подсказки при наборе и «истина» от контроллера (его версия d.ts,
его реестр контролов, его tsgo), причём диагностика должна стоять у строки, а не в баннере над
редактором, и доставляться штатным API (RPC), а не side-channel-топиком. Отдельный вопрос —
нужна ли редактору собственная (vendored) копия `wb-rules.d.ts`, если контроллер может отдать
свою.

## Решение

- **В браузере**: CodeMirror 6 + `typescript` + `@typescript/vfs` + `@valtown/codemirror-ts`
  (lazy-chunk ~1 MB gzip, грузится при открытии редактора), для `.ts` **и** `.js` (allowJs/checkJs,
  `strict:false`), completions/hover/lint. Типы — из `Editor.GetTypes` (d.ts установленного
  контроллера; `Promise.race` с таймаутом 3 с); vendored-копия d.ts — только fallback для старых
  прошивок и build-time генерации completions. Реестр контролов — из `devicesStore` ([ADR-014](ADR-014-live-controls-registry-wbcontrols.md)).
- **С контроллера**: новый RPC `Editor.Check(path)` → `{status: ready|pending|not-ts|unsupported,
  diags[{file?, line, column, severity, message, code}]}` — читает **кэш** фоновой проверки
  ([ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md)/012), не запускает tsgo; homeui опрашивает пока `pending` с нарастающим интервалом (700 мс → 2 с, около минуты), на
  open/save. Рендер — инлайн squiggles/lint gutter с dedup против локальных диагностик.
- `valtown`-фильтр `keepLegacyLimitationForAutocompletionSymbols:false` — иначе прятал весь API
  wb-rules.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Retained-топик `/wbrules/ts-check/<file>` с вердиктом | 4 пути утечки (stale после рестарта, не чистится при delete/rename/disable), не часть API (нет в `asyncapi.mqtt-rpc.yml`), один потребитель |
| Баннер над редактором | неудобно, не привязан к строке — диагностика должна стоять inline у строки |
| Статический список completions из vendored d.ts | устаревает относительно контроллера; понижен до fallback |
| Только серверная проверка | нет мгновенной обратной связи при наборе |
| Только браузерная проверка | см. [ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md) (ssh/scp-воркфлоу) |
| `Editor.Check` запускает tsgo синхронно | блокирует RPC-диспетчер (serial dispatch `MQTTRPCServer`) на время проверки; чтение кэша фоновой проверки — мгновенно |

## Последствия

Плюсы:
- Подсказки и ошибки видны сразу; вердикт контроллера — источник истины, отображается там же.
- `.js` получают тот же сервис, что `.ts`.

Минусы и риски:
- Version skew: TS 6.0.3 в браузере против tsgo 7.1-dev на контроллере — возможны расхождения
  (excess-property checks, freshness, [ADR-013](ADR-013-type-system-wb-rules-dts.md)).
- +1 MB lazy-chunk — заметно с телефона; причина отказа от ESLint ([ADR-016](ADR-016-custom-forgot-await-diagnostics-not-eslint.md)).
- Устаревшие вердикты после правки — gate по `checkedContent`; CM6 `forceLinting()` — no-op без
  очереди ⇒ отдельный lint-refresh effect; фиксированные `setTimeout(1500|2000)` после
  Rename/ChangeState (нет сигнала «reload finished»).
- homeui `Recommends: wb-rules (>= 3.0.0~~)`: на старом движке `GetTypes` → fallback,
  `Check` → `unsupported`.

### Риски и технический долг

- Риск: TS-чанк ~1 МБ gzip (lazy) — медленно с телефона/слабой сети; измерить время до интерактивности на типовом смартфоне.
- Долг: фиксированные `setTimeout(1500/2000)` после `Rename`/`ChangeState`/`copyRule` — нет сигнала «reload finished» (RPC должен отвечать после перезагрузки или событие `/wbrules/events/files`).

## Ссылки

- [ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md), [ADR-012](ADR-012-background-check-parameters-checkjs.md), [ADR-013](ADR-013-type-system-wb-rules-dts.md), [ADR-014](ADR-014-live-controls-registry-wbcontrols.md), [ADR-016](ADR-016-custom-forgot-await-diagnostics-not-eslint.md).
- `wbrules/editor.go` (`Check`, `GetTypes`, `EDITOR_ERROR_*`), `wbrules/esengine.go` (`CheckTsFile`,
  `TsTypesContent`), `asyncapi.mqtt-rpc.yml`; homeui `frontend/src/pages/rules/[rule]/edit-rule.tsx`,
  `stores/rules/*`, `autocomplete/ts-language-service.ts`, `controller-diagnostics.ts`, `lint-refresh.ts`.
- homeui#1202; PR wirenboard/wb-rules#224.
