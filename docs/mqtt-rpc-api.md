# MQTT RPC из правил: справочник API модуля `wb-mqtt-rpc`

Модуль доступен как глобальный объект `MqttRpc` и как `require('wb-mqtt-rpc')`
(в пределах одного файла правил это один и тот же экземпляр). Все методы,
которые обращаются к сети, возвращают `Promise`, поэтому примеры ниже написаны
для `async`-функций (например, `then: async () => { ... }` в `defineRule`).
Типы описаны в `wb-rules.d.ts` (`declare namespace MqttRpc`): редактор
подсказывает сигнатуры и поля, а файлы `.js` и `.ts` проверяются на
контроллере.

Обозначения: `[x]` — необязательный аргумент; `options` — объект
дополнительных настроек вызова (всегда последний аргумент); время — в
миллисекундах, если не сказано иное; `→ T` — значение, которым разрешится
`Promise`.

Общие поля `options` для всех хелперов (`HelperOptions`):

| Поле | Тип | Значение |
|---|---|---|
| `timeout` | number | сколько ждать ответа сервиса; по умолчанию `MqttRpc.defaults.timeout` (60000); `0` или `Infinity` — без ограничения |
| `waitForMethod` | boolean \| number | перед отправкой дождаться появления метода: `true` — не дольше `timeout`, число — собственный предел ожидания |

Операции wb-mqtt-serial с портом или устройством принимают там же
`totalTimeout`, `responseTimeout`, `frameTimeout` (`SerialTimeouts`, мс) —
бюджет времени на стороне драйвера; если `timeout` не задан, ответа ждут не
меньше `totalTimeout` с запасом.

```javascript
// options везде передаётся последним аргументом
const regs = await MqttRpc.serial.device('wb-map12e_1').readHolding(0x80, 2, { totalTimeout: 3000 });
const files = await MqttRpc.rules.list({ timeout: 5000, waitForMethod: true });
```

## 1. Общий клиент

### `MqttRpc.call(driver, service, method[, params[, options]]) → any`

Вызов метода `/rpc/v1/<driver>/<service>/<method>`. `params` — объект (по
умолчанию `{}`), который уходит в поле `params` запроса. `Promise`
разрешается значением поля `result` ответа.

```javascript
const ports = await MqttRpc.call('wb-mqtt-serial', 'ports', 'Load');
const reply = await MqttRpc.call('wb-mqtt-serial', 'port', 'Load', {
  device_id: 'wb-map12e_1', function: 3, address: 0x80, count: 2,
}, { timeout: 15000 });
log('{}', reply.response);
```

Ошибки: `MqttRpc.RpcError` — сервис ответил ошибкой (поля `code`, `message`,
`data` как в ответе, плюс `driver`, `service`, `method`);
`MqttRpc.TimeoutError` — ответ не пришёл за `timeout` (`code` −33000, `data`
`"MqttTimeoutError"`); `TypeError` — неверные аргументы (пустые имена,
символы `/`, `+`, `#`).

```javascript
try {
  await MqttRpc.call('wbrules', 'Editor', 'Load', { path: 'nope.js' });
} catch (e) {
  if (e instanceof MqttRpc.TimeoutError) log.error('wb-rules не отвечает');
  else if (e instanceof MqttRpc.RpcError) log.error('{}/{}/{}: {} ({})', e.driver, e.service, e.method, e.message, e.code);
  else throw e;
}
```

### `MqttRpc.hasMethod(driver, service, method[, timeoutMs | { timeout }]) → boolean`

Обслуживается ли метод сейчас (есть ли retained-топик
`/rpc/v1/<driver>/<service>/<method>`). Возвращает `false`, если за время
ожидания (по умолчанию `MqttRpc.defaults.hasMethodTimeout`, 3000 мс; `0` —
ждать `true` бесконечно) топик так и не появился. Ответ запоминается и
обновляется по подписке, поэтому повторные проверки выполняются мгновенно.

```javascript
if (await MqttRpc.hasMethod('wb-mqtt-serial', 'port', 'Scan', 500)) {
  log('сканирование доступно');
}
```

### `MqttRpc.waitForMethod(driver, service, method[, timeoutMs | { timeout }]) → void`

Разрешается, как только метод появится; по истечении времени ожидания (по
умолчанию `defaults.timeout`; `0` — ждать бесконечно) отклоняется с
`TimeoutError` (`data` `"MqttMethodUnavailable"`). Удобно при старте
контроллера, когда правила могут загрузиться раньше сервисов.

```javascript
await MqttRpc.waitForMethod('db_logger', 'history', 'get_values', 0); // ждать сколько угодно
const chans = await MqttRpc.db.channels();
```

### `MqttRpc.service(driver, service[, methods]) → ServiceProxy`

Объект с методами `call(method, params, options)`, `hasMethod(method, timeout)`
и `waitForMethod(method, timeout)`, привязанными к сервису, а также с функцией
`(params, options) → Promise` для каждого имени из `methods`. Имена методов
не должны совпадать с `call`, `hasMethod`, `waitForMethod`, `driver` и
`service` (иначе — `TypeError`).

