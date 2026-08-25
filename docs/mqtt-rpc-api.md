# MQTT RPC из правил: справочник API модуля `wb-mqtt-rpc`

Модуль доступен как глобальный объект `MqttRpc` и как `require('wb-mqtt-rpc')`
(один и тот же экземпляр в пределах файла правил). Все методы, которые ходят в
сеть, возвращают `Promise`. Типы — в `wb-rules.d.ts` (`declare namespace MqttRpc`),
редактор подсказывает сигнатуры и поля.

Обозначения: `[x]` — необязательный аргумент; `options` — объект параметров;
время в мс, если не сказано иное; `Promise<T>` — что вернёт `await`.

Общие для всех хелперов параметры `options` (`HelperOptions`):

| Поле | Тип | Значение |
|---|---|---|
| `timeout` | number | сколько ждать ответа сервиса; по умолчанию `MqttRpc.defaults.timeout` (60000); `0` или `Infinity` — без ограничения |
| `waitForMethod` | boolean \| number | перед отправкой дождаться появления метода: `true` — не дольше `timeout`, число — свой предел |

Для операций wb-mqtt-serial с портом/устройством там же принимаются
`totalTimeout`, `responseTimeout`, `frameTimeout` (`SerialTimeouts`, мс) —
бюджет времени на стороне драйвера; если `timeout` не задан, ожидание ответа
берётся не меньше `totalTimeout` с запасом.

## 1. Общий клиент

### `MqttRpc.call(driver, service, method[, params[, options]]) → Promise<any>`

Вызов `/rpc/v1/<driver>/<service>/<method>`. `params` — объект (по умолчанию
`{}`), уходит в поле `params` запроса. Разрешается значением `result` ответа.

Ошибки: `MqttRpc.RpcError` — сервис ответил ошибкой (`code`, `message`, `data`
как в ответе, плюс `driver`, `service`, `method`); `MqttRpc.TimeoutError` —
ответа нет за `timeout` (`code` −33000, `data` `"MqttTimeoutError"`);
`TypeError` — неверные аргументы (пустые имена, символы `/`, `+`, `#`).

### `MqttRpc.hasMethod(driver, service, method[, timeoutMs | { timeout }]) → Promise<boolean>`

Обслуживается ли метод сейчас (retained-топик `/rpc/v1/<driver>/<service>/<method>`).
`false`, если за время ожидания (по умолчанию `MqttRpc.defaults.hasMethodTimeout`,
3000 мс; `0` — ждать `true` бесконечно) топик не появился. Ответ запоминается
и обновляется по подписке.

### `MqttRpc.waitForMethod(driver, service, method[, timeoutMs | { timeout }]) → Promise<void>`

Выполняется, как только метод появится; `TimeoutError` (`data`
`"MqttMethodUnavailable"`) по истечении ожидания (по умолчанию
`defaults.timeout`; `0` — бесконечно).

### `MqttRpc.service(driver, service[, methods]) → ServiceProxy`

Объект с `call(method, params, options)`, `hasMethod(method, timeout)`,
`waitForMethod(method, timeout)`, привязанными к сервису, и функцией
`(params, options) → Promise` на каждое имя из `methods`. Имена не должны
совпадать с `call`/`hasMethod`/`waitForMethod`/`driver`/`service`
(`TypeError`). В TypeScript: `MqttRpc.service<MyApi>(...)`, где
`MyApi = { Get: MqttRpc.Method<Params, Result> }`.

### Ошибки и константы

| Имя | Описание |
|---|---|
| `MqttRpc.RpcError(code, message[, data])` | ошибка сервиса; поля `code`, `message`, `data`, `driver`, `service`, `method`; бросается из обработчиков своих методов, чтобы ответить конкретным кодом |
| `MqttRpc.TimeoutError` | наследник `RpcError`: клиент не дождался (`code` −33000; `data` `"MqttTimeoutError"` для ответа, `"MqttMethodUnavailable"` для `waitForMethod`) |
| `MqttRpc.ModbusError(code, message)` | исключение Modbus от устройства (`code` — код исключения: 1 illegal function, 2 illegal data address, …) |
| `MqttRpc.ErrorCode` | `PARSE_ERROR` −32700, `INVALID_REQUEST` −32600, `METHOD_NOT_FOUND` −32601, `INVALID_PARAMS` −32602, `INTERNAL_ERROR` −32603, `SERVER_ERROR` −32000, `TIMEOUT` −33000 |
| `MqttRpc.defaults` | `{ timeout: 60000, hasMethodTimeout: 3000 }` — можно менять, действует в пределах файла |
| `MqttRpc.clientId` | идентификатор клиента этого файла (последний уровень топиков запросов) |
| `MqttRpc.DEFAULT_SERVICE_DRIVER` | `"wbrules-scripts"` |

