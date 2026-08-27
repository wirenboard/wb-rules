# ADR-006: Top-level await через обёртку `async function F(module, exports)` + инспекция промиса

**Статус:** Принято, реализовано
**Дата:** 2026-08-17

## Контекст

Файл правил исполняется как тело функции-обёртки (CommonJS-стиль: `module`, `exports`).
При синхронной обёртке `var v = await sleep(100)` на верхнем уровне — это `SyntaxError`, а
TS-чекер ([ADR-012](ADR-012-background-check-parameters-checkjs.md)) предлагает «сделать файл модулем» (`export {}`), что для файла правил
бессмысленно. Top-level `await` — естественная форма линейных сценариев ([ADR-007](ADR-007-promise-native-lib-js.md)), и его
семантика должна быть окончательной с первого релиза движка: миграция пользовательских файлов с
идиомы `(async () => {...})()` на TLA и обратно дороже, чем одна доработка до релиза.

## Решение

В `wbrules/escontext.go: LoadScenario`:
1. Файл **всегда** оборачивается в `"async function F(module, exports){" + prologue + src + "\n}"`
   (обёртка на той же строке — номера строк сохраняются).
2. Вызов через `PcallNoPump(2)` — без дренажа очереди.
3. Инспекция состояния результата **до** pump'а (`PromiseStateTop()`):
   - `PromiseRejected` ⇒ синхронная ошибка загрузки файла: `RetractTopPromiseRejection()` (чтобы
     трекер [ADR-005](ADR-005-microtask-pump-rejection-tracker.md) не продублировал «async rule error»), `PushPromiseResultTop` → `ESError` →
     `LocFileEntry.Error`;
   - `PromisePending` ⇒ настоящий TLA: файл считается загруженным, продолжение выполнится позже;
   - `PromiseFulfilled` ⇒ готово.
4. Затем `PumpJobs()`.
5. В TS-проверке — `--module esnext --moduleDetection force`, чтобы TLA не ругался.

Для этого в шим добавлен split `Pcall` → `PcallNoPump` + `PumpJobs`, `PromiseStateTop`,
`PushPromiseResultTop`, `RetractTopPromiseRejection`.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Только идиома `(async () => {...})()` | zero-risk, уже работала, но авторы правил ожидают от современного JS именно top-level `await`, и идиома противоречила TS-проверке |
| Только ослабить TS-check | хуже: зелёная проверка на файле, который не загружается |
| «Сначала sync-обёртка, при ошибке компиляции — async» | двойная компиляция, два набора семантик ошибок; «всегда async + инспекция» проще и предсказуемее |
| Настоящие ES-модули (`import`/`export`) | отдельная большая тема (загрузчик, `require`-совместимость) — сознательно не решено, см. §9 «что не решено» |

## Последствия

Плюсы:
- `await` на верхнем уровне работает; ошибка синхронной части файла по-прежнему ошибка загрузки
  (ретракт дублирования в лог).
- Поведение зафиксировано до первого релиза движка.

Минусы и риски:
- Async-обёртка превращает синхронные `throw` в rejection'ы — нивелировано инспекцией.
- `module.exports`, выставленный после TLA, не виден синхронному `require` (редкий случай).
- `moduleDetection: force` делает TS-фикстуры модулями ⇒ аугментация `WbControls` через
  `declare global` ([ADR-014](ADR-014-live-controls-registry-wbcontrols.md)).
- Правила, объявленные после `await`, регистрируются из promise job — корректно благодаря
  `__wbBindRealmAPI` ([ADR-004](ADR-004-one-runtime-realm-per-file.md)).

## Ссылки

- [ADR-003](ADR-003-go-duktape-compatible-shim.md), [ADR-004](ADR-004-one-runtime-realm-per-file.md), [ADR-005](ADR-005-microtask-pump-rejection-tracker.md), [ADR-012](ADR-012-background-check-parameters-checkjs.md), [ADR-014](ADR-014-live-controls-registry-wbcontrols.md).
- `wbrules/escontext.go` (`LoadScenario`), `internal/quickjsduk/duktape.go`
  (`PcallNoPump`, `PromiseStateTop`, `RetractTopPromiseRejection`), `wbrules/rule_tla_test.go`,
  `wbrules/testrules_tla.js`.
- PR wirenboard/wb-rules#223.
