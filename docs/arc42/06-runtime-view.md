# 6. Представление времени выполнения

Сценарии ниже показывают, как блоки из §5 взаимодействуют в ключевых ситуациях. Везде действует инвариант: весь JS исполняется на одной горутине («engine loop», `syncQueue` → `syncLoop`), остальные горутины (драйвер, таймеры, RPC, tsgo-проверки) лишь ставят замыкания в очередь через `CallSync`/`MaybeCallSync`.

## 6.1 (g) Старт процесса

```mermaid
sequenceDiagram
    participant M as main.go
    participant W as wbgong (wbgo.so)
    participant D as Driver
    participant E as ESEngine
    participant Q as quickjsduk
    M->>W: flag.Parse() · -http → /metrics, /debug/pprof · wbgong.Init(-wbgo)
    M->>D: NewDriverBase(id wb-rules, storage -vdb, Reown=!precise) → StartLoop(), SetFilter(AllDevices), WaitForReady
    M->>E: NewESEngine(driver, engineMqttClient, options)
    E->>Q: NewContext() → globalCtx · SetExecutionTimeLimit(-js-timeout) · SetMemoryLimit(-js-memory-limit) · SetJobErrorHandler
    E->>E: NewTSCompiler(-tsgo) (Available()? иначе лог «tsgo binary not found …») · persistent DB (-pdb, 0640) · прототипы · installBuiltins
    E->>Q: loadLib(): eval lib.js в globalCtx → глобал сохраняется в heap stash как __wbGlobalPrototype
    E->>D: setupRuleEngineSettingsDevice() → vdev «wbrules» / «Rule debugging»
    M->>E: engine.Start() → mainLoop(), syncLoop() · NewDirWatcher(regexp js|ts|.disabled) · SetSourceRoot(-editdir)
    loop каждый аргумент (файл/каталог)
        M->>E: watcher.Load(path) → LiveLoadFile → сценарий 6.2
    end
    M->>W: NewMQTTRPCServer("wbrules").Register(NewEditor(engine)) · Start()
    M->>M: ожидание SIGINT/SIGTERM → engine.Stop(), driver.StopLoop()
```

1. Флаги → `-http` (метрики, pprof) → `wbgong.Init(wbgo.so)`; при дефолтном `-broker` и наличии `/var/run/mosquitto/mosquitto.sock` — переключение на unix-сокет.
2. Драйвер (MQTT-клиент `rules`) стартует и ждёт готовности; затем `NewESEngine`: лимиты и обработчик async-ошибок ставятся на `globalCtx` (runtime-wide), далее prototypes, builtins, `lib.js`, vdev настроек. Проба tsgo — только лог (ADR-011); персистентный `tsgo --api --async` стартует лениво при первой транспиляции `.ts`.
3. `engine.Start()` запускает `mainLoop` (ждёт `driverReadyCh`) и `syncLoop`; DirWatcher загружает файлы; при `-editdir` регистрируется Editor RPC — уже после загрузки всех файлов.

## 6.2 (a) Загрузка / перезагрузка `.js`

```mermaid
sequenceDiagram
    participant FS as DirWatcher (fsnotify)
    participant E as ESEngine (engine loop)
    participant LG as loadGuard
    participant C as ESContext (realm файла)
    participant Q as quickjsduk/QuickJS
    participant D as Driver
    participant B as MQTT
    FS->>E: LiveLoadFile(path) → WhenEngineReady → loadScriptAndRefresh(path,false)
    E->>E: checkSourcePath · tracker.Track(md5) — не изменился ⇒ выход · runCleanups(path): правила, vdev RemoveDevice, mqtt-трекеры, таймеры, localCtx.invalidate()
    E->>E: новый LocFileEntry · disabled ⇒ выход
    E->>LG: quarantined(path)? ⇒ лог «[loadguard] skipping quarantined file», выход
    E->>Q: prepareNewContext: PushThreadNewGlobalenv (realm), proto ← __wbGlobalPrototype, __esInitEnv, builtins, eval(__wbBindRealmAPI)(this), exportModSearch
    E->>LG: beginLoad(path) — маркер на диск
    E->>C: LoadScenario(path)
    C->>Q: preprocessor(no-op для .js) → обёртка «async function F(module, exports){…}» → PcompileStringFilename (синтаксис)
    C->>Q: LoadFunctionFromString → push module/exports → PcallNoPump(2)
    Q-->>E: defineRule → _wbDefineRule → buildRule → RuleEngine.DefineRule (AddRule, cleanup, registerSourceItem)
    Q-->>D: defineVirtualDevice → tx CreateDevice/CreateControl
    D-->>B: /devices/<vdev>/controls/<c>, /meta/*
    C->>Q: PromiseStateTop: Rejected ⇒ Retract + PushPromiseResultTop → ESError · Pending ⇒ TLA продолжится позже
    C->>Q: PumpJobs()
    E->>LG: endLoad(path) — маркер снят, счётчик сброшен
    E->>E: trackESError → LocFileEntry.Error · Log(ERROR) → /wbrules/log/error
    E->>B: Refresh(): rev++, setupCron, пересборка DepTracker, uninitializedRules · publish /wbrules/updates/changed <virtualPath>
```