## 2. Сервер: свои методы

### `MqttRpc.defineService([driver,] service, methods) → { driver, service, methods: string[] }`

Публикует методы по адресу `/rpc/v1/<driver>/<service>/<Имя>` (по умолчанию
`driver` = `wbrules-scripts`). `methods` — объект `{ Имя: обработчик }`, где
обработчик — функция `(params, request) → result | Promise<result>` либо
`MqttRpc.method(schema, handler)` (см. ниже). `request`: `{ driver, service,
method, clientId, id, topic }`.

Для каждого метода выставляется retained-топик доступности (`1`); при
выгрузке файла (перезагрузка, удаление; при остановке движка — только с
`-cleanup`) топики очищаются. Одну пару `driver/service` обслуживает один
файл: повторное объявление из другого файла — ошибка загрузки; из того же
файла — добавление/замена методов (предупреждение в логе).

Ответы: результат обработчика (`undefined`, функция → `null`); исключение
`RpcError` → его `code`/`message`/`data`; другое исключение → −32603 (и строка
в логе правил); неизвестный метод → −32601; невалидный JSON → −32700; запрос
без `id` → −32600; `params` не объект и не массив → −32602;
несериализуемый результат → −32603.

### `MqttRpc.method(schema, handler) → MethodSpec`

Метод с проверкой параметров по JSON-схеме до вызова обработчика: не
прошедший запрос получает −32602 с первой проблемой в `message` и списком
`[{ path, message }]` в `data`. TypeScript выводит тип `params` обработчика из
схемы (`MqttRpc.FromSchema<S>`) — в `.ts` и `.js` файлах. Поддерживаемое
подмножество схемы: `type` (и массив типов), `enum`, `const`,
`properties`/`required`/`additionalProperties`, `items`/`minItems`/`maxItems`,
`minLength`/`maxLength`/`pattern`, `minimum`/`maximum`/`exclusiveMinimum`/
`exclusiveMaximum`/`multipleOf`, `anyOf`/`oneOf`/`allOf`/`not`; прочие
ключевые слова игнорируются. Объектная форма `{ params: schema, handler }`
тоже принимается (без вывода типов).

### `MqttRpc.validate(schema, value) → ValidationProblem[]`

Та же проверка отдельно: `[]`, если значение подходит, иначе список
`{ path, message }` (`path` — JSON-указатель, `/` для корня).

## 3. Сервисы контроллера

У каждого объекта сервиса:

| Член | Описание |
|---|---|
| `driver` | идентификатор драйвера в топиках |
| `rpc.<service>.<Method>(params[, options])` | методы RPC как в документации сервиса (см. таблицу в README) |
| `isAvailable([timeout]) → Promise<boolean>` | доступен ли сервис (presence одного из его методов) |
| `waitUntilAvailable([timeout]) → Promise<void>` | дождаться появления сервиса |

Общие типы аргументов:

* **Порт** (`PortSpec`): `'/dev/ttyRS485-1'` (настройки линии по умолчанию
  9600 N 8 2), `{ path, baudRate, parity, dataBits, stopBits }` (принимаются и
  snake_case-поля из ответов сервисов), Modbus TCP — `'10.0.0.5:502'`,
  `{ ip, port }` или `{ address, port }`.
* **Момент времени** (`Db.Time`): `Date` или миллисекунды с начала эпохи
  (`Date.now()`).
* **Канал истории** (`Db.ChannelSpec`): `'device/control'` или
  `['device', 'control']`.

### 3.1 `MqttRpc.serial` — wb-mqtt-serial

#### `serial.device(target) → Serial.Device`

`target` — MQTT-идентификатор устройства из конфига (`'wb-map12e_1'`; порт,
протокол и адрес возьмутся из конфига драйвера) либо `{ port, slaveId[,
deviceType][, rtuOverTcp] }` (`deviceType` — тип из шаблона, нужен для
`settings/read/write`; `rtuOverTcp` — кадры RTU через TCP-сокет вместо
Modbus TCP).