```javascript
const editor = MqttRpc.service('wbrules', 'Editor', ['List', 'Load']);
const files = await editor.List();
const first = await editor.Load({ path: files[0].virtualPath });
const removed = await editor.call('Remove', { path: 'old.js' });
```

В TypeScript прокси типизируется аргументом типа:

```typescript
type StoreApi = {
  Get: MqttRpc.Method<{ key: string }, { value: number }>;
  Ping: MqttRpc.Method<{}, 'pong'>;
};
const store = MqttRpc.service<StoreApi>('my-driver', 'Store', ['Get', 'Ping']);
const value = (await store.Get({ key: 'k' })).value; // number
```

### Ошибки и константы

| Имя | Описание |
|---|---|
| `MqttRpc.RpcError(code, message[, data])` | ошибка сервиса; поля `code`, `message`, `data`, `driver`, `service`, `method`. Бросайте её из обработчиков своих методов, чтобы ответить конкретным кодом |
| `MqttRpc.TimeoutError` | наследник `RpcError`: клиент не дождался ответа (`code` −33000; `data` — `"MqttTimeoutError"` при ожидании ответа, `"MqttMethodUnavailable"` при `waitForMethod`) |
| `MqttRpc.ModbusError(code, message)` | исключение Modbus, полученное от устройства (`code` — код исключения: 1 — illegal function, 2 — illegal data address, …) |
| `MqttRpc.ErrorCode` | `PARSE_ERROR` −32700, `INVALID_REQUEST` −32600, `METHOD_NOT_FOUND` −32601, `INVALID_PARAMS` −32602, `INTERNAL_ERROR` −32603, `SERVER_ERROR` −32000, `TIMEOUT` −33000 |
| `MqttRpc.defaults` | `{ timeout: 60000, hasMethodTimeout: 3000 }`; значения можно менять, они действуют в пределах файла |
| `MqttRpc.clientId` | идентификатор клиента для этого файла (последний уровень топиков запросов) |
| `MqttRpc.DEFAULT_SERVICE_DRIVER` | `"wbrules-scripts"` |

```javascript
MqttRpc.defaults.timeout = 10000; // для всех вызовов из этого файла
log('этот файл обращается к RPC как {}', MqttRpc.clientId);
try {
  await MqttRpc.serial.device('wb-map12e_1').readHolding(0xffff);
} catch (e) {
  if (e instanceof MqttRpc.ModbusError && e.code === 2) log('устройство не знает такого регистра');
  else throw e;
}
```

## 2. Сервер: свои методы

### `MqttRpc.defineService([driver,] service, methods) → { driver, service, methods: string[] }`

Публикует методы по адресам `/rpc/v1/<driver>/<service>/<Имя>` (по умолчанию
`driver` — `wbrules-scripts`). `methods` — объект `{ Имя: обработчик }`, где
обработчик — либо функция `(params, request) → result | Promise<result>`,
либо `MqttRpc.method(schema, handler)` (см. ниже). `request` — объект
`{ driver, service, method, clientId, id, topic }`.

```javascript
// mosquitto_pub -t /rpc/v1/wbrules-scripts/Lights/Set/cli -m '{"id":1,"params":{"room":"hall","on":true}}'
MqttRpc.defineService('Lights', {
  Set: (params, request) => {
    dev['lights/' + params.room] = !!params.on;
    log('{} попросил {}', request.clientId, params.room);
    return { room: params.room, on: !!params.on };
  },
  // асинхронный обработчик: ответ уйдёт, когда промис разрешится
  Status: async () => ({ hall: dev['lights/hall'], last: await MqttRpc.db.lastValue('lights/hall') }),
  // ответить конкретной ошибкой
  Fail: () => {
    throw new MqttRpc.RpcError(1001, 'not today', { retryAfter: 60 });
  },
});

// под другим драйвером: /rpc/v1/my-integration/Lights/Ping
MqttRpc.defineService('my-integration', 'Lights', { Ping: () => 'pong' });
```

Для каждого метода публикуется retained-топик доступности со значением `1`;
при выгрузке файла (перезагрузка, удаление; при остановке движка — только с
флагом `-cleanup`) топики очищаются. Одну пару `driver/service` обслуживает
только один файл: повторное объявление из другого файла — ошибка загрузки,
из того же файла — добавление или замена методов (с предупреждением в логе).

Ответы: результат обработчика (`undefined` или функция → `null`); исключение
`RpcError` → его `code`/`message`/`data`; любое другое исключение → −32603
(и запись в логе правил); неизвестный метод → −32601; невалидный JSON →
−32700; запрос без `id` → −32600; `params` не объект и не массив → −32602;
несериализуемый результат → −32603.

### `MqttRpc.method(schema, handler) → MethodSpec`

