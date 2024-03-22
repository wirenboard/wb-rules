function cellSpec(devName, cellName) {
  return devName === undefined ? '(no cell)' : '{}/{}'.format(devName, cellName);
}

defineVirtualDevice('testDevice', {
  title: 'Test Device',
  cells: {
    switchControl: {
      type: 'switch',
      value: false,
      order: 4,
    },
    rangeControl: {
      type: 'range',
      max: 100,
      value: 50,
      order: 3,
    },
    textControl: {
      type: 'text',
      value: 'some text',
      readonly: false,
    },
    startControl: {
      type: 'switch',
      value: false,
    },
    checkUndefinedControl: {
      type: 'switch',
      value: false,
    },
    vDevWithOrder: {
      type: 'switch',
      value: false,
    },
    createVDevWithControlMetaTitle: {
      type: 'switch',
      value: false,
    },
    createVDevWithControlMetaUnits: {
      type: 'switch',
      value: false,
    },
  },
});

getDevice('testDevice')
  .getControl('textControl')
  .setEnumTitles({ txt0: { en: 'zero' }, txt1: { en: 'one' } });

defineRule('onChangeStartControl', {
  whenChanged: 'testDevice/startControl',
  then: function (newValue, devName, cellName) {
    log(
      'got startControl, changed: {} -> {}',
      cellSpec(devName, cellName),
      newValue === undefined ? '(none)' : newValue
    );
    if (newValue) {
      dev['testDevice/textControl#error'] = 'error text';
      dev['testDevice/textControl#description'] = 'new description';
      dev['testDevice/textControl#type'] = 'range';
      dev['testDevice/textControl#max'] = '255';
      dev['testDevice/textControl#min'] = '5';
      dev['testDevice/textControl#order'] = '4';
      dev['testDevice/textControl#units'] = 'meters';
      dev['testDevice/textControl#readonly'] = '1';
    } else {
      dev['testDevice/textControl#error'] = '';
      dev['testDevice/textControl#description'] = 'old description';
      dev['testDevice/textControl#type'] = 'text';
      dev['testDevice/textControl#max'] = '0';
      dev['testDevice/textControl#min'] = '0';
      dev['testDevice/textControl#order'] = '5';
      dev['testDevice/textControl#units'] = 'chars';
      dev['testDevice/textControl#readonly'] = '0';
    }
  },
});

defineRule('onChangeSwitchControl', {
  whenChanged: 'testDevice/switchControl#error',
  then: function (newValue, devName, cellName) {
    log(
      'got switchControl, changed: {} -> {}',
      cellSpec(devName, cellName),
      newValue === undefined ? '(none)' : newValue
    );
  },
});

defineRule('onChangeSw', {
  whenChanged: 'somedev/sw#error',
  then: function (newValue, devName, cellName) {
    log(
      'got sw, changed: {} -> {}',
      cellSpec(devName, cellName),
      newValue === undefined ? '(none)' : newValue
    );
    if (newValue !== '') {
      dev['testDevice/switchControl'] = true;
    } else {
      dev['testDevice/switchControl'] = false;
    }
  },
});

defineRule('asSoonAsExtError', {
  asSoonAs: function () {
    return dev['somedev/sw#error'];
  },
  then: function (newValue, devName, cellName) {
    log(devName + '/' + cellName + ' = ' + newValue);
  },
});

defineRule('undefinedControlMeta', {
  whenChanged: 'testDevice/checkUndefinedControl',
  then: function () {
    var m = dev['undefined_device/control#type'];
    log('Meta: ' + m);
  },
});

defineRule('makeVdevWithOrder', {
  whenChanged: 'testDevice/vDevWithOrder',
  then: function () {
    defineVirtualDevice('vDevWithOrder', {
      cells: {
        test1: {
          type: 'text',
          value: 'hello',
          readonly: true,
          order: 4,
        },
        test2: {
          type: 'text',
          value: 'world',
          readonly: true,
          order: 3,
        },
      },
    });
  },
});

defineRule('makeVdevWithControlMetaTitle', {
  whenChanged: 'testDevice/createVDevWithControlMetaTitle',
  then: function () {
    defineVirtualDevice('vDevWithControlMetaTitle', {
      cells: {
        test1: {
          title: 'ControlMetaTitleOne',
          type: 'value',
          value: 1,
        },
      },
    });
  },
});

defineRule('makeVdevWithControlMetaUnits', {
  whenChanged: 'testDevice/createVDevWithControlMetaUnits',
  then: function () {
    defineVirtualDevice('vDevWithControlMetaUnits', {
      cells: {
        test1: {
          units: 'W',
          type: 'value',
          value: 1,
        },
      },
    });
  },
});