Методы объекта устройства (`options` — `HelperOptions & SerialTimeouts`):

| Метод | Возвращает | Описание |
|---|---|---|
| `readHolding(address[, count][, options])` | `Promise<number[]>` | функция 3; до 125 регистров, беззнаковые 16-битные |
| `readInput(address[, count][, options])` | `Promise<number[]>` | функция 4 |
| `readCoils(address[, count][, options])` | `Promise<boolean[]>` | функция 1; до 2000 бит |
| `readDiscrete(address[, count][, options])` | `Promise<boolean[]>` | функция 2 |
| `writeHolding(address, value \| values[][, options])` | `Promise<void>` | одно значение — функция 6, массив — 16 (до 123) |
| `writeCoil(address, bool \| bools[][, options])` | `Promise<void>` | одно — функция 5, массив — 15 (до 1968) |
| `modbus(fn, address[, { count, data, writeAddress, writeCount, …options }])` | `Promise<string>` | произвольная функция (1–6, 15, 16, 23); `data` — hex для записи; ответ — hex данных (пусто для записи) |
| `raw(hex, responseSize[, options])` | `Promise<string>` | произвольные байты в порт (только для явного порта) |
| `settings([{ force, …options }])` | `Promise<{ parameters, fw?, model? }>` | параметры устройства (кэш драйвера, `force` — перечитать) |
| `readChannel(name[, options])` | `Promise<any>` | текущее значение канала по имени из шаблона |
| `readChannels(...names \| names[][, options])` | `Promise<Record<string, any>>` | несколько каналов |
| `readParameter(id[, options])` / `readParameters(...ids)` | `Promise<any>` / `Promise<Record>` | параметры по идентификаторам |
| `writeChannel(name, value)` / `writeChannels({ name: value })` | `Promise<void>` | запись каналов |
| `setParameter(id, value)` / `setParameters({ id: value })` | `Promise<void>` | запись параметров |
| `read({ channels?, parameters? })` | `Promise<{ channels, parameters, readonly }>` | каналы и параметры одним запросом |
| `write({ channels?, parameters? })` | `Promise<void>` | запись одним запросом |
| `probe([{ protocol }])` | `Promise<ScannedDevice \| null>` | кто отвечает по адресу (только явный порт); `null` — никто |
| `setPolling(enabled)`, `pausePolling()`, `resumePolling()` | `Promise<void>` | опрос устройства драйвером (пауза сама снимается через 10 мин) |
| `withPollingPaused(fn)` | `Promise<результат fn>` | пауза опроса вокруг `fn(device)`; опрос возобновляется и при исключении |
| `firmwareInfo()`, `updateFirmware([options])`, `waitForFirmwareUpdate()`, `restoreFirmware()`, `clearFirmwareError()` | см. ниже | для устройства по id порт и адрес берутся из конфига |
| `resolve()` | `Promise<{ port, slaveId, type }>` | порт/адрес/тип по конфигу драйвера (читается каждый раз) |
| `id`, `port`, `slaveId`, `deviceType` | поля | как было задано |

Ошибки Modbus от устройства — `MqttRpc.ModbusError`; ошибки порта/драйвера —
`RpcError` (например −32600 «Request timeout» при превышении `totalTimeout`).

#### Конфигурация и порты

| Метод | Возвращает | Описание |
|---|---|---|
| `serial.devices([{ lang }])` | `Promise<ConfiguredDeviceInfo[]>` | все устройства конфига: `{ id, type, name, slaveId (число для Modbus), enabled, port, config }`; `id` — MQTT-идентификатор (явный `id`, иначе `<mqtt-id шаблона>_<slave_id>`) |
| `serial.ports()` | `Promise<ConfiguredPort[]>` | настроенные порты |
| `serial.config([{ lang }])` | `Promise<{ config, schema, types }>` | конфиг драйвера, его схема, типы устройств |
| `serial.deviceTypes([{ lang }])` | `Promise<(DeviceType & { group })[]>` | все типы плоским списком |
| `serial.deviceSchema(type)` | `Promise<any>` | JSON-схема настроек типа |
| `serial.uploadTemplate(filename, content[, { force, lang }])` | `Promise<{ types }>` | загрузить пользовательский шаблон (строка или объект) |
| `serial.deleteTemplate(type[, { force, lang }])` | `Promise<{ types }>` | удалить шаблон |

#### Сканирование и настройка