Метод с проверкой параметров по JSON-схеме перед вызовом обработчика: запрос,
не прошедший проверку, получает −32602 с первой проблемой в `message` и полным
списком `[{ path, message }]` в `data`. TypeScript выводит тип `params`
обработчика из схемы (`MqttRpc.FromSchema<S>`) — и в `.ts`, и в `.js` файлах.
Поддерживаемое подмножество схемы: `type` (в том числе массив типов), `enum`,
`const`, `properties`/`required`/`additionalProperties`,
`items`/`minItems`/`maxItems`, `minLength`/`maxLength`/`pattern`,
`minimum`/`maximum`/`exclusiveMinimum`/`exclusiveMaximum`/`multipleOf`,
`anyOf`/`oneOf`/`allOf`/`not`; прочие ключевые слова игнорируются.
Принимается и объектная форма `{ params: schema, handler }`, но без вывода
типов.

```javascript
MqttRpc.defineService('Heating', {
  SetTarget: MqttRpc.method(
    {
      type: 'object',
      properties: {
        room: { type: 'string', minLength: 1 },
        t: { type: 'number', minimum: 5, maximum: 35 },
        mode: { enum: ['eco', 'comfort'] },
      },
      required: ['room', 't'],
      additionalProperties: false,
    },
    (params) => {
      // params.room — string, params.t — number, params.mode — 'eco' | 'comfort' | undefined
      dev['heating/' + params.room + '_target'] = params.t;
      return { room: params.room, t: params.t, mode: params.mode || 'comfort' };
    }
  ),
});
// запрос {"id":1,"params":{"room":"","t":40}} получит:
// {"id":1,"error":{"code":-32602,"message":"invalid params: /room must be at least 1 characters",
//   "data":[{"path":"/room","message":"must be at least 1 characters"},{"path":"/t","message":"must be <= 35"}]}}
```

### `MqttRpc.validate(schema, value) → ValidationProblem[]`

Та же проверка отдельно от RPC: возвращает `[]`, если значение соответствует
схеме, иначе — список `{ path, message }` (`path` — JSON-указатель, `/` для
корня).

```javascript
const problems = MqttRpc.validate(
  { type: 'object', properties: { t: { type: 'number' } }, required: ['t'] },
  { t: 'hot' }
);
// [{ path: '/t', message: 'must be number, got string' }]
if (problems.length) log.error('{}', problems.map((p) => p.path + ' ' + p.message).join('; '));
```

## 3. Сервисы контроллера

У каждого объекта сервиса есть:

| Свойство или метод | Описание |
|---|---|
| `driver` | идентификатор драйвера в топиках |
| `rpc.<service>.<Method>(params[, options])` | методы RPC в том виде, в каком они описаны в документации сервиса (см. таблицу в README) |
| `isAvailable([timeout]) → boolean` | доступен ли сервис (по retained-топику одного из его методов) |
| `waitUntilAvailable([timeout]) → void` | дождаться появления сервиса |

```javascript
if (!(await MqttRpc.serial.isAvailable())) {
  log.warning('wb-mqtt-serial не запущен');
  return;
}
await MqttRpc.db.waitUntilAvailable(30000);
// слой rpc: метод в точности такой, как в документации сервиса
const cfg = await MqttRpc.serial.rpc.config.Load({ lang: 'ru' });
```

Общие типы аргументов:

* **Порт** (`PortSpec`): путь `'/dev/ttyRS485-1'` (настройки линии по
  умолчанию — 9600 N 8 2), объект `{ path, baudRate, parity, dataBits, stopBits }`
  (принимаются и поля в snake_case из ответов сервисов), для Modbus TCP —
  `'10.0.0.5:502'`, `{ ip, port }` или `{ address, port }`.
* **Момент времени** (`Db.Time`): `Date` или число миллисекунд с начала эпохи
  (`Date.now()`).
* **Канал истории** (`Db.ChannelSpec`): `'device/control'` или
  `['device', 'control']`.

```javascript
const portA = '/dev/ttyRS485-1';                                   // 9600 N 8 2
const portB = { path: '/dev/ttyRS485-2', baudRate: 115200, parity: 'N', dataBits: 8, stopBits: 1 };
const gateway = '10.0.0.5:502';                                     // Modbus TCP
const since = new Date('2026-08-25T00:00:00');
const untilMs = Date.now();
const channel = ['wb-adc', 'Vin'];                                   // то же, что 'wb-adc/Vin'
```

### 3.1 `MqttRpc.serial` — wb-mqtt-serial

#### `serial.device(target) → Serial.Device`

`target` — либо MQTT-идентификатор устройства из конфига (`'wb-map12e_1'`;
порт, протокол и адрес будут взяты из конфига драйвера), либо объект
`{ port, slaveId[, deviceType][, rtuOverTcp] }` (`deviceType` — тип из
шаблона, нужен для `settings`/`read`/`write`; `rtuOverTcp` — кадры RTU через
TCP-сокет вместо Modbus TCP).

```javascript
const meter = MqttRpc.serial.device('wb-map12e_1');                    // из конфига
const relay = MqttRpc.serial.device({ port: '/dev/ttyRS485-2', slaveId: 12, deviceType: 'WB-MR6C' });
const remote = MqttRpc.serial.device({ port: '10.0.0.5:502', slaveId: 1 });                 // Modbus TCP
const viaGateway = MqttRpc.serial.device({ port: '10.0.0.6:502', slaveId: 2, rtuOverTcp: true }); // RTU через TCP
```

