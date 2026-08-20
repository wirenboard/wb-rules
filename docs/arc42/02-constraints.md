# 2. Ограничения

Ограничения, которые архитектура обязана соблюдать. Они объясняют, почему многие решения
в ADR выглядят «консервативно»: замена движка сделана
под существующий код, процесс и способ поставки, а не наоборот.

## 2.1. Технические ограничения

| Ограничение | Суть | Следствия для архитектуры |
|---|---|---|
| **Go + cgo, один процесс** | wb-rules — Go-программа (`go.mod`, Go 1.26); движок JS встроен через cgo; все правила выполняются в одном процессе `/usr/bin/wb-rules` | движок должен быть встраиваемой C-библиотекой (QuickJS), а не внешним рантаймом; горутины мигрируют между OS-потоками между cgo-вызовами ⇒ `JS_UpdateStackTop` и «глубокие» операции одним C-вызовом (ADR-003) |
| **Один поток выполнения JS** | весь JS исполняется в «engine loop» (`syncQueue`, `CallSync`); драйвер и RPC — отдельные горутины, но в JS входят только через очередь | синхронный бесконечный цикл блокирует все правила ⇒ watchdog (ADR-008); per-file изоляция по CPU невозможна без смены модели |
| **Общий heap** | один `JSRuntime` на процесс, realm (`JSContext`) на файл (ADR-004) | лимит памяти только процессный (`JS_SetMemoryLimit` 512 МиБ, systemd `MemoryMax=50%`); «RSS на скрипт» недоступен |
| **Целевые платформы** | armhf (WB6/WB7, GOARM=6) и arm64 (WB8), Debian (trixie на WB8); кросс-сборка `CGO_ENABLED=1` с `arm-linux-gnueabihf-gcc`/`aarch64-linux-gnu-gcc` | никаких зависимостей без armhf-сборки (Bun/Deno отпали); arm64 линкуется `-extldflags=-fuse-ld=bfd` |
| **RAM-бюджет контроллера** | десятки МБ на процесс; на WB8 steady-state RSS 36.7 МБ (Duktape — 37.6) | отказ от Node/изолятов (40–50 МБ базы + 8–10 МБ/скрипт) |
| **Поставка только deb-пакетами** | установка/обновление через apt из репозиториев WB (testing set, release); никаких rsync/pipeline-артефактов на контроллер | движок — исходники в submodule, компилируются при сборке пакета; `Breaks:` на старые `wb-rules-system`/`wb-mqtt-confed` |
| **Плагин `wbgo.so`** | драйвер MQTT-устройств — Go-plugin `/usr/lib/wb-rules/wbgo.so` из закрытого `wbgo-private`, собирается тем же toolchain (go1.26.5) и теми же флагами (`-trimpath -ldflags "-s -w"`), иначе `plugin.Open` отказывает; `-race` требует race-сборки плагина | бинарь и плагин — одна сборка; CI собирает `.so` из master `wbgo-private`; тесты используют `wbrules/wbgo.so` без `-trimpath` |
| **MQTT-конвенции Wiren Board** | топики `/devices/<dev>/controls/<ctrl>`, `/meta/*`, `/on`; retained-значения; брокер mosquitto (unix-socket или tcp 1883); `wbgong` v0.7.4 API | интерфейс устройств — через `wbgong.Driver`, не напрямую MQTT; поток `/wbrules/log/+`, `/wbrules/updates/+`, RPC `/rpc/v1/wbrules/Editor/...` — часть публичного контракта homeui |
| **Обратная совместимость ES5/API** | всё, что работало на Duktape 1.0.2, должно работать: Duktape-стиль CommonJS (`require`, `module.static`, `Duktape.modSearch`), точные строки ошибок, `Duktape.enc/dec`, колбэк-сигнатуры, «swallow-and-log» при записи в контрол | шим воспроизводит семантику Duktape, включая квирки (ADR-003, ADR-009); новые возможности — только аддитивно |
| **Нет GitHub Actions** | CI — только Jenkins (`buildDebGolangWbgo`, armhf+arm64, lintian); Actions отключены на уровне репозитория | проверки, требующие приватных сабмодулей (корпус), на CI не запускаются; `TestCorpus` — локально |
| **Инфраструктура systemd** | `Restart=on-failure RestartSec=1`, `MemoryHigh=30% MemoryMax=50%`, `After=mosquitto` | crash при загрузке файла ⇒ перезапуск ⇒ loadguard-карантин после 3 падений; порядок `dpkg --configure` влияет на доступность зависимостей при старте |

