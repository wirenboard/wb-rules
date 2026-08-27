# ADR-008: Guardrails — watchdog 10 с, heap cap 512 MiB, loadguard-карантин, `CallSync` без panic

**Статус:** Принято, реализовано
**Дата:** 2026-08-17

## Контекст

Модель «один heap, один JS-поток» ([ADR-004](ADR-004-one-runtime-realm-per-file.md)) означает, что один runaway-скрипт блокирует все
правила; у Duktape эффект был тем же, просто без измерений. На внутреннем корпусе скриптов
`while(true)` в одном файле вешает движок целиком, а фоновые горутины (cron) копятся.

С промисами появился новый, типовой способ написать такой цикл нечаянно: `while (sleep(1000)) {}`
без `await` — промис truthy, цикл синхронный и бесконечный. Цепочка последствий в исходной
модели: watchdog прерывает цикл, но колбэк другого правила, ждущий engine loop через `CallSync`,
в debug-режиме паникует по таймауту ⇒ `exit(2)` ⇒ systemd перезапускает процесс ⇒ файл
загружается снова ⇒ crash loop ⇒ Editor RPC недоступен, файл нельзя даже открыть и поправить в
редакторе. Защита нужна на каждом звене этой цепочки, а не только на первом.

## Решение

Четыре независимых механизма — по одному на каждое звено цепочки выше:

1. **Watchdog** `DEFAULT_JS_EXECUTION_LIMIT` = **10 с** (флаг `-js-timeout`):
   `JS_SetInterruptHandler` → `goInterrupt` сравнивает `now - execStart > execLimit` для
   внешнего входа (`pushActive` на глубине 1) и отдельно для каждого promise job (`jobStart`).
   Сообщение: «execution timed out: exceeded the 10s js-timeout (runaway loop without await, or a stalled synchronous engine call?)»
   + traceback в лог правил; контекст остаётся рабочим. Подмена текста привязана к факту
   срабатывания interrupt handler'а, поэтому чужая ошибка не может «украсть» сообщение таймаута.
2. **Heap cap** `JS_SetMemoryLimit(rt, 512 MiB)` (флаг `-js-memory-limit`, 0 = off) —
   allocation bomb даёт catchable OOM-исключение в виновном скрипте вместо смерти процесса;
   внешняя ограда — systemd `MemoryHigh=30% MemoryMax=50%`.
3. **Loadguard** (`wbrules/loadguard.go`): marker-файл `wbrules-loading.marker` вокруг
   `LoadScenario`; если процесс упал внутри — при старте `detectCrash()` увеличивает счётчик
   (`wbrules-loadguard.json`, write-then-rename); после `LOAD_CRASH_QUARANTINE_THRESHOLD = 3`
   подряд файл пропускается до смены mtime («[loadguard] skipping quarantined file»). Каталог —
   `filepath.Dir(-pdb)`; `-pdb ""` отключает.
4. **`CallSync`** в debug-режиме больше не паникует через `ENGINE_CALLSYNC_TIMEOUT` = 120 с, а
   пишет «CallSync blocked for 2m0s (runaway rule blocking the main loop?); still waiting» и ждёт.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Per-script RSS-лимит | невозможен при одном heap ([ADR-004](ADR-004-one-runtime-realm-per-file.md)) |
| Только watchdog | не закрывает crash-loop при краше в загрузке и панику CallSync |
| Лимит 60 с | 60 с простоя всех правил на контроллере — слишком долго; 10 с покрывает реальные правила корпуса |
| Lint в редакторе как единственная защита | не защищает ssh/scp-воркфлоу; реализован как дополнение ([ADR-016](ADR-016-custom-forgot-await-diagnostics-not-eslint.md)) |
| Убивать процесс при OOM и полагаться на systemd | теряются все правила и состояние; цикл рестартов |

## Последствия

Плюсы:
- Runaway-цикл стоит максимум 10 с; процесс переживает allocation bomb; crash-loop при загрузке
  самогасится; редактор остаётся доступным.

Минусы и риски:
- 10 с убьёт легитимные тяжёлые синхронные вычисления (настраивается флагом).
- Лимит кучи общий на все файлы: виновник получит OOM первым, но не обязательно только он.
- Карантин снимается только правкой файла (смена mtime) — нужно знать о механизме
  (сообщение в лог правил).
- Тесты: `TestRunawayScriptInterrupted` (JsExecutionLimit 300 мс), `exectimeout_test.go`
  (сообщение не «крадётся»), `memlimit_test.go` (OOM throw, контекст жив), `loadguard_test.go`.

### Риски и технический долг

- Риск: Go-паники вне JS (`wbgo`, MQTT, `concurrent map writes`, `send on closed channel`) по-прежнему валят процесс — loadguard защищает только окно загрузки; предлагается recover-обёртки на границе драйвер → движок (SOFT-5793, SOFT-7341, SOFT-4057).
- Риск: watchdog 10 с убьёт и легитимные длинные синхронные вычисления — `-js-timeout` настраивается; нужна индикация «упёрся в лимит» в UI и per-rule метрики (roadmap).
- Риск: systemd `MemoryMax=50%` vs heap cap 512 МиБ — на WB6/7 50 % RAM может быть меньше 512 МиБ (OOM-kill раньше catchable OOM), на WB8 лимит кучи не защищает от роста Go-части; предлагается выбирать heap cap от объёма RAM или снизить дефолт для armhf.
- Долг: события watchdog/heap/карантина местами без `file:line`, карантин не виден в `Editor.List`; Prometheus-метрики выключены по умолчанию и не per-rule.

## Ссылки

- [ADR-004](ADR-004-one-runtime-realm-per-file.md), [ADR-005](ADR-005-microtask-pump-rejection-tracker.md), [ADR-016](ADR-016-custom-forgot-await-diagnostics-not-eslint.md).
- `wbrules/esengine.go` (`DEFAULT_JS_EXECUTION_LIMIT`, `DEFAULT_JS_MEMORY_LIMIT`),
  `wbrules/engine.go` (`ENGINE_CALLSYNC_TIMEOUT`, строки ~1036–1057), `wbrules/loadguard.go`,
  `internal/quickjsduk/duktape.go` (`SetExecutionTimeLimit`, `SetMemoryLimit`, `goInterrupt`),
  `main.go` (флаги), `debian/wb-rules.service`.
- PR wirenboard/wb-rules#223.