Методы объекта устройства (`options` — `HelperOptions & SerialTimeouts`); в
примерах ниже `meter` и `relay` — объекты из примера выше. Исключения Modbus
от устройства приходят как `MqttRpc.ModbusError`, ошибки порта или драйвера —
как `RpcError` (например, −32600 «Request timeout» при превышении
`totalTimeout`).

##### `device.readHolding(address[, count][, options]) → number[]`

Функция 3; до 125 регистров; значения — беззнаковые 16-битные числа.

```javascript
const [hi, lo] = await meter.readHolding(0x1400, 2);
const total = (hi << 16) | lo;
```

##### `device.readInput(address[, count][, options]) → number[]`

Функция 4.

```javascript
const [raw] = await meter.readInput(0x10);
```

##### `device.readCoils(address[, count][, options]) → boolean[]`

Функция 1; до 2000 бит.

```javascript
const relays = await relay.readCoils(0, 6);  // [true, false, ...]
```

##### `device.readDiscrete(address[, count][, options]) → boolean[]`

Функция 2.

```javascript
const inputs = await relay.readDiscrete(0, 8);
```

##### `device.writeHolding(address, value | values[][, options]) → void`

Одно значение — функция 6, массив — функция 16 (до 123 регистров).

```javascript
await relay.writeHolding(0x60, 1);             // функция 6
await meter.writeHolding(0x1000, [0, 0, 500]); // функция 16
```

##### `device.writeCoil(address, bool | bools[][, options]) → void`

Одно значение — функция 5, массив — функция 15 (до 1968 бит).

```javascript
await relay.writeCoil(0, true);                       // функция 5
await relay.writeCoil(0, [true, false, true, true]);  // функция 15
```

##### `device.modbus(fn, address[, { count, data, writeAddress, writeCount, …options }]) → string`

Произвольная функция Modbus (1–6, 15, 16, 23); `data` — данные для записи в
hex; результат — данные ответа в hex (пустая строка для функций записи).

```javascript
const hex = await meter.modbus(3, 0x80, { count: 2 });              // '12340001'
await meter.modbus(23, 0x10, { count: 2, writeAddress: 0x20, writeCount: 1, data: '1234' });
```

##### `device.raw(hex, responseSize[, options]) → string`

Произвольные байты в порт (только для устройства с явно заданным портом):
на вход — hex-строка, результат — hex-строка ответа.

```javascript
const answer = await relay.raw('0c03008000018499', 8);
```

##### `device.settings([{ force, …options }]) → { parameters, fw?, model? }`

Параметры устройства из шаблона (для настроенных устройств драйвер их
кэширует; `force` — перечитать с устройства).

```javascript
const { parameters, fw, model } = await meter.settings({ force: true });
log('{} {}: {}', model, fw, JSON.stringify(parameters));
```

##### `device.readChannel(name[, options]) → any` / `device.readChannels(...names | names[][, options]) → { name: value }`

Текущие значения каналов по именам из шаблона (драйвер читает их с
устройства).

```javascript
const urms = await meter.readChannel('Urms L1');
const { 'Urms L1': u, 'Irms L1': i } = await meter.readChannels('Urms L1', 'Irms L1');
const some = await meter.readChannels(['Urms L1', 'Irms L1'], { totalTimeout: 5000 });
```

##### `device.readParameter(id[, options]) → any` / `device.readParameters(...ids | ids[][, options]) → { id: value }`

Значения параметров по их идентификаторам.

```javascript
const baud = await meter.readParameter('baud_rate');
const modes = await meter.readParameters('in1_mode', 'in2_mode');
```

##### `device.writeChannel(name, value[, options]) → void` / `device.writeChannels({ name: value }[, options]) → void`

```javascript
await relay.writeChannel('K1', 1);
await relay.writeChannels({ K1: 0, K2: 1 });
```

##### `device.setParameter(id, value[, options]) → void` / `device.setParameters({ id: value }[, options]) → void`

```javascript
await relay.setParameter('in1_mode', 2);
await meter.setParameters({ baud_rate: 96, in1_mode: 2 });
```

##### `device.read({ channels?, parameters? }[, options]) → { channels, parameters, readonly }` / `device.write({ channels?, parameters? }[, options]) → void`

Каналы и параметры одним запросом.

```javascript
const both = await meter.read({ channels: ['Urms L1'], parameters: ['baud_rate'] });
log('{} {} readonly: {}', both.channels['Urms L1'], both.parameters.baud_rate, both.readonly.join(','));
await relay.write({ channels: { K1: 1 }, parameters: { in1_mode: 1 } });
```

##### `device.probe([{ protocol, …options }]) → ScannedDevice | null`

Кто отвечает по адресу устройства (только для явно заданного порта); `null` —
никто.

```javascript
const who = await relay.probe();
if (who) log('найдено {} sn={} fw={}', who.device_signature, who.sn, who.fw && who.fw.version);
```

##### `device.setPolling(enabled)`, `device.pausePolling()`, `device.resumePolling()` → void

Управление опросом устройства драйвером (пауза снимается автоматически через
10 минут).