1. Источник — fsnotify (DirWatcher в wbgo-private) или `watcher.Load` при старте; всё на engine loop. `ContentTracker` (md5) отсекает неизменённое содержимое (кроме `loadIfUnchanged=true` от `LiveWriteScript`).
2. `runCleanups(path)` снимает всё, что файл создал раньше: правила, vdev (удаляются и создаются заново), `trackMqtt`, таймеры; старый realm помечается `invalid` (поздние колбэки пропускаются). Карантин loadguard проверяется до создания realm'а (см. 6.5).
3. Новый realm: глобал файла наследует `__wbGlobalPrototype` (lib.js), realm-локальные builtins и `__wbBindRealmAPI` (ADR-004, ADR-007).
4. Файл всегда оборачивается в `async function F(module, exports)` (ADR-006): синхронная ошибка = отклонённый промис ⇒ ошибка загрузки; `await` верхнего уровня оставляет промис pending. `Refresh()` пересобирает зависимости и cron и публикует `/wbrules/updates/changed`.

## 6.3 (b) Загрузка `.ts`

```mermaid
sequenceDiagram
    participant E as ESEngine (engine loop)
    participant TS as TSCompiler
    participant TG1 as tsgo --api --async (child)
    participant C as ESContext
    participant TG2 as tsgo --noEmit (batch)
    participant B as MQTT
    E->>E: как в 6.2 до LoadScenario (tracker, cleanups, loadguard, новый realm)
    E->>TS: preprocessRuleSource(path, src): Available()? иначе ошибка «TypeScript compiler not found … wb-tsgo» + tracker.Untrack
    TS->>TG1: transpileModule(src, ESNext, sourceMap) по stdin/stdout (JSON-RPC)
    TG1-->>TS: JS + source map → lineMaps[path]
    alt синтаксическая ошибка
        TS-->>E: TSSyntaxError{Line} → ошибка загрузки на строке .ts, tsCheckResults[path] = терминальный вердикт
    end
    E->>C: LoadScenario с wrapPrologue «"use strict"», lineTranslator=TranslateLine → запуск как в 6.2
    E->>E: scheduleTsCheck(path): tsCheckGen[path]++, entry pending, registry = controlsRegistryDts()
    E->>TS: CheckAsync(path, registry, report) — батч 300 мс, семафор 2
    TS->>TG2: tsgo --noEmit --pretty false --target esnext --lib esnext --strict false --module preserve --moduleDetection force --esModuleInterop --allowJs --checkJs <files> wb-rules.d.ts /tmp/wb-controls-*.d.ts (60 s)
    TG2-->>TS: «path(line,col): error TSnnnn: msg» построчно
    TS-->>E: report(diags) → MaybeCallSync: gen совпал? → tsCheckResults[path]={ready,diags}
    E->>B: /wbrules/log/warning «TS check: file:line:col: msg» (≤10 строк на файл)
    Note over E: правила уже работают · диагностика advisory · Editor.Check читает кэш
```

1. Транспиляция — «run first, check later» (ADR-010): ~1 мс на файл через персистентный дочерний процесс; source map даёт таблицу строк, трейсбеки указывают на `.ts`. Отсутствие tsgo — ошибка загрузки; `loadScript` снимает файл с дедупликации (`Untrack`), чтобы повтор сработал, когда tsgo появится.
2. Фоновая проверка батчится (0.27 с/файл против 0.34 с на 20 файлов) и ограничена двумя процессами; реестр контролов передаётся временным `.d.ts` (ADR-014). Результат — в лог правил и в кэш `Editor.Check`; `.js` проверяются тоже (`--checkJs`), но advisory (ADR-012).

## 6.4 (c) Изменение контрола → правило → запись

