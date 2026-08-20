# 12. Глоссарий

Термины в том значении, в котором они используются в коде wb-rules и этой
документации. Сгруппированы по областям; внутри группы — по алфавиту (латиница, затем
кириллица).

## 12.1. Движок и шим

| Термин | Значение |
|---|---|
| **Duktape** | Встраиваемый ES5-движок (версия 1.0.2, 2014 г.), использовавшийся wb-rules до 2.47 через модуль `github.com/wirenboard/go-duktape`. Его стек-API остался контрактом для кода `wbrules/` (см. шим). |
| **go-duktape API** | Стек-ориентированный Go-интерфейс к движку (`PushString`, `Pcall`, `GetPropString`, `PushThreadNewGlobalenv`, …), используемое wb-rules подмножество которого (~90 методов) реализует шим. |
| **Heap / runtime** | Единственный `JSRuntime` процесса: общая куча всех файлов, heap stash (`_esThreads`, `_esModules`, колбэки), `JS_SetMemoryLimit`, interrupt handler. Аналог Duktape-heap. |
| **Pump (microtask pump, job pump)** | Дренаж очереди promise-job'ов `JS_ExecutePendingJob` при каждом возврате в Go с внешнего JS-входа (глубина 0), до 100 000 job'ов за ход; даёт event-loop-семантику промисов внутри синхронного Go-процесса. `PcallNoPump`/`PumpJobs` — явный split для инспекции TLA. |
| **QuickJS** | Встраиваемый JS-движок Ф. Беллара (релиз 2026-06-04, submodule `third_party/quickjs`), ES2025+; интерпретатор байткода без JIT. |
| **Realm (контекст, «thread»)** | `JSContext`, создаваемый на каждый файл правил (`PushThreadNewGlobalenv` → `qjd_new_context`): собственный глобальный объект с прототипом `__wbGlobalPrototype`, свой кэш `require`, свои биндеры. Историческое имя в коде — thread (`_esThreads`). |
| **Rejection tracker** | `JS_SetHostPromiseRejectionTracker`: регистрирует необработанные rejection'ы, снимает при `is_handled` в том же ходу, после pump'а сбрасывает в `reportJobError` → `async rule error: … (stack)`. `RetractTopPromiseRejection` — исключение результата TLA-обёртки из трекинга. |
| **Shim (шим, `internal/quickjsduk`)** | Go + C слой (`duktape.go`, `shim.c`, `qjs_*.c`), реализующий API go-duktape поверх QuickJS; подключён через `go.mod replace`. Содержит стек активных realm'ов, `JS_UpdateStackTop`, one-cgo-call обёртки `qjd_eval/qjd_call`, Duktape-совместимый CommonJS и строки ошибок. Поверхность «заморожена». |
| **TLA (top-level await)** | `await` на верхнем уровне файла правил: файл оборачивается в `async function F(module, exports)`, состояние промиса инспектируется до pump'а (Rejected ⇒ синхронная ошибка загрузки, Pending ⇒ отложенная загрузка). |
| **Watchdog (`-js-timeout`)** | Лимит синхронного исполнения (по умолчанию 10 с, `DEFAULT_JS_EXECUTION_LIMIT`): interrupt handler QuickJS прерывает вход/job, превысивший окно, с сообщением `execution timed out: exceeded the 10s js-timeout (runaway loop without await, or a stalled synchronous engine call?)`. |
| **Heap cap (`-js-memory-limit`)** | `JS_SetMemoryLimit` на runtime (512 МиБ по умолчанию, 0 — выключен): превышение даёт catchable out-of-memory в скрипте вместо смерти процесса. |
| **`__wbBindRealmAPI` (binder)** | Функция из `lib.js`, компилируемая в каждом realm'е (`eval('(' + fn.toString() + ')')(this)`): создаёт realm-локальные `defineRule`, таймеры, `spawn`, `sleep`, `changed`, `nextMqtt`, `PersistentStorage`, чтобы вызовы после `await` атрибутировались своему файлу. |
| **`__wbGlobalPrototype`** | Глобальный объект realm'а, в который загружен `lib.js`; служит прототипом глобалов всех файловых realm'ов (общие builtins, `dev`, `cron`, `Notify`, `Alarms`). |

## 12.2. Модель правил и библиотека