```javascript
await meter.pausePolling();
try {
  await meter.writeHolding(0x80, 1);
} finally {
  await meter.resumePolling();
}
```

##### `device.withPollingPaused(fn[, options]) → результат fn`

То же одной конструкцией: опрос приостанавливается на время `fn(device)` и
возобновляется по завершении, в том числе при исключении; результат `fn`
возвращается.

```javascript
const version = await meter.withPollingPaused(async (d) => {
  await d.writeHolding(0x80, 1);
  return (await d.readHolding(0x81))[0];
});
```

##### `device.firmwareInfo()`, `device.updateFirmware([options])`, `device.waitForFirmwareUpdate()`, `device.restoreFirmware()`, `device.clearFirmwareError()`

Те же операции, что и у `serial.*` (см. «Прошивки»), но для устройства,
заданного идентификатором, порт и адрес берутся из конфига.

```javascript
const info = await meter.firmwareInfo();
if (info.fw_has_update) {
  await meter.updateFirmware({ onProgress: (e) => log('{}%', e.progress) });
}
```

##### `device.resolve() → { port, slaveId, type }`

Порт, адрес и тип устройства по конфигу драйвера (конфиг читается при каждом
вызове).

```javascript
const where = await meter.resolve();
log('{} на {}:{}', where.type, where.port.path || where.port.ip, where.slaveId);
```

Поля `device.id`, `device.port`, `device.slaveId`, `device.deviceType` —
такие, какими они были заданы при создании объекта.

#### Конфигурация и порты

##### `serial.devices([{ lang, …options }]) → ConfiguredDeviceInfo[]`

Все устройства из конфига: `{ id, type, name, slaveId (число для Modbus),
enabled, port, config }`; `id` — MQTT-идентификатор (явно заданный `id`, иначе
`<mqtt-id шаблона>_<slave_id>`), тот же, что используется в `dev[...]`.

```javascript
for (const d of await MqttRpc.serial.devices()) {
  log('{} ({} @ {}:{}) {}', d.id, d.type, d.port.path || d.port.ip, d.slaveId, d.enabled ? '' : 'выключено');
}
// из записи списка — сразу объект устройства
const [first] = await MqttRpc.serial.devices();
if (first.slaveId !== undefined) {
  const regs = await MqttRpc.serial.device({ port: first.port, slaveId: first.slaveId }).readHolding(0);
}
```

##### `serial.ports([options]) → ConfiguredPort[]`

Порты из конфига драйвера.

```javascript
const ports = await MqttRpc.serial.ports(); // [{ path, baud_rate, ... }, { address, port }]
```

##### `serial.config([{ lang, …options }]) → { config, schema, types }`

Конфиг драйвера, его JSON-схема и типы устройств.

```javascript
const { config } = await MqttRpc.serial.config();
log('портов: {}', config.ports.length);
```

##### `serial.deviceTypes([{ lang, …options }]) → (DeviceType & { group })[]`

Все типы устройств одним плоским списком.

```javascript
const types = await MqttRpc.serial.deviceTypes({ lang: 'ru' });
const wb = types.filter((t) => t.group === 'Wiren Board' && !t.deprecated).map((t) => t.type);
```

##### `serial.deviceSchema(type[, options]) → any`

JSON-схема настроек для типа устройства.

```javascript
const schema = await MqttRpc.serial.deviceSchema('WB-MR6C');
```

##### `serial.uploadTemplate(filename, content[, { force, lang, …options }]) → { types }` / `serial.deleteTemplate(type[, { force, lang, …options }]) → { types }`

Пользовательские шаблоны (`content` — строка или объект; `force` — выполнить
операцию, даже если шаблоном пользуются настроенные устройства).

```javascript
const template = readConfig('/etc/wb-rules/my-device.json');
await MqttRpc.serial.uploadTemplate('my-device.json', template, { force: true });
await MqttRpc.serial.deleteTemplate('MY-DEVICE');
```

#### Сканирование и настройка

##### `serial.scan(port[, { command, mode, totalTimeout, …options }]) → ScannedDevice[]`

Сканирование порта по Fast Modbus; если сканирование прервано, бросается
`Error` с полем `devices` (устройства, найденные до ошибки).

```javascript
try {
  const found = await MqttRpc.serial.scan('/dev/ttyRS485-1');
  found.forEach((d) => log('{} sn={} addr={}', d.device_signature, d.sn, d.cfg && d.cfg.slave_id));
} catch (e) {
  log.error('{}; до ошибки найдено {}', e.message, (e.devices || []).length);
}
```

##### `serial.probe(port, slaveId[, { protocol, …options }]) → ScannedDevice | null`

Кто отвечает по адресу на порту; `null` — никто.

```javascript
const who = await MqttRpc.serial.probe({ path: '/dev/ttyRS485-1', baudRate: 115200 }, 7);
```

##### `serial.setup(port, items[, { totalTimeout, …options }]) → {}`

Смена адресов и настроек линии: `items` — `[{ slaveId | sn, baudRate?,
parity?, dataBits?, stopBits?, set: { slaveId?, baudRate?, parity?, stopBits?
} }]` (текущие настройки линии устройства; по умолчанию 9600 N 8 1).

