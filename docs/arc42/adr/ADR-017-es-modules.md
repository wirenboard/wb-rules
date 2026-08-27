# ADR-017: ES-модули в файлах правил и модулей — определение формата по исходнику, загрузчик QuickJS с хостом в движке

**Статус:** Принято, реализовано
**Дата:** 2026-08-27

## Контекст

После [ADR-006](ADR-006-top-level-await.md) файл правил — тело `async function F(module, exports)`,
модули — CommonJS в стиле Duktape 1.x ([ADR-003](ADR-003-go-duktape-compatible-shim.md)):
`require()`, `module.exports`, `module.static`, кэш per realm. `import`/`export` внутри обёртки —
`SyntaxError`, и README честно объявлял ES-модули неподдерживаемыми; TS-проверка при этом
работала в режиме модуля (`--moduleDetection force`), то есть редактор *разрешал* писать
`import`, а движок такой файл не загружал. Ветка `quickjs-fs` (#231) обходила это транспиляцией
в CommonJS (`module: commonjs`) — только для `.ts`, с потерей живых привязок и `import.meta`, и с
помощниками interop, сдвигающими строки. Вопрос был отложен в §9 «что не решено»: нужны
собственный загрузчик, политика совместимости с `require()`/`module.static` и решение о TLA.

QuickJS даёт всё необходимое: `JS_EVAL_TYPE_MODULE`, `JS_SetModuleLoaderFunc` (нормализация +
загрузка), `JS_NewCModule` (синтетические модули), кэш определений модулей **per `JSContext`**
(`ctx->loaded_modules`) — то есть ровно per realm, как и наш `require`-кэш ([ADR-004](ADR-004-one-runtime-realm-per-file.md)).

## Решение

1. **Формат файла определяется по исходнику, без переименований** (`.mjs` не вводится).
   `CompileScriptOrModule` в шиме: сначала компилируется классическая обёртка (function
   expression); если она не компилируется и в тексте есть модульный синтаксис на уровне
   выражения (`esmSyntaxRx`: `import`/`export`-объявление в начале строки или после `;`/`}`,
   либо `import.meta`), файл компилируется как модуль (`JS_EVAL_TYPE_MODULE | COMPILE_ONLY`) —
   его вердикт и его ошибка становятся окончательными. Файл без модульного синтаксиса — сценарий,
   как прежде, с теми же сообщениями об ошибках: для него после неудачи expression-формы парсер
   запускается в **program-форме**, как делал старый `LoadScenario` (`legacyErrors`), и снимок
   вердиктов корпуса не изменился ни на один файл.
2. **Загрузка ES-модуля файла правил** (`LoadScenario`): `EvalModuleNoPump` → промис вычисления
   модуля инспектируется точно как промис обёртки ([ADR-006](ADR-006-top-level-await.md)):
   rejected ⇒ синхронная ошибка загрузки (ретракт из трекера), pending ⇒ TLA, fulfilled ⇒ готово.
   Импорты разрешаются и загружаются на этапе компиляции (QuickJS делает `js_resolve_module`
   внутри `JS_Eval`), поэтому отсутствующий модуль — ошибка компиляции файла с понятным текстом
   `cannot find module "x" (imported from <file>)`.
3. **Загрузчик — в шиме, файловая система — в движке.** `JS_SetModuleLoaderFunc` установлен на
   runtime; C-трамплины зовут `goModuleNormalize`/`goModuleLoad`, а те — интерфейс
   `duktape.ModuleHost`, который реализует `ESEngine` (`wbrules/modules.go`):
   `ResolveModule(base, spec)` (относительные — от каталога импортирующего файла, абсолютные —
   как есть, «голые» — по `modulesDirs`; пробы `как есть`, `.js`, `.ts`, `.js→.ts`),
   `LoadModuleSource` (`.ts` — через `TSCompiler.Transpile`, так что source-map строк работает и
   для модулей), `InitCjsModule`/`InitImportMeta` (метаданные: `filename`/`static` и
   `url`/`filename`/`dirname`/`static` из стэша `_esModules[path]`). Протокол `Duktape.modSearch`
   удалён: `require()` тоже идёт через `ModuleHost` (id-семантика Duktape для `./x` сохранена
   в `resolveModuleID`).
4. **Interop в обе стороны, один экземпляр на файл в realm'е.** `import` CommonJS-файла: файл
   выполняется при загрузке импортёра (как в Node.js), затем оборачивается в синтетический
   модуль `qjd_new_cjs_module` (`default` = `module.exports`, именованные экспорты — снимок
   собственных перечислимых свойств; значения выставляет `init_func` из
   `JS_SetModulePrivateValue`). `require()` ES-модуля: модуль вычисляется синхронно; fulfilled ⇒
   пространство имён; pending ⇒ `Error` с `code = ERR_REQUIRE_ASYNC_MODULE`; rejected ⇒ синхронный
   `throw`. Кэш `require` дополнительно ключуется `file:<path>`, а скомпилированные модули
   помнятся в `rts.esModules[ctx,path]` — так `require("x")` после `import "x"` (и наоборот)
   находит **тот же** экземпляр, а не второй. Оба кэша сметаются при инвалидации realm'а.
5. **Один и тот же ошибочный модуль — одна запись в логе.** QuickJS исполняет тело модуля как
   async-функцию и при `throw` отбрасывает её промис без обработчика, а затем отклоняет промис
   модуля той же ошибкой — трекер ([ADR-005](ADR-005-microtask-pump-rejection-tracker.md))
   записывал её дважды. Ретракт теперь работает по **идентичности причины** (указатель объекта
   ошибки передаётся из C в трекер): `RetractTopPromiseRejection` снимает и запись самого
   промиса, и все записи с той же причиной.
6. **TypeScript.** Транспиляция — `module: preserve` (ESM-синтаксис остаётся как написан,
   CommonJS-формы `import x = require()` / `export =` — тоже; неиспользуемые импорты
   по-прежнему элидируются). Фоновая проверка запускается из **сгенерированного tsconfig**
   (`-p`): те же флаги, что были, плюс `module: preserve`, `allowImportingTsExtensions` и
   `paths: {"*": [<каталог модулей>/*, …]}` — «голые» спецификаторы типизируются по реальным
   файлам модулей, относительные — по соседним файлам (bundler-резолюция);
   `declare module "*"` остаётся запасным вариантом для неразрешимого.
7. **Редактор.** Новый RPC `Editor.ResolveModule {from, specifier} → {path, content}` —
   резолюция движка как сервис: homeui подкладывает исходники импортов в виртуальную ФС
   language service (относительные — рядом с файлом, «голые» — под `/wb-rules-modules/` с
   `paths`-подстановкой), транзитивно ([ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md)).

## Рассмотренные альтернативы

| Альтернатива | Почему отклонена |
|---|---|
| Транспилировать `import`/`export` в CommonJS (подход #231) | только `.ts`; нет живых привязок, `import.meta`, циклов по спецификации; interop-помощники сдвигают строки; `.js` с `import` так и не загружается |
| Формат по расширению (`.mjs`/`.mts`) | новые расширения нужно прописывать в watcher, редакторе, пакетах; пользователи ожидают, что `import` в `.js`/`.ts` просто работает (так делает TS-проверка) |
| Всегда компилировать как модуль, если парсится | модуль — strict mode и без `module`/`exports`: старые sloppy-файлы (`psName = …`) ломаются молча; нужен *позитивный* признак модульного синтаксиса |
| Детекция только регулярным выражением | ложные срабатывания в строках/комментариях; поэтому регулярка лишь *разрешает попытку* модульной компиляции после провала обёртки |
| `JS_DetectModule` из QuickJS | смотрит только на первый токен файла — `defineVirtualDevice(...)` перед `import` уже «не модуль» |
| Относительные импорты — по каталогам модулей (как `require("./x")`) | противоречит Node.js и TS-резолюции (редактор и проверка видят соседний файл); для `require()` старая семантика сохранена ради совместимости |
| Доступ к внутренностям `JSModuleDef` через общий translation unit (`qjs_build.c`) | не нужно: `JS_MKPTR(JS_TAG_MODULE, m)`, идемпотентные link/evaluate и `JS_GetModuleNamespace` покрывают всё публичным API |
| `require()` TLA-модуля — дождаться промиса (pump внутри `require`) | нарушает run-to-completion и порядок job'ов; Node.js тоже отказывает (`ERR_REQUIRE_ASYNC_MODULE`) |

## Последствия

Плюсы:
- `import`/`export`, `import()`, `import.meta`, TLA, циклы — по спецификации, в `.js` и `.ts`;
  модули на TypeScript; общий код рядом с правилами (`./lib/x.ts`).
- Ни один существующий файл не меняет поведения: вердикты корпуса (663 файла) байт-в-байт те же;
  `require()`/`module.static`/`Duktape.enc/dec` работают как раньше.
- Ошибки импортов — ошибки загрузки с трейсбеком модуля и source-map для `.ts`.
- Проверка типов и редактор видят реальные модули (`paths`, `Editor.ResolveModule`).

Минусы и риски:
- `require()` TLA-модуля успевает выполнить его синхронную часть до отказа (модуль остаётся
  загруженным и доступен `import()`); в Node.js модуль не запускается. Документировано.
- Именованные экспорты CommonJS-модуля — снимок на момент связывания (как в Node.js): свойство,
  добавленное в `exports` позже, через `import { x }` не видно.
- Файл, импортированный из соседнего файла правил, остаётся и самостоятельной точкой входа
  (watcher загружает все `.js`/`.ts`): общий код должен быть свободен от побочных эффектов, либо
  лежать в каталоге модулей.
- Диагностика фоновой проверки внутри импортированного модуля приписывается первому файлу
  батча (как и раньше для cross-file); при `paths` таких диагностик станет больше.
- `import.meta.static` доступен и файлу правил (состояние переживает перезагрузку файла) —
  единообразие предпочли особому случаю.
- Ещё одна поверхность в шиме (~500 строк `modules.go` + C-мост); покрыта тестами шима
  (interop, циклы, TLA, дубли, изоляция realm'ов) и движка (`rule_esm_test.go`, TS-сьют, churn).

## Ссылки

- [ADR-003](ADR-003-go-duktape-compatible-shim.md), [ADR-004](ADR-004-one-runtime-realm-per-file.md), [ADR-005](ADR-005-microtask-pump-rejection-tracker.md), [ADR-006](ADR-006-top-level-await.md), [ADR-012](ADR-012-background-check-parameters-checkjs.md), [ADR-015](ADR-015-homeui-editor-ts-language-service-plus-controller-verdict.md).
- `internal/quickjsduk/modules.go` (`ModuleHost`, `CompileScriptOrModule`, `EvalModuleNoPump`,
  `goRequire`, `goModuleNormalize`, `goModuleLoad`), `internal/quickjsduk/shim.c` (`qjd_compile_module`,
  `qjd_eval_module`, `qjd_new_cjs_module`, `qjd_install_module_loader`), `wbrules/modules.go`
  (`ResolveModule`, `LoadModuleSource`, `InitCjsModule`, `InitImportMeta`, `ResolveModuleForEditor`),
  `wbrules/escontext.go` (`LoadScenario`), `wbrules/tsloader.go` (`checkMany`, tsconfig, `SetModuleDirs`),
  `wbrules/editor.go` (`ResolveModule`), `asyncapi.mqtt-rpc.yml`.
- Тесты: `internal/quickjsduk/modules_test.go`, `wbrules/rule_esm_test.go`,
  `wbrules/rule_typescript_test.go` (`TestTsEsm*`), `wbrules/rule_leak_churn_test.go`
  (`TestLeakChurnEsmImports`), фикстуры `wbrules/testrules_esm*.js`, `testrules_ts_esm*.ts`,
  `wbrules/test-modules/test/esm/`, `wbrules/esmlib/`.
- README, раздел «ES-модули».