| Метод | Возвращает | Описание |
|---|---|---|
| `serial.scan(port[, { command, mode, totalTimeout }])` | `Promise<ScannedDevice[]>` | Fast Modbus-сканирование порта; прерванное — исключение `Error` с полем `devices` (найденные до ошибки) |
| `serial.probe(port, slaveId[, { protocol }])` | `Promise<ScannedDevice \| null>` | кто отвечает по адресу |
| `serial.setup(port, items[, { totalTimeout }])` | `Promise<{}>` | смена адресов/настроек линии: `items` — `[{ slaveId \| sn, baudRate?, parity?, dataBits?, stopBits?, set: { slaveId?, baudRate?, parity?, stopBits? } }]` (текущие настройки устройства по умолчанию 9600 N 8 1) |

#### Прошивки (`serial.*` — только последовательные порты; `deviceManager.*` — и TCP)

| Метод | Возвращает | Описание |
|---|---|---|
| `firmwareInfo(port, slaveId[, { protocol, rtuOverTcp }])` | `Promise<FirmwareInfo>` | `fw`, `available_fw`, `fw_has_update`, `bootloader`, `components`, `model`, … |
| `updateFirmware(port, slaveId[, { type, wait, timeout, startTimeout, onProgress }])` | `Promise<void>` (`Promise<"Ok">` при `wait: false`) | обновить `firmware` (по умолчанию), `bootloader` или `component`; ждёт окончания по state-топику: `onProgress(entry)` на каждое изменение `progress`, ошибка обновления — исключение с полем `state`; `startTimeout` (10 с) — сколько ждать появления устройства в state |
| `waitForFirmwareUpdate(port, slaveId[, options])` | `Promise<void>` | дождаться окончания уже идущего обновления |
| `restoreFirmware(port, slaveId)` | `Promise<"Ok">` | восстановить прошивку устройства в загрузчике |
| `clearFirmwareError(port, slaveId[, { type }])` | `Promise<"Ok">` | сбросить запись об ошибке обновления |
| `firmwareUpdateState()` | `Promise<{ devices: [{ port: { path }, slave_id, progress, type, error }] }>` | текущее состояние обновлений |

### 3.2 `MqttRpc.db` — wb-mqtt-db (история)

| Метод | Возвращает | Описание |
|---|---|---|
| `db.query(channel \| channels[], [options])` | `Promise<{ values: HistoryRecord[], hasMore }>` | записи одного или нескольких каналов (по запросу на канал, слитые по времени). `options`: `since`/`until` (момент), `last` (мс, несовместим с `since`), `limit` (на канал), `minInterval` (мс), `maxRecords` (усреднение базой), `requestTimeout` (с), `afterUid` (продолжение). Запись: `{ channel, device, control, time: Date, value (number \| string), min, max, retain, uid }` |
| `db.lastValue(channel)` | `Promise<HistoryRecord \| undefined>` | последняя запись канала |
| `db.average(channel, { last \| since, until[, buckets] })` | `Promise<number \| undefined>` | среднее по периоду: среднее до `buckets` (100) интервалов, усреднённых базой (интервалы выровнены по эпохе, поэтому края окна весят чуть иначе); `undefined`, если числовых записей нет |
| `db.channels()` | `Promise<ChannelInfo[]>` | все каналы базы: `{ channel, device, control, items, lastTime: Date }` |

Границы окна передаются базе целыми секундами (`since` округляется вниз,
`until` — вверх).

### 3.3 `MqttRpc.rules` — редактор правил wb-rules

| Метод | Возвращает | Описание |
|---|---|---|
| `rules.list()` | `Promise<FileEntry[]>` | файлы правил: `{ virtualPath, enabled, error?, rules, devices, timers }` |
| `rules.load(path)` | `Promise<{ content, enabled, error? }>` | текст файла |
| `rules.save(path, content)` | `Promise<{ path, error?, traceback? }>` | сохранить (ошибка загрузки нового текста — внутри результата) |
| `rules.remove(path)`, `rules.rename(path, newPath)`, `rules.enable(path)`, `rules.disable(path)` | `Promise<void>` | управление файлами |
| `rules.check(path[, { interval, timeout }])` | `Promise<{ status, diags }>` | вердикт проверки типов; опрашивает каждые `interval` (200) мс, пока `pending`; `timeout` (30000) → `TimeoutError` |
| `rules.types()` | `Promise<string>` | текст `wb-rules.d.ts` |