```javascript
await MqttRpc.serial.setup('/dev/ttyRS485-1', [
  { sn: 4265607, set: { slaveId: 12 } },                              // по серийному номеру
  { slaveId: 1, baudRate: 9600, set: { baudRate: 115200, parity: 'E' } },
]);
```

#### Прошивки

`serial.*` работает только с последовательными портами; у `deviceManager.*`
те же методы, но порт может быть и TCP.

##### `firmwareInfo(port, slaveId[, { protocol, rtuOverTcp, …options }]) → FirmwareInfo`

```javascript
const info = await MqttRpc.serial.firmwareInfo('/dev/ttyRS485-1', 12);
log('{}: {} -> {} ({})', info.model, info.fw, info.available_fw, info.fw_has_update ? 'есть обновление' : 'актуально');
```

##### `updateFirmware(port, slaveId[, { type, wait, timeout, startTimeout, onProgress, …options }]) → void` (`→ "Ok"` при `wait: false`)

Обновить `firmware` (по умолчанию), `bootloader` или `component`. Ждёт
окончания обновления по state-топику: `onProgress(entry)` вызывается при
каждом изменении `progress`; ошибка обновления — исключение с полем `state`;
`startTimeout` (10000) — сколько ждать появления устройства в state-топике;
`timeout` (600000) — предел на всё обновление.

```javascript
try {
  await MqttRpc.serial.updateFirmware('/dev/ttyRS485-1', 12, {
    onProgress: (e) => log('{}: {}%', e.type, e.progress),
  });
  log('обновлено');
} catch (e) {
  log.error('{} (state: {})', e.message, JSON.stringify(e.state));
}
// не ждать окончания: только запустить
await MqttRpc.serial.updateFirmware('/dev/ttyRS485-1', 13, { type: 'bootloader', wait: false });
```

##### `waitForFirmwareUpdate(port, slaveId[, options]) → void`

Дождаться окончания уже идущего обновления.

```javascript
await MqttRpc.serial.waitForFirmwareUpdate('/dev/ttyRS485-1', 13, { timeout: 300000 });
```

##### `restoreFirmware(port, slaveId[, options]) → "Ok"` / `clearFirmwareError(port, slaveId[, { type, …options }]) → "Ok"`

```javascript
await MqttRpc.serial.restoreFirmware('/dev/ttyRS485-1', 12);      // устройство осталось в загрузчике
await MqttRpc.serial.clearFirmwareError('/dev/ttyRS485-1', 12);   // убрать запись об ошибке
```

##### `firmwareUpdateState([{ timeout }]) → { devices }`

Текущее состояние обновлений: `devices` — `[{ port: { path }, slave_id,
progress, type, error }]`.

```javascript
const state = await MqttRpc.serial.firmwareUpdateState();
state.devices.forEach((d) => log('{}:{} {}% {}', d.port.path, d.slave_id, d.progress, d.error ? d.error.message : ''));
```

### 3.2 `MqttRpc.db` — wb-mqtt-db (история)

##### `db.query(channel | channels[][, options]) → { values, hasMore }`

Записи одного или нескольких каналов (по одному запросу на канал; результаты
сливаются по времени). `options`: `since`/`until` (момент времени), `last`
(мс; несовместим с `since`), `limit` (на канал), `minInterval` (мс),
`maxRecords` (усреднение на стороне базы), `requestTimeout` (с), `afterUid`
(продолжение выборки). Запись: `{ channel, device, control, time: Date,
value (number | string), min, max, retain, uid }`. Границы окна передаются
базе целыми секундами (`since` округляется вниз, `until` — вверх).

```javascript
const { values, hasMore } = await MqttRpc.db.query('wb-adc/Vin', { last: 3600000, limit: 100 });
for (const r of values) log('{} {}', r.time.toISOString(), r.value);

// несколько каналов за окно, усреднение до 60 точек на канал
const day = await MqttRpc.db.query(['wb-adc/Vin', ['wb-adc', 'A1']], {
  since: new Date('2026-08-24T00:00:00'),
  until: new Date('2026-08-25T00:00:00'),
  maxRecords: 60,
});
// продолжение выборки, если записей больше limit
if (hasMore) {
  const more = await MqttRpc.db.query('wb-adc/Vin', { last: 3600000, limit: 100, afterUid: values[values.length - 1].uid });
}
```

##### `db.lastValue(channel[, options]) → HistoryRecord | undefined`

Последняя запись канала (`undefined`, если записей нет).

```javascript
const last = await MqttRpc.db.lastValue('wb-adc/Vin');
if (last) log('последнее значение {} в {}', last.value, last.time.toLocaleTimeString());
```

##### `db.average(channel, { last | since, until[, buckets] }) → number | undefined`

Среднее за период: среднее по не более чем `buckets` (по умолчанию 100)
интервалам, усреднённым базой (база выравнивает интервалы по началу эпохи,
поэтому крайние интервалы окна могут иметь чуть иной вес); `undefined`, если
числовых записей нет.

