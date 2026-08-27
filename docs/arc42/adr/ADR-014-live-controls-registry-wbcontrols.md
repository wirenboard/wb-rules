# ADR-014: Живой реестр `WbControls` — серверный (таблица драйвера) и клиентский (homeui devicesStore)

**Статус:** Принято, реализовано
**Дата:** 2026-08-17

## Контекст

Типизированный d.ts ([ADR-013](ADR-013-type-system-wb-rules-dts.md)) сам по себе не закрывает главную дыру: строковые ссылки на контролы —
`getControl("dev/ctrl")` и `dev["dev/ctrl"]`, основной стиль правил в корпусе, — остаются `any`,
и `dev["buzzer/enabled"] = 123` для переключателя проходит проверку. Реестр известных контролов
и их типов нужен и в браузере, и в фоновой проверке на контроллере ([ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md)). При этом набор
устройств/контролов известен только во время выполнения — из драйвера (таблица устройств wbgo)
и из homeui (`devicesStore.cells`).

## Решение

- В поставляемом `types/wb-rules.d.ts` — пустой `interface WbControls {}`; `dev` объявлен как
  mapped type `{ [K in keyof WbControls | (string & {})]: ... }`, `getControl<K extends keyof
  WbControls>`, `changed<K>`.
- **Серверный реестр**: `ESEngine.controlsRegistryDts()` / `renderControlsRegistry` строит
  `interface WbControls { "dev/ctrl": "type"; … }` из таблицы драйвера (пропуская `system__*`)
  и добавляет `declare var <имя>: any;` для каждого имени из `defineAlias`, чтобы алиасы не
  считались неизвестными идентификаторами; снимок берётся на engine loop, пишется во временный
  `/tmp/wb-controls-*.d.ts` и передаётся tsgo вместе с основным d.ts при каждой фоновой
  проверке ([ADR-012](ADR-012-background-check-parameters-checkjs.md)).
- **Клиентский реестр**: homeui `autocomplete/registry.ts: buildControlsRegistry(devicesStore)`
  генерирует виртуальный `/wb-controls.d.ts` (declaration merging) для языкового сервиса в браузере
  ([ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)), пропуская `isSystem` и неизвестные типы.
- Трюк `(string & {})`: перечисленные ключи типизированы на чтение и запись, остальные — `any`,
  без «& any»-отравления (plain string index signature портит известные ключи).
- **Пустой реестр = нестрогая проверка**: без данных о контролах все ссылки `any` — на контроллере
  без драйвера или в старой homeui ложных ошибок не возникает.
- Из-за `moduleDetection: force` ([ADR-006](ADR-006-top-level-await.md)) TS-фикстуры — модули, аугментация `WbControls` в них
  идёт через `declare global`.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Только control-объекты (`getControl(...)` → typed) без строкового реестра | `dev["a/b"]` — основной стиль корпуса; не закрывает главную дыру |
| Статический реестр, генерируемый при сборке/установке | набор устройств меняется в runtime (Modbus-устройства, Zigbee, vdev других файлов) |
| Реестр только в браузере | контроллерная проверка (ssh/scp-воркфлоу, [ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md)) осталась бы слепой |
| Plain string index signature в `dev` | ломает типы известных ключей (становятся `any`) |
| Строгая ошибка на неизвестный ключ | ложные срабатывания при временно отсутствующих устройствах; принято `any` |

## Последствия

Плюсы:
- `dev["buzzer/enabled"] = 123` ловится до исполнения и в редакторе, и в логе контроллера; дополняет
  runtime-лог [ADR-009](ADR-009-mistyped-control-write-log-not-throw.md).
- Completions по реальным именам устройств/контролов в редакторе.

Минусы и риски:
- Снимок реестра на момент проверки: контролы, созданные позже, не типизированы (остаются `any`).
- Временный файл в `/tmp` на каждую проверку; очистка — ответственность `checkMany`.
- Две реализации генератора (Go и TS) — риск расхождения фильтров (`system__*` vs `isSystem`).

### Риски и технический долг

- Риск: пустой реестр `WbControls` (контроллер без устройств, `system__*`) ⇒ любой `dev["x/y"]` — `any`, ложное чувство безопасности; документировано, реестр регенерируется на каждый `scheduleTsCheck`; предлагается предупреждение «контрол не найден» в редакторе.

## Ссылки

- [ADR-009](ADR-009-mistyped-control-write-log-not-throw.md), [ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md), [ADR-012](ADR-012-background-check-parameters-checkjs.md), [ADR-013](ADR-013-type-system-wb-rules-dts.md), [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md).
- `wbrules/esengine.go` (`controlsRegistryDts`, `renderControlsRegistry`), `wbrules/tsloader.go`
  (`checkMany`), `wbrules/rule_ts_registry_test.go`, `wbrules/testrules_ts_registry.ts`,
  `types/wb-rules.d.ts` (`WbControls`, `dev`); homeui `frontend/src/.../autocomplete/registry.ts`.
- PR wirenboard/wb-rules#224; homeui#1202.