Ошибки сервиса приходят как `RpcError` с кодами 1000–1009 и `data`
`"EditorError"`.

### 3.4 `MqttRpc.confed` — редактор конфигов wb-mqtt-confed

| Метод | Возвращает | Описание |
|---|---|---|
| `confed.list()` | `Promise<ConfigEntry[]>` | конфиги: `{ title, description, configPath, schemaPath, editor }` |
| `confed.load(path)` | `Promise<{ configPath, content, schema, editor }>` | содержимое (разобранный JSON) и схема |
| `confed.save(path, content)` | `Promise<void>` | записать; зависимые сервисы перезапускаются |
| `confed.update(path, fn)` | `Promise<content>` | загрузить, изменить (`fn(content, loaded)` меняет объект на месте или возвращает новый; можно `async`), сохранить; возвращает сохранённое |

### 3.5 `MqttRpc.logs` — журнал wb-mqtt-logs

| Метод | Возвращает | Описание |
|---|---|---|
| `logs.read([options])` | `Promise<LogRecord[]>` | записи: `{ time: Date, level (6 = info), msg, service?, cursor? }`. `options`: `service`, `boot` (хэш из `boots()`), `since` (момент), `levels` (0–7), `pattern`, `regex`, `caseSensitive`, `cursor`, `direction` (`backward`/`forward`), `limit` (≤ 100). Без `since` — новые первыми с конца журнала; с `since` (или `direction: 'forward'`) — старые первыми, начиная с этого момента |
| `logs.tail(service[, count])` | `Promise<LogRecord[]>` | последние `count` (50) записей сервиса |
| `logs.services()` | `Promise<string[]>` | известные сервисы |
| `logs.boots()` | `Promise<{ hash, start: Date, end?: Date }[]>` | загрузки системы |
| `logs.cancel()` | `Promise<void>` | прервать идущее чтение |

### 3.6 `MqttRpc.diag` — wb-diag-collect

| Метод | Возвращает | Описание |
|---|---|---|
| `diag.collect([{ timeout }])` | `Promise<{ basename, fullname }>` | собрать архив диагностики и дождаться его (по умолчанию до 300000 мс); неудача — `Error` |
| `diag.isAlive()` | `Promise<boolean>` | отвечает ли сервис |

### 3.7 `MqttRpc.deviceManager` — сканер шины wb-device-manager

| Метод | Возвращает | Описание |
|---|---|---|
| `deviceManager.scan([options])` | `Promise<ScannedDevice[]>` | запустить сканирование и дождаться результата. `options`: `port` (путь или `host:port`; по умолчанию все порты), `protocol`/`rtuOverTcp`, `type` (`extended`/`standard`/`bootloader`), `preserveOldResults`, `outOfOrderSlaveIds`, `timeout` (600000), `startTimeout` (10000), `onProgress(state)`. Ошибка сканирования — `Error` с полем `state` |
| `deviceManager.stopScan()` | `Promise<void>` | остановить |
| `deviceManager.state()` | `Promise<ScanState \| null>` | retained-состояние: `{ scanning, progress, scanning_ports, devices, error }` |
| `firmwareInfo`, `updateFirmware`, `waitForFirmwareUpdate`, `restoreFirmware`, `clearFirmwareError`, `firmwareUpdateState` | как у `serial` | та же сигнатура, порты и TCP (`'host:port'`) |

Занятый сервис отвечает `RpcError` −33100.

### 3.8 `MqttRpc.dali` — wb-mqtt-dali

| Метод | Возвращает | Описание |
|---|---|---|
| `dali.buses()` | `Promise<(Bus & { gateway: { id, name } })[]>` | все шины всех шлюзов плоским списком |
| `dali.send(busId, command \| commands[])` | `Promise<CommandResult[]>` | выполнить команды (`'DAPC(A0, 0xFE)'`) атомарно; по результату `{ status, response?, error? }` на команду |
| `dali.commands()` | `Promise<CommandInfo[]>` | известные команды |

## 4. Прочее

* `trackMqtt(topic, callback, { cache: false })` — опция движка, которой
  модуль пользуется для потоков запросов/ответов: отключает воспроизведение
  последнего значения топика поздним подписчикам (решает первый подписчик
  шаблона).
* Все подписки, таймеры и объявленные методы принадлежат файлу правил и
  освобождаются при его перезагрузке.