```javascript
const avgDay = await MqttRpc.db.average('wb-adc/Vin', { last: 86400000 });
const avgNight = await MqttRpc.db.average('wb-adc/Vin', {
  since: new Date('2026-08-24T23:00:00'),
  until: new Date('2026-08-25T06:00:00'),
  buckets: 20,
});
```

##### `db.channels([options]) → ChannelInfo[]`

Все каналы базы: `{ channel, device, control, items, lastTime: Date }`.

```javascript
const stale = (await MqttRpc.db.channels()).filter((c) => Date.now() - c.lastTime.getTime() > 86400000);
log('давно не обновлялись: {}', stale.map((c) => c.channel).join(', '));
```

### 3.3 `MqttRpc.rules` — редактор правил wb-rules

Ошибки сервиса приходят как `RpcError` с кодами 1000–1009 и `data`
`"EditorError"`.

##### `rules.list([options]) → FileEntry[]`

Файлы правил: `{ virtualPath, enabled, error?, rules, devices, timers }`.

```javascript
const broken = (await MqttRpc.rules.list()).filter((f) => f.error);
broken.forEach((f) => log.error('{}: {}', f.virtualPath, f.error.message));
```

##### `rules.load(path[, options]) → { content, enabled, error? }`

```javascript
const { content } = await MqttRpc.rules.load('lighting.js');
```

##### `rules.save(path, content[, options]) → { path, error?, traceback? }`

Если новый текст файла не загрузился, ошибка приходит внутри результата, а не
исключением.

```javascript
const { content } = await MqttRpc.rules.load('lighting.js');
const saved = await MqttRpc.rules.save('lighting.js', content.replace('22', '23'));
if (saved.error) log.error('не загрузилось: {}', JSON.stringify(saved.error));
```

##### `rules.remove(path)`, `rules.rename(path, newPath)`, `rules.enable(path)`, `rules.disable(path)` → void

```javascript
await MqttRpc.rules.disable('legacy.js');
await MqttRpc.rules.rename('legacy.js', 'attic/legacy.js');
await MqttRpc.rules.remove('scratch.js');
```

##### `rules.check(path[, { interval, timeout, …options }]) → { status, diags }`

Результат проверки типов: пока статус `pending`, запрос повторяется каждые
`interval` (200) мс; по истечении `timeout` (30000) — `TimeoutError`.

```javascript
const verdict = await MqttRpc.rules.check('lighting.ts');
if (verdict.status === 'ready') {
  verdict.diags.forEach((d) => log('{}:{} {}', d.line, d.column, d.message));
}
```

##### `rules.types([options]) → string`

Текст файла `wb-rules.d.ts`.

```javascript
const dts = await MqttRpc.rules.types();
```

### 3.4 `MqttRpc.confed` — редактор конфигов wb-mqtt-confed

##### `confed.list([options]) → ConfigEntry[]`

Список конфигов: `{ title, description, configPath, schemaPath, editor }`.

```javascript
const entries = await MqttRpc.confed.list();
log('{}', entries.map((c) => c.configPath).join('\n'));
```

##### `confed.load(path[, options]) → { configPath, content, schema, editor }`

```javascript
const { content } = await MqttRpc.confed.load('/etc/wb-mqtt-serial.conf');
log('debug={}', content.debug);
```

##### `confed.save(path, content[, options]) → void`

Записать конфиг; зависимые сервисы будут перезапущены.

```javascript
const { content } = await MqttRpc.confed.load('/etc/wb-mqtt-serial.conf');
content.debug = false;
await MqttRpc.confed.save('/etc/wb-mqtt-serial.conf', content);
```

##### `confed.update(path, fn[, options]) → content`

Загрузить конфиг, изменить и сохранить: `fn(content, loaded)` либо меняет
объект на месте, либо возвращает новый (может быть `async`); возвращает
сохранённое содержимое.

```javascript
await MqttRpc.confed.update('/etc/wb-mqtt-serial.conf', (cfg) => {
  cfg.debug = false;
});
const replaced = await MqttRpc.confed.update('/etc/wb-rules.conf', () => ({ debug: true }));
```

### 3.5 `MqttRpc.logs` — журнал wb-mqtt-logs

##### `logs.read([options]) → LogRecord[]`

Записи журнала: `{ time: Date, level (6 — info), msg, service?, cursor? }`.
`options`: `service`, `boot` (хэш из `boots()`), `since` (момент времени),
`levels` (0–7), `pattern`, `regex`, `caseSensitive`, `cursor`, `direction`
(`backward`/`forward`), `limit` (не больше 100). Без `since` — новые первыми,
с конца журнала; с `since` (или с `direction: 'forward'`) — старые первыми,
начиная с указанного момента.

```javascript
// ошибки wb-mqtt-serial за последний час, старые первыми
const errors = await MqttRpc.logs.read({
  service: 'wb-mqtt-serial.service',
  levels: [0, 1, 2, 3],
  since: Date.now() - 3600000,
  limit: 100,
});
errors.forEach((e) => log('{} {}', e.time.toISOString(), e.msg));

// листание назад от последней записи предыдущей страницы
const page1 = await MqttRpc.logs.read({ service: 'wb-rules.service', limit: 100 });
const page2 = await MqttRpc.logs.read({ service: 'wb-rules.service', limit: 100, cursor: page1[page1.length - 1].cursor });
```

