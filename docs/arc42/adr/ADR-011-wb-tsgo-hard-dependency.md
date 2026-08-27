# ADR-011: TypeScript — обязательная зависимость; отдельный пакет и репозиторий `wb-tsgo`, бинарь `/usr/bin/tsgo`

**Статус:** Принято, реализовано
**Дата:** 2026-08-15

## Контекст

Выбрав tsgo как внешний процесс ([ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md)), нужно было решить, как он попадает на контроллер:
из какого репозитория собирается, как называется бинарь, и обязателен ли он для wb-rules.
Три вопроса решались вместе: является ли tsgo полноправным инструментом на контроллере или
внутренней деталью wb-rules; как назвать немодифицированный бинарь; допустима ли работа
wb-rules без компилятора (TS как опция с предупреждением) или TypeScript — штатная часть движка.

На последний вопрос отвечает порядок установки: при `Recommends: wb-tsgo` dpkg конфигурирует
wb-rules раньше wb-tsgo, сервис стартует и пробует компилятор, которого ещё нет, и все `.ts`
падают до следующего рестарта. «Мягкая» зависимость даёт недетерминированное поведение
на каждой установке.

## Решение

- Отдельный репозиторий `wirenboard/wb-tsgo`, собирающий **немодифицированный** typescript-go
  (go.mod pin `v0.0.0-20260813172707-3d981a41cced`, «7.1.0-dev»; pure Go, `CGO_ENABLED=0`,
  `GOARM=6` для armhf, lintian override `statically-linked-binary`). Пакет `wb-tsgo`, бинарь
  `/usr/bin/tsgo`.
- `debian/control` wb-rules: `Depends: ${shlibs:Depends}, ${misc:Depends}, wb-tsgo` (не Recommends).
- Флаг `-tsgo` (default `/usr/bin/tsgo`); `-tsgo ""` отключает TS (для тестов/отладки).
- Устойчивость к порядку установки в три слоя: (1) `Depends` — порядок configure; (2)
  per-operation `Available()` (stateless `LookPath`, без кэширования «нет бинаря»); (3)
  `tracker.Untrack` при неудачной загрузке `.ts` — иначе повторная загрузка того же содержимого
  пропускалась бы как «not modified».

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Второй бинарный пакет из исходников wb-rules | смешивает релизные циклы движка и компилятора; усложняет Jenkins-пайплайн (`buildDebGolangWbgo`) |
| Бинарь `/usr/lib/wb-rules/tsgo` или имя `wb-tsgo` | бинарь не модифицирован — должен называться `tsgo` и быть доступен как инструмент («first-class citizen») |
| `Recommends` + graceful degradation | порядок configure не гарантирован — `.ts` не работают после установки до рестарта; TypeScript — штатная часть движка, а не опция |
| Pipeline-артефакт Jenkins, подкладываемый при сборке wb-rules | непрозрачная версия, нет отдельного пакета для обновления |
| Связывать tsgo в Go-библиотеку | см. [ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md) |

## Последствия

Плюсы:
- Один источник истины о версии компилятора; обновляется независимо от wb-rules.
- Гарантированный порядок установки; `.ts` работают сразу после установки.

Минусы и риски:
- +1 пакет (~28 MB статически слинкованного бинаря) на всех контроллерах, даже без `.ts`.
- На armhf (WB6/7) tsgo не проверялся по скорости.
- homeui зависит от wb-rules через `Recommends: wb-rules (>= 3.0.0~~)` (не Depends):
  новый UI на старом движке деградирует — `GetTypes` fallback, `Check` → `unsupported` ([ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)).
- Тесты `rule_no_tsgo_test.go` (NoTsgo/LateTsgo) фиксируют поведение при отсутствии/позднем
  появлении бинаря.

### Риски и технический долг

- Риск: права на релиз `wb-tsgo` — отдельный репозиторий и пакет, без него wb-rules не устанавливается (`Depends`); нужен закреплённый владелец репо, сборка в Jenkins и описанная процедура обновления (пин → сборка → testing set).

## Ссылки

- [ADR-010](ADR-010-tsgo-external-process-run-first-check-later.md), [ADR-012](ADR-012-background-check-parameters-checkjs.md), [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md).
- `debian/control`, `main.go` (`-tsgo`), `wbrules/tsloader.go` (`Available`),
  `wbrules/esengine.go` (`preprocessRuleSource` — сообщение «TypeScript compiler not found at … wb-tsgo»),
  `wbrules/rule_no_tsgo_test.go`.
- Репозиторий wirenboard/wb-tsgo; PR wirenboard/wb-rules#224.
