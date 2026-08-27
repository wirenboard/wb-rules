# ADR-005: Microtask pump + host promise-rejection tracker; async-ошибки как строки лога

**Статус:** Принято, реализовано
**Дата:** 2026-08-13 (pump), 2026-08-15 (tracker)

## Контекст

У Duktape 1.0.2 промисов не было, и wb-rules не имел event-loop в JS-смысле: все вызовы Go→JS
синхронны, обратно в Go — через callback'и. QuickJS даёт промисы и очередь jobs, но её нужно
дренировать хосту (`JS_ExecutePendingJob`): без этого промис никогда не резолвится — jobs просто
не исполняются. Кроме того, хост должен сам замечать необработанные отказы: с точки зрения
`JS_ExecutePendingJob` reaction job «успешен» (ошибка уходит в производный промис), поэтому
код возврата `<0` для ошибок в async-коде не срабатывает никогда — без отдельного механизма
они глотаются.

## Решение

- **Pump.** Шим дренирует `JS_ExecutePendingJob` при каждом возврате в Go с внешнего JS-входа
  (глубина 0): `pumpJobs()` в `internal/quickjsduk/duktape.go`, лимит `maxJobs=100000` за ход.
  Это даёт семантику event-loop без отдельного потока: любой callback из таймера, MQTT, spawn —
  точка входа, после которой очередь дренируется.
- **Tracker.** `qjd_install_rejection_tracker` → `JS_SetHostPromiseRejectionTracker`; rejection,
  обработанный в том же ходу (`is_handled`), снимается (retract); после дренажа `flushRejections`
  → `reportJobError` → `SetJobErrorHandler` движка → `engine.Log(ERROR, "async rule error: ... (stack)")`
  → `/wbrules/log/error`.
- Порядок критичен: исключение вызова захватывается **до** pump'а — pump, выполненный раньше
  `JS_GetException`, уничтожает реальное исключение. Внутри pump'а watchdog взводится на каждый
  job как на отдельное окно ([ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md)).
- Async-ошибка — это строка лога (позже — и диагностика в редакторе, [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)/016), не исключение:
  исключение некуда пробрасывать, вызывающего JS-кадра нет.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Полагаться на код возврата `JS_ExecutePendingJob` | по спецификации реакций job успешен, ошибка уходит в производный промис — никогда не видна |
| Проброс async-ошибки в JS исключением | нет вызывающего кадра; в Go — тоже некуда (вызов давно вернулся) |
| Отдельная горутина/поток event-loop | ломает однопоточный контракт движка (CallSync, engine loop) и cgo-модель [ADR-003](ADR-003-go-duktape-compatible-shim.md) |
| Pump по таймеру | задержки между `await` и продолжением; лишние пробуждения на embedded-устройстве |

## Последствия

Плюсы:
- `await`, `Promise.all`, `setTimeout`-промисы работают предсказуемо; необработанные ошибки видны.
- Leak-тесты: churn 5000 итераций — рост ≤ 256 KB; map tracker'а дренируется; realm'ы с
  вечно-pending промисами освобождаются (`TestRealmWithParkedPromisesReleased`).

Минусы и риски:
- Ошибки после `await` видны только в логе/редакторе, не ломают загрузку файла (кроме TLA, [ADR-006](ADR-006-top-level-await.md)).
- Фантомные rejection'ы от забытых таймеров — именно то, что трекер теперь показывает; отсюда
  дизайн таймаутов в [ADR-007](ADR-007-promise-native-lib-js.md).
- Лимит 100000 jobs за ход — защитный предел, не семантика.

### Риски и технический долг

- Риск (flaky): тесты с таймингами (`TestTimedOutWaitersReclaimed`, `TestPersistentStorageSuite`) чувствительны к нагруженному CI; предлагается `-count=3` для async-сьютов.

## Ссылки

- [ADR-003](ADR-003-go-duktape-compatible-shim.md), [ADR-004](ADR-004-one-runtime-realm-per-file.md), [ADR-006](ADR-006-top-level-await.md), [ADR-007](ADR-007-promise-native-lib-js.md), [ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md).
- `internal/quickjsduk/duktape.go` (`pumpJobs`, `flushRejections`, `reportJobError`),
  `internal/quickjsduk/shim.c` (`qjd_install_rejection_tracker`, `goPromiseRejection`),
  тесты `TestUnhandledRejectionReported`, `TestHandledRejectionNotReported`, `leak_test.go`,
  `wbrules/rule_async_leak_test.go`.
- PR wirenboard/wb-rules#223.
