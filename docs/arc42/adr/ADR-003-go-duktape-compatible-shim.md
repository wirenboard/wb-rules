# ADR-003: Шим, совместимый с API go-duktape (`internal/quickjsduk`), вместо переписывания движка

**Статус:** Принято, реализовано
**Дата:** 2026-08-12

## Контекст

`wbrules/esengine.go` (~3300 строк) и `escontext.go` написаны против стек-API go-duktape
(`Push*/Get*/Pcall/Eval`, heap stash, `duk_push_thread_new_globalenv`, CommonJS 1.x `modSearch`).
Эта логика battle-tested (36 тест-сьютов, правила с 2015 г.). Полная переписка под QuickJS C API
означала бы потерю этой валидации. Разумный путь — не переписывать `esengine.go`, а подменить
движок под ним и дать собственному тест-сьюту wb-rules проверить порт.

## Решение

- Реализовать на QuickJS ровно те методы стек-API go-duktape (~90), которые использует wb-rules, в
  пакете `internal/quickjsduk` (`duktape.go` ~1700 строк + `shim.c/.h`), подключить через
  `replace github.com/wirenboard/go-duktape => ./internal/quickjsduk` в `go.mod`.
- Код `wbrules/` почти не трогать: точечные правки в `escontext.go` (разбор стеков и сообщений об ошибках), фикс в `lib.js`, три правки
  test-data (см. «Документированные отличия»).
- Ключевые семантические адаптации внутри шима:
  1. QuickJS передаёт C-функции realm *вызывающего кода* (для `JS_NewCFunctionData`) — это может
     быть realm другого файла или уже выгруженный realm, — а Duktape исполняет нативные функции в
     контексте вызывающего *треда* ⇒ шим ведёт стек активных realm'ов (`pushActive/popActive`) и
     диспатчит каждый вызов в realm, исполняющий JS сейчас. В promise job (активного
     входа нет) вызов остаётся в realm самой функции — так пост-`await` вызовы сохраняют
     привязку к своему файлу; primary — только запасной вариант для функции, чей realm
     уже выгружен (замыкание пережило перезагрузку своего файла).
  2. Go мигрирует горутины между OS-потоками между cgo-вызовами ⇒ на каждом входе
     `JS_UpdateStackTop`, а каждая «глубокая» операция (eval, call, pending job, new context, JSON)
     **фьюзится в один C-вызов** (`qjd_eval`, `qjd_call`, ...). Иначе проверка глубины стека
     QuickJS даёт вероятностный `stack overflow` при сотнях realm'ов в одном процессе.
  3. CommonJS Duktape 1.x: кэш модулей per-realm (модуль инициализируется в каждом файле),
     относительные id, pre-registration циклов, `Duktape.modSearch`, полифил `Duktape.enc/dec`
     (`version` 10002 — уровень Duktape 1.0.2).
  4. Объекты custom-классов получают `Object.prototype` (в QuickJS по умолчанию `null`).
  5. Точные строки ошибок Duktape (`Error: error error (rc -100)`) — их ассертят тесты.
- Две адаптации формата движка в `wbrules/escontext.go`: `fileRx` разбирает строки стека QuickJS
  (`at fn (file:line:col)` и форму синтаксической ошибки `at file:line:col`) вместо Duktape'овских;
  `GetESError` берёт сообщение из самого объекта ошибки (у Duktape `.stack` начинался с `Error: msg`,
  у QuickJS содержит только кадры). Остальной `wbrules/` не тронут; `lib.js` работает как есть
  (ES6 Proxy в QuickJS покрывает то, что давал форк Duktape).
- Тесты гоняются против production-плагина `wbrules/wbgo.so` из wbgo-private
  (`go build -buildvcs=false -buildmode=plugin`), собранного **без** `-trimpath`, как и тестовый
  бинарь — Go требует одинаковых build ID общих пакетов.
- Эволюция шима после базового порта: microtask pump и rejection tracker ([ADR-005](ADR-005-microtask-pump-rejection-tracker.md)),
  `PcallNoPump/PromiseStateTop/RetractTopPromiseRejection` ([ADR-006](ADR-006-top-level-await.md)), `SetMemoryLimit` и
  `SetExecutionTimeLimit` ([ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md)).

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Чистый порт `esengine.go` на QuickJS C API | потеря battle-tested логики; нет валидации существующим тест-сьютом; большой объём изменений в хрупком коде |
| Готовые Go-биндинги QuickJS (`quickjs-go` и т. п.) | другой API-стиль — всё равно пришлось бы переписывать `esengine.go`; нет per-realm/модульной семантики Duktape |
| Обёртка над `qjs` как отдельным процессом | разрушает общий heap и синхронную модель вызовов Go↔JS |

## Последствия

Плюсы:
- Все 36 исходных сьютов проходят на production-плагине `wbgo.so` без его изменений; объём правок в `wbrules/` минимален.
- Все дальнейшие движковые фичи локализованы в одном пакете.

Минусы, риски, долги:
- Поверхность шима «заморожена»: новые фичи идут в `lib.js` ([ADR-007](ADR-007-promise-native-lib-js.md)), а не в новые C entry points.
- Ограничения cgo: без variadic-функций, фильтрация флагов компилятора ([ADR-002](ADR-002-bellard-quickjs-submodule.md)).
- `-race` требует race-сборки `wbgo.so`; TSAN падает на сьютах с churn'ом heap'ов.
- Лишний проход по стеку значений даёт +0.6 мс к медиане MQTT-реакции ([ADR-001](ADR-001-quickjs-instead-of-duktape.md)).
- Документированные отличия поведения: строка многострочного `defineRule` — первая (24 → 17),
  поля `StorableObject` неперечислимы (spec-correct `for-in`), эмодзи в логе — UTF-8 вместо CESU-8.

### Риски и технический долг

- Риск (ABI плагина `wbgo.so`): бинарь wb-rules и плагин должны собираться одним toolchain и флагами (`-trimpath -ldflags "-s -w"`), иначе `plugin.Open` отказывает; CI берёт `.so` из master `wbgo-private` в момент сборки ⇒ драйвер и движок могут разойтись; `cp` поверх live-mmap'нутого `.so` — SIGBUS (процедура stop → swap → start). Предлагается пиновать ревизию wbgo-private в сборке или поставлять `.so` отдельным пакетом с версионной зависимостью.
- Риск: `TestCorpus` (внутренний корпус, приватный submodule `update = none`) на CI не гоняется — регрессия совместимости может дойти до testing set; митигация: локальный прогон с `WB_RULES_CORPUS_REQUIRED=1` перед релизом и синтетические регресс-тесты в репо; предлагается Jenkins-job с доступом к сабмодулю.

## Ссылки

- [ADR-001](ADR-001-quickjs-instead-of-duktape.md), [ADR-002](ADR-002-bellard-quickjs-submodule.md), [ADR-004](ADR-004-one-runtime-realm-per-file.md), [ADR-005](ADR-005-microtask-pump-rejection-tracker.md), [ADR-006](ADR-006-top-level-await.md), [ADR-008](ADR-008-guardrails-watchdog-heap-loadguard.md).
- `internal/quickjsduk/duktape.go`, `internal/quickjsduk/shim.c`, `internal/quickjsduk/shim.h`,
  `internal/quickjsduk/duktape_test.go`, `go.mod` (`replace`).
- `wbrules/escontext.go`, `wbrules/esengine.go`.
- PR wirenboard/wb-rules#223.