## 2.2. Организационные ограничения

| Ограничение | Суть |
|---|---|
| **Существующие пользователи и корпус** | установленная база контроллеров с правилами; внутренний регрессионный корпус скриптов правил — эталон совместимости; содержимое не публикуется (только «internal Wiren Board rules corpus») |
| **Приватные репозитории** | `wbgo-private` (драйвер, обфусцированный IP, ABI публичен через `wbgong`), `wb-rules-corpus` (корпус; submodule `wbrules/testdata/corpus` с `update = none`, иначе `git clone --recursive` падает и оставляет `third_party/quickjs` пустым) |
| **Stacked PR** | работа разбита: #223 (`quickjs-core` → `master`: движок, async, корпус, без TS), #224 (`quickjs-ts` → `quickjs-core`: TS), homeui #1202; `debian/control|rules` в #223 байт-в-байт как в master | ревью и CI проходят для каждого PR отдельно; нижний PR должен быть самодостаточным |
| **Testing set** | поставка ранних сборок через aptly testing set `quickjs-typescript` (`experimental.quickjs-typescript`, версии `*~exp~quickjs+ts~*`); stable остаётся на 2.46.x (Duktape) до мержа — системные скрипты должны работать на обоих |
| **Зафиксированные продуктовые решения** | ряд решений принят явно и не подлежит пересмотру без нового ADR: Bellard QuickJS (не quickjs-ng); решения по TypeScript и редактору фиксируются в ADR PR #224 |

## 2.3. Конвенции

| Конвенция | Применение |
|---|---|
| **Codestyle WB** | Go — `gofmt`/линтеры из `codestyle/` (submodule); JS-библиотека `lib.js` — ES5-совместимый стиль там, где код исполняется и в старом движке (`wb-rules-system`, `wb-scenarios` — строго ES5); примеры в README — стрелочные функции |
| **Языки документации** | README, changelog, arc42 — русский; комментарии в коде — английский |
| **Сообщения лога как контракт** | префиксы `async rule error:`, `control X/Y: write ignored (...) at file:line`, `[loadguard]`, `execution timed out: exceeded the 10s js-timeout` — разбираются homeui и людьми; менять только с обновлением редактора |
| **Лицензии** | wb-rules — MIT-WB (`LICENSE`, Contactless Devices LLC); QuickJS — MIT (`third_party/quickjs/LICENSE`, Bellard/Gordon); `debian/copyright` wb-rules — уточнить, отражает ли он QuickJS |
| **Версионирование** | `debian/changelog` `2.47.0~quickjs<N>`; `CONFIG_VERSION` в `internal/quickjsduk/qjs_build.c` обновляется вместе с submodule |
| **Тестирование** | новые баги закрепляются минимальными синтетическими тестами в репо (`rule_corpus_regress_test.go` и др.), а не ссылками на корпус; leak-канарейки (`MemoryUsed()` после 2×`RunGC()`); сьюты на fake broker `wbgong/testutils` |
| **Публичные интерфейсы** | MQTT-RPC описывается в `asyncapi.mqtt-rpc.yml`; retained-топики для служебных данных не вводятся |

## 2.4. Ключевые числа, закреплённые ограничениями

| Параметр | Значение | Откуда |
|---|---|---|
| Лимиты процесса | `MemoryHigh=30%`, `MemoryMax=50%`, `Restart=on-failure`, `RestartSec=1` | `debian/wb-rules.service` |
| Очереди | `SYNC_QUEUE_LEN=32`, `EventBuffer` cap 16, `CallSync` timeout 120 с | `wbrules/engine.go` |
