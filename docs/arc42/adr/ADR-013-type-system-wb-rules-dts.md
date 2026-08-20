# ADR-013: Система типов `types/wb-rules.d.ts` — заимствования из wb-mirta, English-only

**Статус:** Принято, реализовано
**Дата:** 2026-08-16

## Контекст

Прямое переложение README в `wb-rules.d.ts` — `any`-типы, `dev` как `Record<string, any>`, опции
контролов без связи с типом — не ловит ни одной реальной ошибки. Проект wb-mirta (`@mirta/globals`,
лицензия Unlicense) демонстрирует техники, дающие реальную проверку того же API: маппинг типов
контролов → TS-типов, discriminated union для опций, брендированные идентификаторы. Заимствуются
только техники типизации, не документация: комментарии и JSDoc в d.ts — только на английском
(README остаётся русским).

## Решение

`types/wb-rules.d.ts` (~800 строк; ставится в `/usr/share/wb-rules/types/wb-rules.d.ts`,
отдаётся редактору через `Editor.GetTypes`, [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)):

- `TypeMappings` (27 типов контролов) → `CellType`, `CellValue`.
- `ControlOptions` — discriminated union, сверенный с `fillControlArgs` (`wbrules/engine.go`): `units` только у
  `value`, `precision` у `value` и `range`, `enum` у `value`/`text` с `Record<lang, string>` (движок
  **молча отбрасывает** plain-string заголовки); запрещённые опции объявлены `?: never`, потому что
  TS 6.0 не делает excess-property check через generic inference, а tsgo 7.1 делает — `never`
  работает в обеих.
- `CustomControlOptions` — ветка для вендорских/кастомных типов вне `TypeMappings`: рантайм
  (`fillControlArgs`) принимает любую строку типа и трактует значение как строку, поэтому
  декларации принимают `{ type: string & {}, value: string }` (экстра-опции `?: never`), не
  ослабляя discriminated-union ошибок известных типов; `setType` тоже принимает любую строку.
- Типизированный `defineVirtualDevice<S>` → `VirtualDevice<TCells>` / `VirtualDeviceControl<TType>`;
  типизированные перегрузки `getControl` (и у устройства, и глобальная по реестру) — честно
  `| undefined`: контрол может исчезнуть к моменту вызова.
- Брендированный `RuleId`: `enableRule/disableRule/runRule` принимают только его — старая
  сигнатура `name: string` никогда не работала.
- `changed<K>`, `getDevice/getControl` честно `| undefined`.
- `declare module "*"` — чтобы `require` любых модулей не резолвился на диске (иначе в `.js`
  TS трактует ambient `require` как CommonJS-импорт, [ADR-012](ADR-012-background-check-parameters-checkjs.md)).
- `PersistentStorage` callable **и** constructible (оба стиля встречаются в системных правилах).
- Пустой `interface WbControls {}` как цель declaration merging для живого реестра ([ADR-014](ADR-014-live-controls-registry-wbcontrols.md)).
- `dev` — mapped type `{ [K in keyof WbControls | (string & {})]: ... }`.
- Тот же файл вендорится в homeui (`autocomplete/wb-rules.d.ts`, `globals-generated.ts`) —
  синхронизируется вручную.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Оставить README-стиль d.ts (`any`) | не ловит ни одной реальной ошибки; бессмысленна вся проверка |
| Типизировать только через control-объекты (подход Mirta) | выбран реестр строковых ссылок ([ADR-014](ADR-014-live-controls-registry-wbcontrols.md)): homeui и драйвер уже знают все device/control/type; стиль `dev["a/b"]` — подавляющий в корпусе |
| Codegen типов из `defineVirtualDevice` | не потребовался: `defineVirtualDevice<S>` типизируется inference'ом |
| Русскоязычные JSDoc в d.ts | отклонено: d.ts — англоязычный артефакт (hover в редакторе, внешние IDE) |
| Публикация d.ts как npm-пакета | не требуется на этом этапе; источник истины — файл в пакете wb-rules, редактор получает его с контроллера |

## Последствия

Плюсы:
- Проверка ловит реальные ошибки: неверный тип значения, несуществующие опции контрола,
  `enableRule("name")`.
- Completions/hover в редакторе ([ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)) построены на том же файле.

Минусы, риски, долги:
- `const` type params и freshness ненадёжны между TS 6.0 и tsgo 7.1.
- Пробелы: `then`-параметры `any`.
- Ручная синхронизация копии в homeui.
- Контракт защищён тестом `wbrules/testrules_ts_typeassert.ts` под `--strict false` и `true`.

### Риски и технический долг

- Риск (version skew): браузер — `typescript` 6.0.3, контроллер — tsgo 7.1-dev; excess-property checks и `const` type params ведут себя по-разному — `testrules_ts_typeassert.ts` пинит контракт под обе версии; синхронизировать мажор TS в homeui при обновлении tsgo.
- Долг: пробелы `wb-rules.d.ts` — `then` params `any`, `readConfig`/`require` → `any`, `RuleSpec._cron` публичен; d.ts синхронизируется в homeui вручную (генерировать vendored-копию из wb-rules).
- Долг: plain-string заголовки enum молча отбрасываются движком (d.ts запрещает, движок — нет) — автооборачивать `{en: str}` или явная ошибка.

## Ссылки

- [ADR-007](ADR-007-promise-native-lib-js.md) (типы async API), [ADR-012](ADR-012-background-check-parameters-checkjs.md), [ADR-014](ADR-014-live-controls-registry-wbcontrols.md), [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md).
- `types/wb-rules.d.ts`, `wbrules/esengine.go` (`fillControlArgs`, `TsTypesContent`),
  `wbrules/testrules_ts_typeassert.ts`, homeui `frontend/src/.../autocomplete/wb-rules.d.ts`.
- Отчёт `wb-mirta-core-report.md`; PR wirenboard/wb-rules#224; homeui#1202.