```mermaid
sequenceDiagram
    participant B as MQTT
    participant D as Driver (wbgo.so, клиент rules)
    participant RE as RuleEngine
    participant L as engine loop
    participant R as Rule / DepTracker
    participant J as JS (realm файла)
    B->>D: /devices/D/controls/C = v
    D->>RE: driverEventHandler → ControlChangeEvent{Spec,Value,PrevValue,IsComplete,IsRetained} → eventBuffer.PushEvent (cap 16, коалесценция)
    RE->>L: mainLoop → processEvents → CallSync(RunRules(event))
    L->>R: controlToRulesListMap[spec] → SetCheckMode(WithEvent) · без контролов / uninitialized → Independent
    loop по ruleList
        L->>R: Rule.Check(e): StartTrackingDeps → cond.Check → StoreRuleDeps(rule)
        R->>J: условие (whenChanged/when/asSoonAs): dev[...] → _wbDevObject/_wbCellObject → ControlProxy.Value() · trackControlSpec
        opt условие сработало
            R->>J: then(newValue, device, cell) через invokeCallback → PcallProp (pushActive, watchdog)
            alt sync then
                J->>RE: dev["x/y"] = v → setDevValue → ControlProxy.SetValueAt
                RE->>D: vdev: tx UpdateValue (кэш сразу, новое событие) / внешний: SetOnValue
                D->>B: /devices/x/controls/y  или  /devices/x/controls/y/on
            else async then (возврат Promise)
                J-->>L: возврат на первом await → popActive → pumpJobs() (микрозадачи)
                Note over J,L: продолжение — из таймера / changed() / nextMqtt() / spawn-колбэка · throw после await → rejection tracker → Log(ERROR,"async rule error: …")
            end
        end
    end
    Note over RE: ошибка sync-колбэка → CallbackErrorHandler → /wbrules/log/error · отклонённая запись → «control x/y: write ignored (...) at file:line»
```

1. Драйвер принимает публикацию своим MQTT-клиентом; `EventBuffer` коалесцирует всплески. `RunRules` выбирает кандидатов по зависимостям, записанным при прошлых проверках (DepTracker), плюс правила без контролов и неинициализированные; каждая проверка условия заново фиксирует прочитанные контролы.
2. Запись в локальный vdev обновляет кэш немедленно и порождает новое событие; запись во внешнее устройство идёт в `/on`-топик, кэш обновится по эху. Отказ драйвера (неверный тип) — только лог (ADR-009).
3. Async-колбэк: после первого `await` управление возвращается в Go, шим дренирует микрозадачи; дальнейшие шаги выполняются из других входов в JS на той же горутине (ADR-005).

## 6.5 (d) Watchdog, ограничение кучи, карантин loadguard

```mermaid
sequenceDiagram
    participant L as engine loop
    participant Q as quickjsduk
    participant JS as JS
    participant LG as loadGuard
    participant SD as systemd
    rect rgb(245,245,245)
    Note over L,JS: Watchdog (-js-timeout, 10 s)
    L->>Q: Pcall/PcallProp → pushActive (depth 1): execStart=now
    JS->>Q: while(true){} … QuickJS вызывает interrupt handler → goInterrupt
    Q-->>JS: now-execStart > execLimit ⇒ return 1 ⇒ InternalError: interrupted
    Q-->>L: GetESError → ExecTimeoutAbort ⇒ «execution timed out: exceeded the 10s js-timeout (runaway loop without await, or a stalled synchronous engine call?)» + трейсбек → лог правила · realm пригоден дальше
    end
    rect rgb(245,245,245)
    Note over L,JS: Heap cap (-js-memory-limit, 512 MiB, runtime-wide)
    JS->>Q: аллокация сверх JS_SetMemoryLimit
    Q-->>JS: исключение out-of-memory в виновном скрипте → лог · процесс жив (внешний забор — systemd MemoryMax=50%)
    end
    rect rgb(245,245,245)
    Note over L,SD: Loadguard (крах процесса внутри LoadScenario)
    L->>LG: beginLoad(path): пишет wbrules-loading.marker
    L->>JS: LoadScenario(path) … процесс падает (panic/SIGSEGV)
    SD->>L: Restart=on-failure, RestartSec=1 → новый процесс
    L->>LG: newLoadGuard.detectCrash(): маркер уцелел ⇒ Crashes[path]++ (json write-then-rename) · ≥3 ⇒ QuarantinedMtime=mtime(path)
    LG-->>L: quarantined(path) ⇒ loadScript пропускает файл с логом ошибки (до смены mtime · чистый endLoad обнуляет счётчик)
    end
```

