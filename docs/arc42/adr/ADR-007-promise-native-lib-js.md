# ADR-007: Промис-нативная библиотека в `lib.js` (sleep/changed/nextMqtt/spawn), не в Go

**Статус:** Принято, реализовано
**Дата:** 2026-08-15

## Контекст

С появлением промисов ([ADR-005](ADR-005-microtask-pump-rejection-tracker.md)) стандартной библиотеке правил нужны async-примитивы,
позволяющие писать сценарии линейно: пауза, ожидание изменения контрола, ожидание MQTT-сообщения,
запуск команды с `await`. Нужно было выбрать, где их реализовывать — в `lib.js` или в Go — и
зафиксировать семантику таймаутов: классический пример «детектор движения с задержкой
выключения» показывает, что наивные таймауты через `Promise.race` оставляют висящие таймеры.

## Решение

Реализовать в `scripts/lib.js` (realm-локально, внутри `__wbBindRealmAPI`, [ADR-004](ADR-004-one-runtime-realm-per-file.md)):

- `spawn(cmd, args, opts)` / `runShellCommand(cmd, opts)` → `Promise<{exitCode, capturedOutput,
  capturedErrorOutput}>`; callback-форма сохранена.
- `sleep(ms)` → `Promise<void>`.
- `changed(ctrl[, timeoutMs])` → `Promise<value>`: одно постоянное анонимное `whenChanged`-правило
  на контрол на файл + список waiter'ов; та же конвертация типов, что у `whenChanged`.
- `nextMqtt(topic[, timeoutMs])` → `Promise<{topic, value, retained, qos}>`: через `trackMqtt`, пропускает
  retained («next» = новое сообщение).
- Go получил одну правку: `esWbSpawn` доставляет `spawnError` через callback (иначе промису
  не за что зацепиться).

Семантика:
- Ненулевой exit **резолвит** (shell-style: `grep` с кодом 1 — не исключение); reject только если
  процесс не запустился.
- Таймауты — через **отменяемый `setTimeout`** (`clearTimeout` при успехе) и **splice waiter'а** из
  очереди по таймауту с reject. Не `Promise.race`.
- `__wbAsyncWaiters()` — диагностическая функция-канарейка для leak-тестов.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Реализация в Go через стек-API шима | нужны ручные promise capabilities и повторный вход в правильный realm — целый класс трудноуловимых багов атрибуции; поверхность шима заморожена ([ADR-003](ADR-003-go-duktape-compatible-shim.md)) |
| `dev.changed(...)` на `dev[]`-прокси | `dev[]` — самый горячий путь; общий прокси не знает realm вызывающего ⇒ лишний proxy-hop на каждый доступ либо потеря атрибуции файла |
| `Promise.race([p, sleep(t).then(throw)])` для таймаутов | при выигрыше `p` остаётся таймер, его rejection некому обработать ⇒ фантомный «async rule error» (ровно то, что трекает [ADR-005](ADR-005-microtask-pump-rejection-tracker.md)) |
| Без splice waiter'а по таймауту | массив waiter'ов на тихом топике растёт без предела — утечка, которую ловят leak-тесты |
| Reject при ненулевом exit | ломает shell-идиомы; противоречит семантике callback-формы |

## Последствия

Плюсы:
- ~40 строк читаемого JS с видимыми стек-фреймами; realm-корректность «почти бесплатно».
- Примеры README переписаны на `async/await` и стрелочные функции.

Минусы и риски:
- `changed` держит постоянное анонимное правило на контрол (очищается вместе с файлом) — это
  дополнительная подписка в DepTracker, не отражаемая в списке именованных правил файла.
- Регресс-тест `TestTimedOutWaitersReclaimed` event-gated, но остаётся чувствителен к нагруженной
  VM.
- Типы этих API входят в типизированный контракт API движка (ADR-013, PR #224).

### Риски и технический долг

- Долг: `spawn`, `runShellCommand`, `sleep`, `changed`, `nextMqtt`, `PersistentStorage`, `defineRule` реализованы дважды (top-level и внутри `__wbBindRealmAPI`) — дрейф реализаций; нужна одна фабрика для обоих мест.
- Долг: `spawn` без `exitCallback` предупреждает о ненулевом exit (нужен `{quiet:true}`); `xformat` исполняет код из строки формата — кандидат на deprecate.
- Долг: `Notify` остаётся колбэк-API поверх `curl`, `sendSMS`/`sendEmail` не сообщают об ошибках (SOFT-7065/7107) — промис-обёртки и fetch-подобный API в roadmap.
- Долг: `.disabled` распознаётся только как суффикс `foo.js.disabled`; инструменты сообщества используют `foo.disabled.ts` — принимать оба.

## Ссылки

- [ADR-004](ADR-004-one-runtime-realm-per-file.md), [ADR-005](ADR-005-microtask-pump-rejection-tracker.md), ADR-013, ADR-016 (диагностика «forgot await» в редакторе).
- `scripts/lib.js` (`sleep`, `nextMqtt`, `changed`, `__wbBindRealmAPI`),
  `wbrules/esengine.go` (`esWbSpawn`), `wbrules/spawn.go`, `wbrules/rule_async_api_test.go`,
  `wbrules/rule_async_leak_test.go`.
- README (раздел «Асинхронные функции и промисы»), `sample-async.js`.
- PR wirenboard/wb-rules#223 (changelog `2.47.0~quickjs1`).
