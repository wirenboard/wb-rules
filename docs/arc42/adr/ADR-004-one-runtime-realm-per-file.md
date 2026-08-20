# ADR-004: Один `JSRuntime` на процесс + realm (`JS_NewContext`) на файл правил

**Статус:** Принято, реализовано
**Дата:** 2026-08-12 (модель), 2026-08-14 (per-realm rebinding)

## Контекст

Исторически wb-rules не создаёт heap на скрипт: каждый файл получает свежий глобальный объект
(Duktape `duk_push_thread_new_globalenv`), но куча, поток исполнения, `module.static`, `dev`,
heap stash — общие. Это «cooperative namespacing, not isolation». При переходе на QuickJS ([ADR-001](ADR-001-quickjs-instead-of-duktape.md))
встал выбор: сохранить модель или перейти на `JSRuntime` на файл с настоящими per-script
лимитами. Бенчмарк дал context 0.17 MB против runtime 0.20 MB на скрипт: за +0.04 MB/скрипт можно было
получить настоящую изоляцию с per-script memory cap.

## Решение

- Один `JSRuntime` (создаётся в `quickjsduk.NewContext()` вместе с primary-контекстом, `require`
  и rejection tracker'ом).
- На каждый файл правил — отдельный realm `JSContext` через `PushThreadNewGlobalenv()` →
  `qjd_new_context` (`wbrules/esengine.go: prepareNewContext`). Handle realm'а хранится в stash
  `_esThreads[path]`; освобождение handle'а освобождает realm через GC (`reapDeadContexts`).
- Глобал файла получает прототип `__wbGlobalPrototype` — глобал общего `globalCtx`, куда один раз
  загружен `scripts/lib.js`. Builtins Go (`esBuiltinFuncs`) ставятся realm-локально
  (`installBuiltins`), realm-чувствительные JS-обёртки — через `__wbBindRealmAPI` (см. ниже).
- `require()` кэшируется per-realm; `module.static` и `PersistentStorage(name, {global:true})` —
  осознанные каналы обмена между файлами; `global.__proto__` — escape hatch.

### Per-realm rebinding `__wbBindRealmAPI`

Прямое следствие модели «builtins на прототипе»: после `await` promise job выполняется без
активного контекста, и вызов `defineRule`/`setTimeout`/`spawn`/`PersistentStorage`/`sleep`/
`changed`/`nextMqtt` из прототипа атрибутировался бы не тому файлу (утечка при reload). Поэтому при
создании realm'а в нём компилируется `eval('(' + __wbBindRealmAPI.toString() + ')')(this)` —
тонкие обёртки, замкнутые на свой realm. Важно, что binder компилируется именно `eval`'ом в
локальном realm'е: функция, созданная в глобальном realm'е, диспатчила бы в глобал. В binder
входит и `PersistentStorage` — иначе хранилище, открытое после `await`, привязывалось бы не к тому
файлу.

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| `JSRuntime` на файл (реальные `JS_SetMemoryLimit` per-file, отдельный interrupt) | drop-in шим ([ADR-003](ADR-003-go-duktape-compatible-shim.md)) требует общего heap stash, общего prototype-глобала, кросс-файловых объектов (`module.static`, Go-обёртки прокси), общих модулей `require`; модель унаследована от Duktape-версии вместе с её семантикой |
| Документировать ограничение «регистрируйте правила и таймеры синхронно, до первого `await`» вместо rebinding | правило, которое легко нарушить незаметно (утечка проявляется только при reload файла); async-код — основной мотив смены движка, он не должен быть second-class |
| Процесс на файл | см. [ADR-001](ADR-001-quickjs-instead-of-duktape.md) |

## Последствия

Плюсы:
- Минимальная память на файл (0.17 MB), общий `dev`, общие модули, привычная семантика.
- Файл изолирован по именам; после `await` атрибуция файла сохраняется (cleanup при reload корректен).

Минусы и риски:
- Per-script лимит RSS невозможен (один общий heap QuickJS на процесс). Доступные ручки
  процессные: `JS_SetMemoryLimit` 512 MiB, systemd `MemoryMax=50%`, `GOMEMLIMIT`.
- Один зависший синхронный цикл блокирует все правила ⇒ watchdog ([ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md)).
- ~30 мелких функций на файл (пренебрежимо); реализации builtins продублированы (top-level и
  внутри binder'а) — hygiene-долг с риском дрейфа.

### Риски и технический долг

- Риск: один JS-поток и один heap на процесс — тяжёлое синхронное правило задерживает остальные, per-file лимитов CPU/памяти нет, RSS «на скрипт» измерить нельзя; смена модели (runtime на файл) — только новым ADR. Связанные обращения: SOFT-7064, SOFT-3239.

## Ссылки

- [ADR-001](ADR-001-quickjs-instead-of-duktape.md), [ADR-003](ADR-003-go-duktape-compatible-shim.md), [ADR-005](ADR-005-microtask-pump-rejection-tracker.md), [ADR-007](ADR-007-promise-native-lib-js.md), [ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md).
- `internal/quickjsduk/duktape.go` (`PushThreadNewGlobalenv`, `runtimeState`), `wbrules/esengine.go`
  (`prepareNewContext`, `GLOBAL_OBJ_PROTO_NAME`, `THREAD_STORAGE_OBJ_NAME`), `scripts/lib.js`
  (`global.__wbBindRealmAPI`).