| Термин | Значение |
|---|---|
| **Контрол (control, cell)** | Значение `/devices/<dev>/controls/<ctrl>` с метаданными (`/meta/type`, `units`, …); `ControlSpec{DeviceId, ControlId}`; «cell» — старое имя в коде (`_wbCellObject`). |
| **Устройство / vdev (виртуальное устройство)** | Устройство `/devices/<id>`; vdev создаётся правилами через `defineVirtualDevice`, принадлежит драйверу wb-rules (`wbgo.so`), значения сохраняются в `wbrules-vdev.db`. Остальные — external. |
| **`dev`** | Proxy-объект доступа к контролам: `dev["d/c"]`, `dev.d.c`, `dev["d/c#meta"]`; чтение идёт в Go-кэш драйвера и регистрирует зависимость правила; запись — `UpdateValue` (vdev) или `/on` (external). |
| **DepTracker** | Механизм `RuleEngine`: во время проверки условия записывает, какие контролы прочитало правило (`StartTrackingDeps` → `StoreRuleDeps`), и строит `controlToRulesListMap`, чтобы на событие пересчитывать только зависимые правила. |
| **Правило (`defineRule`)** | Именованная или анонимная единица реакции: условие (`whenChanged`, `when`, `asSoonAs`, `_cron`) + `then(newValue, device, control)`; возвращает branded `RuleId`; привязано к файлу (`ScopedCleanup`) и realm'у. |
| **`whenChanged` / `when` / `asSoonAs`** | Виды условий: изменение контрола (или значения функции); level-triggered (пока истинно — при каждом пересчёте); edge-triggered (один раз при переходе в истину). |
| **cron** | Условие `cron("spec")` — `robfig/cron/v3` с секундами; `setupCron` пересобирается на каждом `Refresh()`. Отдельно: `cron` в `lib.js` (`CronEntry`). |
| **Таймеры** | `startTimer/startTicker` (именованные, `timers.x.firing`), `setTimeout/setInterval`; `MIN_INTERVAL_MS=1`, предупреждение при периоде ≤10 мс; realm-локальные через binder. |
| **Async-библиотека** | `spawn()`/`runShellCommand()` → `Promise<{exitCode, capturedOutput, capturedErrorOutput}>` (ненулевой код — resolve, reject только если процесс не запустился), `sleep(ms)`, `changed(ctrl[,timeoutMs])` (постоянное анонимное правило на контрол на файл), `nextMqtt(topic[,timeoutMs])` (пропускает retained). |
| **PersistentStorage / StorableObject** | bbolt-хранилище ключ-значение (`wbrules-persistent.db`, права 0640) per-file или `{global:true}`; вложенные объекты должны быть обёрнуты в `StorableObject` (поля неперечислимы). Callable и constructible. |
| **`module.static`** | Объект модуля, общий для всех realm'ов, которые его `require`'ят (кэш модулей — per-realm, но `static` — из стэша `_esModules`); способ обмена состоянием между файлами. |
| **`require` / `modSearch`** | CommonJS в стиле Duktape 1.x: поиск по `WB_RULES_MODULES` (`/etc/wb-rules-modules:/usr/share/wb-rules-modules`), `Duktape.modSearch`, `module.filename`, кэш per realm. |
| **Notify / Alarms** | Встроенные модули `wb-notify.js` (`sendSMS/sendEmail/sendWebhook/sendTelegramMessage` через `curl`/`sendmail`/`gammu`) и `wb-alarms.js` (`Alarms.load(config)` из `/etc/wb-rules/alarms.conf`). |
| **Loadguard / карантин** | Защита от crash-loop при загрузке: маркер `wbrules-loading.marker` вокруг `LoadScenario`; если процесс умер с маркером — счётчик падений файла; после 3 подряд файл в карантине до смены mtime (`wbrules-loadguard.json`). |
| **Write ignored** | Сообщение `control X/Y: write ignored (...) at file:line` при отказе драйвера принять значение (неверный тип и т. п.); исключение не бросается, кэш не меняется. |
| **ScopedCleanup / LocFileEntry** | Per-file список действий отката (правила, vdev, таймеры, MQTT-трекеры) и per-file запись о расположении правил/устройств/таймеров, ошибке загрузки и флаге enabled (для `Editor.List`). |
| **ContentTracker** | md5-дедупликация содержимого файла при перезагрузке (из `wbgong`); `Untrack` — принудить повторную загрузку того же содержимого. |

## 12.3. Инфраструктура, тестирование, процесс

| Термин | Значение |
|---|---|
| **`wbgo.so` / wbgong** | Go-plugin драйвера MQTT-устройств из закрытого `wbgo-private` (`/usr/lib/wb-rules/wbgo.so`) и публичный модуль интерфейсов `wbgong` (Driver, DirWatcher, ContentTracker, MQTTRPCServer, testutils). ABI требует одинакового toolchain и флагов сборки. |
| **Fake broker** | `wbgong/testutils` `FakeMQTTFixture`/`Suite`: in-memory брокер для Go-сьютов (`RuleSuiteBase`), ассерты по логу брокера (`Verify`, `SkipTill`), `fakeCron`. |
| **Testing set** | aptly-набор репозитория WB для ранних сборок (`experimental.quickjs-typescript`, версии `*~exp~quickjs+ts~*`): контроллеры подписываются на него, чтобы получить `wb-rules 2.47.0~quickjsN` и `wb-tsgo` через apt. Не путать с `SetTesting`/`-debug-queues` (режим движка без очередей для детерминированных тестов). |
| **Корпус** | Внутренний набор скриптов правил для регрессионной проверки совместимости (приватный submodule `wbrules/testdata/corpus`, `update = none`) и snapshot вердиктов `corpus-verdicts.txt` (ключ — sha256 содержимого); `TestCorpus` локально, `TestCorpusExample` (6 синтетических файлов) всегда. |
| **Stacked PR** | Цепочка PR, каждый поверх предыдущего: #223 (`quickjs-core`→`master`) → #224 (`quickjs-ts`→`quickjs-core`) → homeui #1202. |
| **Jenkins `buildDebGolangWbgo`** | Общая pipeline WB для Go-пакетов с плагином wbgo: Go 1.26, armhf+arm64, lintian; единственный CI (GitHub Actions отключены). |
| **Submodule (Bellard)** | `third_party/quickjs` — упстрим без изменений; обёртки `qjs_*.c` `#include`-ят его исходники; `CONFIG_VERSION` задаётся в `qjs_build.c`. |
| **Leak-тесты / канарейка** | `MemoryUsed()` после 2×`RunGC()` как мера роста heap; `__wbAsyncWaiters()` — диагностический счётчик ожидающих промисов. |
| **Engine loop / CallSync** | Единственная горутина, исполняющая JS (`syncQueue`); `CallSync` — синхронный вызов в неё (таймаут 120 с, в debug — предупреждение вместо panic); `MaybeCallSync` — неблокирующий после остановки. |
| **EventBuffer** | Буфер `ControlChangeEvent` (cap 16) между горутиной драйвера и engine loop с коалесценцией событий одного контрола. |