1. Watchdog измеряет wall-time одного синхронного входа в JS или одного promise job (каждый job — своё окно `jobStart`); срабатывание — обычное JS-исключение с понятным текстом, другие правила работают дальше (ADR-008). Лимит кучи общий для всех realm'ов (ADR-004): аллокационная бомба даёт catchable OOM, а не смерть процесса.
2. Loadguard защищает от crash-loop «файл падает при каждой загрузке → systemd перезапускает → Editor RPC недоступен»: после трёх подряд крахов файл пропускается до редактирования.

## 6.6 (e) Editor.Save из homeui

```mermaid
sequenceDiagram
    participant UI as homeui (editorProxy)
    participant B as MQTT
    participant RPC as MQTTRPCServer (wbgo.so)
    participant ED as Editor
    participant E as ESEngine (engine loop)
    participant FS as Файловая система
    UI->>B: /rpc/v1/wbrules/Editor/Save/<clientId> {path, content}
    B->>RPC: JSON-RPC (последовательный диспатч)
    RPC->>ED: Save(args): path.Clean, validateScriptPath · файл отключён ⇒ +.disabled
    ED->>E: LiveWriteScript(virtualPath, content) → CallSync: checkVirtualPath → физический путь под -editdir
    E->>FS: MkdirAll, os.WriteFile (DirWatcher тоже сработает — ContentTracker дедуплицирует)
    E->>E: loadScriptAndRefresh(cleanPath, true) — сценарий 6.2 (для .ts — 6.3)
    alt загрузка без ошибок
        ED-->>RPC: EditorSaveResponse{path}
    else ScriptError / ошибка записи
        ED-->>RPC: {path, error: msg, traceback:[{line,name}]} (in-band) / RPC error 1002 (EDITOR_ERROR_WRITE)
    end
    RPC->>B: /rpc/v1/wbrules/Editor/Save/<clientId>/reply
    B-->>UI: ответ · параллельно /wbrules/updates/changed и /wbrules/log/* обновляют список/консоль · для .ts → checkTsFile (6.7)
```

1. RPC обслуживается горутиной сервера wbgo.so; `Save` блокируется на `LiveWriteScript` до завершения загрузки на engine loop. Ошибка скрипта возвращается в теле ответа (редактор показывает её у строки), системная — кодом RPC.
2. Сохранение = запись + принудительная перезагрузка (`loadIfUnchanged=true`), включая удаление/пересоздание vdev (гонка драйвера исправлена в wbgo-private #100).

## 6.7 (f) Редактор homeui: типы, реестр, language service, опрос Editor.Check

```mermaid
sequenceDiagram
    participant P as edit-rule.tsx
    participant S as rulesStore / devicesStore
    participant RPC as Editor RPC (через MQTT)
    participant LS as ts-language-service (lazy chunk)
    participant CM as CodeMirror 6
    P->>RPC: Editor.Load(path) → content · Editor.GetTypes() (race с таймаутом 3 с)
    RPC-->>P: wb-rules.d.ts контроллера (fallback: vendored autocomplete/wb-rules.d.ts)
    P->>S: buildControlsRegistry(devicesStore) → interface WbControls {...}
    P->>LS: import() → loadTsEditorSupport(path, content, typesDts, registryDts)
    LS->>LS: TS LS (lib.es*.d.ts, allowJs+checkJs, strict:false) + кастомные диагностики 990001–990004
    LS-->>CM: completions, hover, lint (squiggles)
    P->>S: checkTsFile(path)
    loop ≤40 раз, backoff 700 мс → 2 с (≈1 мин), пока status=pending
        S->>RPC: Editor.Check({path})
        RPC-->>S: {status, diags}
    end
    S-->>CM: ready ⇒ controller-diagnostics → merge (dedupe против LS), lint-refresh · /wbrules/log/error с path:line → runtime-errors → lint · /wbrules/log/+ → консоль · /wbrules/updates/changed → список
```

1. Типы берутся с контроллера (`GetTypes`), vendored-копия — fallback для старых прошивок (ADR-015); реестр контролов строится из `devicesStore` — тот же приём, что `controlsRegistryDts` на контроллере (ADR-014).
2. Локальная диагностика мгновенна; авторитетный вердикт контроллера приходит опросом `Editor.Check` и показывается инлайн с дедупликацией (ADR-016 — кастомные проверки «забытого await» вместо ESLint).
