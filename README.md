# wb-rules

Rule engine for Wiren Board

## Установка на Wiren Board

Пакет wb-rules в репозитории, для установки и обновления надо выполнить
```
apt-get update
apt-get install wb-rules
```

Правила находятся в каталоге ```/etc/wb-rules/```

## Сборка из исходников


Сборка go1.4.1 с поддержкой CGo (например, на Ubuntu 14.04):

```
sudo apt-get install -y build-essential fakeroot dpkg-dev \
  debhelper pkg-config binutils-arm-linux-gnueabi git mercurial gcc-arm-linux-gnueabi
mkdir progs && cd progs
git clone https://go.googlesource.com/go
cd go
git checkout go1.4.1
cd src
GOARM=5 GOARCH=arm GOOS=linux CC_FOR_TARGET=arm-linux-gnueabi-gcc CGO_ENABLED=1 ./make.bash
```

Сборка пакета для Wiren Board:
```
cd
git clone https://github.com/contactless/wb-rules.git
cd wb-rules/
export GOPATH=~/go
mkdir -p $GOPATH
export PATH=$HOME/progs/go/bin:$GOPATH/bin:$PATH
make prepare
dpkg-buildpackage -b -aarmel -us -uc
```

## Правила


Правила пишутся на языке Ecmascript 5 и загружаются из папки `/etc/wb-rules`.

Пример файла с правилами (`sample1.js`):
```js
defineVirtualDevice("relayClicker", {
  title: "Relay Clicker",
  cells: {
    enabled: {
      type: "switch",
      value: false
    }
  }
});

defineRule("startClicking", {
  asSoonAs: function () {
    return dev.relayClicker.enabled && (dev.uchm121rx["Input 0"] == "0");
  },
  then: function () {
    startTicker("clickTimer", 1000);
  }
});

defineRule("stopClicking", {
  asSoonAs: function () {
    return !dev.relayClicker.enabled || dev.uchm121rx["Input 0"] != "0";
  },
  then: function () {
    timers.clickTimer.stop();
  }
});

defineRule("doClick", {
  when: function () {
    return timers.clickTimer.firing;
  },
  then: function () {
    dev.uchm121rx["Relay 0"] = !dev.uchm121rx["Relay 0"];
  }
});
```

### Предопределённые функции и переменные:

`defineVirtualDevice(name, { title: <название>, cells: { описание параметров... } })`
задаёт виртуальное устройство, которое может быть использовано для включения/выключения тех
или иных управляющих алгоритмов и установки их параметров.

Описания параметров - Ecmascript-объект, ключами которого являются имена параметров,
а значениями - описания параметров. 
Описание параметра - объект с полями 
* `type` - тип, публикуемый в MQTT-топике `/devices/.../controls/.../meta/type` для данного параметра.
* `value` - значение параметра по умолчанию (топик `/devices/.../controls/...`).
* `max` для параметра типа `range` может задавать его максимально допустимое значение.

`defineRule(name, { asSoonAs|when: function() { ... }, then: function () { ... } })` 
задаёт правило. Правила просматриваются при получении значений параметров по MQTT и срабатывании
таймеров (см. `startTimer()` `startTicker()` ниже). 

Правила, задаваемые при помощи `asSoonAs`, называются edge-triggered и срабатывают в случае, когда значение, возвращаемое
функцией, заданной в `asSoonAs`, становится истинным при том, что при предыдущем просмотре
данного правила оно было ложным.

Правила, задаваемые при помощи `when`, называются level-triggered,
и срабатывают при каждом просмотре, при котором функция, заданная в `when`, возвращает
истинное значение. При срабатывании правила выполняется функция, заданная
в свойстве `when`.

`dev` задаёт доступные параметры и устройства. `dev["abc"]["def"]` (или, что то же самое,
`dev.abc.def`) задаёт параметр `def` утройства `abc`, доступный по MQTT-топику
`/devices/.../controls/...`. 
Значение параметра зависит от его типа: `switch`, `wo-switch` -
булевский тип, "text" - строковой, остальные типы параметров считаются числовыми.

`startTimer(name, milliseconds)`
запускает однократный таймер с указанным именем. 

Таймер становится доступным как `timers.<name>`. При срабатывании таймера происходит просмотр правил, при этом `timers.<name>.firing` для этого таймера становится истинным на время этого просмотра. 

`startTicker(name, milliseconds)`
запускает периодический таймер с указанным интервалом, который также становится доступным как `timers.<name>`.

Метод `stop()` таймера (обычного или периодического) приводит к его останову.

Объект `timers` устроен таким образом, что `timers.<name>` для любого произвольного
`<name>` всегда возвращает "таймероподобный" объект, т.е. объект с методом
`stop()` и свойством `firing`. Для неактивных таймеров `firing` всегда содержит
`false`, а метод `stop()` ничего не делает. 

`log(str1, [str2 [, ...]])`
выводит указанные строки в лог, разделяя их пробелом.

`debug(str1, [str2 [, ...]])`
также выводит указанные строки в лог, разделяя их пробелом, но только в случае, когда разрешена отладка. 

`publish(topic, payload, [QoS [, retain]])` 
публикует MQTT-сообщение с указанными topic'ом, содержимым, QoS и значением флага retained.

Для включения отладочного режима задать порт и опцию `-debug`
в `/etc/default/wb-rules`:
```
WB_RULES_OPTIONS="-debug"
```

Сообщения об ошибках записываются в syslog.