##### `logs.tail(service[, count][, options]) → LogRecord[]`

Последние `count` (по умолчанию 50) записей сервиса, новые первыми.

```javascript
const tail = await MqttRpc.logs.tail('wb-rules.service', 20);
```

##### `logs.services([options]) → string[]` / `logs.boots([options]) → { hash, start: Date, end?: Date }[]`

Список сервисов в журнале и список загрузок системы.

```javascript
const services = await MqttRpc.logs.services();
const [current] = await MqttRpc.logs.boots();
log('загрузка {} с {}', current.hash, current.start.toISOString());
```

##### `logs.cancel([options]) → void`

Прервать текущее чтение журнала.

```javascript
await MqttRpc.logs.cancel();
```

### 3.6 `MqttRpc.diag` — wb-diag-collect

##### `diag.collect([{ timeout, …options }]) → { basename, fullname }`

Собрать архив диагностики и дождаться его готовности (по умолчанию не дольше
300000 мс); при неудаче — `Error`.

```javascript
const archive = await MqttRpc.diag.collect();
log('архив: {}', archive.fullname);
```

##### `diag.isAlive([options]) → boolean`

```javascript
if (!(await MqttRpc.diag.isAlive())) log.warning('wb-diag-collect не отвечает');
```

### 3.7 `MqttRpc.deviceManager` — сканер шины wb-device-manager

Занятый сервис отвечает `RpcError` с кодом −33100.

##### `deviceManager.scan([options]) → ScannedDevice[]`

Запустить сканирование и дождаться результата. `options`: `port` (путь или
`host:port`; по умолчанию — все порты), `protocol`/`rtuOverTcp`, `type`
(`extended`/`standard`/`bootloader`), `preserveOldResults`,
`outOfOrderSlaveIds`, `timeout` (600000), `startTimeout` (10000),
`onProgress(state)`. Ошибка сканирования — `Error` с полем `state`.

```javascript
const found = await MqttRpc.deviceManager.scan({
  port: '/dev/ttyRS485-1',
  type: 'standard',
  onProgress: (s) => log('{}%', s.progress),
});
found.forEach((d) => log('{} sn={} @{}:{}', d.device_signature, d.sn, d.port.path, d.cfg.slave_id));
```

##### `deviceManager.stopScan([options]) → void` / `deviceManager.state([{ timeout }]) → ScanState | null`

```javascript
const state = await MqttRpc.deviceManager.state();
if (state && state.scanning) await MqttRpc.deviceManager.stopScan();
```

##### `deviceManager.firmwareInfo`, `updateFirmware`, `waitForFirmwareUpdate`, `restoreFirmware`, `clearFirmwareError`, `firmwareUpdateState`

Как у `serial` (см. «Прошивки»), но порт может быть и TCP (`'host:port'`).

```javascript
const info = await MqttRpc.deviceManager.firmwareInfo('10.0.0.5:502', 1);
await MqttRpc.deviceManager.updateFirmware('10.0.0.5:502', 1, { onProgress: (e) => log('{}%', e.progress) });
```

### 3.8 `MqttRpc.dali` — wb-mqtt-dali

##### `dali.buses([options]) → (Bus & { gateway: { id, name } })[]`

Все шины всех шлюзов одним плоским списком.

```javascript
const buses = await MqttRpc.dali.buses();
buses.forEach((b) => log('{} ({}) устройств: {}', b.name, b.gateway.name, b.devices.length));
```

##### `dali.send(busId, command | commands[][, options]) → CommandResult[]`

Выполнить команду или несколько команд (например, `'DAPC(A0, 0xFE)'`)
атомарно; результат — по одному объекту `{ status, response?, error? }` на
команду.

```javascript
const [bus] = await MqttRpc.dali.buses();
const [dim] = await MqttRpc.dali.send(bus.id, 'DAPC(A0, 0x80)');
if (dim.status === 'error') log.error('{}', dim.error);
const results = await MqttRpc.dali.send(bus.id, ['DAPC(A0, 0xFE)', 'DAPC(A1, 0x00)']);
```

##### `dali.commands([options]) → CommandInfo[]`

Список поддерживаемых команд.

```javascript
const known = await MqttRpc.dali.commands();
log('{}', known.map((c) => c.name).join(', '));
```

## 4. Прочее

* `trackMqtt(topic, callback, { cache: false })` — опция движка, которой
  модуль пользуется для потоков запросов и ответов: она отключает
  воспроизведение последнего значения топика подписчикам, подключившимся
  позже (режим определяет первый подписчик на данный шаблон топика).
* Все подписки, таймеры и объявленные методы принадлежат файлу правил и
  освобождаются при его перезагрузке.

```javascript
// поток одноразовых сообщений: без кэша последнего значения
trackMqtt('/my-service/events/+', (msg) => log('{} = {}', msg.topic, msg.value), { cache: false });
```
