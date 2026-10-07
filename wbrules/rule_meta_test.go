package wbrules

import (
	"fmt"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleMetaSuite struct {
	RuleSuiteBase
}

func (s *RuleMetaSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_meta.js")
}

func (s *RuleMetaSuite) TestMeta() {
	// set error by js code
	s.publish("/devices/testDevice/controls/startControl/on", "1", "testDevice/startControl")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/startControl: [1] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/description: [new description] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/error: [error text] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/max: [255] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/min: [5] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/order: [4] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"new description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"error text\",\"max\":255,\"min\":5,\"order\":4,\"readonly\":false,\"type\":\"range\",\"units\":\"meters\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"new description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"error text\",\"max\":255,\"min\":5,\"order\":4,\"readonly\":false,\"type\":\"range\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"new description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"error text\",\"max\":255,\"min\":5,\"order\":5,\"readonly\":false,\"type\":\"range\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"new description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"error text\",\"max\":255,\"order\":5,\"readonly\":false,\"type\":\"range\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"new description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"error text\",\"order\":5,\"readonly\":false,\"type\":\"range\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"new description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"error text\",\"order\":5,\"readonly\":false,\"type\":\"text\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"error text\",\"order\":5,\"readonly\":false,\"type\":\"text\"}] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/startControl/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [got startControl, changed: testDevice/startControl -> true] (QoS 1)",
	)

	// unset error by js code
	s.publish("/devices/testDevice/controls/startControl/on", "0", "testDevice/startControl")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/startControl: [0] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/description: [old description] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/error: [] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/max: [0] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/min: [0] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/order: [5] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/readonly: [0] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"new description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"\",\"max\":255,\"min\":5,\"order\":4,\"readonly\":true,\"type\":\"range\",\"units\":\"meters\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"new description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"error text\",\"max\":255,\"min\":5,\"order\":4,\"readonly\":true,\"type\":\"range\",\"units\":\"meters\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"old description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"\",\"max\":255,\"min\":5,\"order\":4,\"readonly\":true,\"type\":\"range\",\"units\":\"meters\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"old description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"\",\"order\":4,\"readonly\":true,\"type\":\"text\",\"units\":\"meters\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"old description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"\",\"order\":4,\"readonly\":true,\"type\":\"text\",\"units\":\"meters\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"old description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"\",\"order\":4,\"readonly\":true,\"type\":\"text\",\"units\":\"meters\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"old description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"\",\"order\":5,\"readonly\":true,\"type\":\"text\",\"units\":\"chars\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"old description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"\",\"order\":5,\"readonly\":true,\"type\":\"text\",\"units\":\"meters\"}] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/startControl/on: [0] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [got startControl, changed: testDevice/startControl -> false] (QoS 1)",
	)

	// when error set on somedev/sw -> testDevice/switchControl is changed to true
	s.publish("/devices/somedev/controls/sw/meta/error", "another error", "somedev/sw", "testDevice/switchControl")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/switchControl: [1] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"old description\",\"enum\":{\"str0\":{\"en\":\"Off\"},\"str1\":{\"en\":\"On\"}},\"error\":\"\",\"order\":5,\"readonly\":false,\"type\":\"text\",\"units\":\"chars\"}] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta: [{\"description\":\"old description\",\"enum\":{\"txt0\":{\"en\":\"zero\"},\"txt1\":{\"en\":\"one\"}},\"error\":\"\",\"order\":5,\"readonly\":false,\"type\":\"text\",\"units\":\"chars\"}] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/error: [another error] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [got sw, changed: somedev/sw#error -> another error] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [somedev/sw = false] (QoS 1)",
	)

	// when error unset on somedev/sw -> testDevice/switchControl is changed to false
	s.publish("/devices/somedev/controls/sw/meta/error", "", "somedev/sw", "testDevice/switchControl")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/switchControl: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/error: [] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [got sw, changed: somedev/sw#error -> ] (QoS 1)",
	)

	s.VerifyEmpty()
}

func (s *RuleMetaSuite) TestUndefinedControlMeta() {
	s.publish("/devices/testDevice/controls/checkUndefinedControl/on",
		"1", "testDevice/checkUndefinedControl")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/checkUndefinedControl: [1] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/checkUndefinedControl/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [Meta: null] (QoS 1)",
	)
}

func (s *RuleMetaSuite) TestVirtualDeviceOrder() {
	s.publish("/devices/testDevice/controls/vDevWithOrder/on", "1", "testDevice/vDevWithOrder")
	s.VerifyUnordered(
		"Subscribe -- driver: /devices/vDevWithOrder/controls/test1/on",
		"Subscribe -- driver: /devices/vDevWithOrder/controls/test2/on",
		"driver -> /devices/testDevice/controls/vDevWithOrder: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test1/meta/order: [4] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test1/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test1/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test1/meta: [{\"order\":4,\"readonly\":true,\"type\":\"text\"}] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test1: [hello] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2/meta/order: [3] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2/meta: [{\"order\":3,\"readonly\":true,\"type\":\"text\"}] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2: [world] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/meta/driver: [wbrules] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/meta/name: [] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/meta: [{\"driver\":\"wbrules\"}] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/vDevWithOrder/on: [1] (QoS 1)",
	)
}

func (s *RuleMetaSuite) TestVirtualDeviceControlMetaTitle() {
	s.publish("/devices/testDevice/controls/createVDevWithControlMetaTitle/on", "1", "testDevice/createVDevWithControlMetaTitle")
	s.VerifyUnordered(
		"Subscribe -- driver: /devices/vDevWithControlMetaTitle/controls/test1/on",
		"driver -> /devices/testDevice/controls/createVDevWithControlMetaTitle: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaTitle/controls/test1/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaTitle/controls/test1/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaTitle/controls/test1/meta/type: [value] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaTitle/controls/test1/meta: [{\"order\":1,\"readonly\":true,\"title\":{\"en\":\"ControlMetaTitleOne\"},\"type\":\"value\"}] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaTitle/controls/test1: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaTitle/meta/driver: [wbrules] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaTitle/meta/name: [] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaTitle/meta: [{\"driver\":\"wbrules\"}] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/createVDevWithControlMetaTitle/on: [1] (QoS 1)",
	)
	s.VerifyEmpty()
}

func (s *RuleMetaSuite) TestVirtualDeviceControlMetaUnits() {
	s.publish("/devices/testDevice/controls/createVDevWithControlMetaUnits/on", "1", "testDevice/createVDevWithControlMetaUnits")
	s.VerifyUnordered(
		"Subscribe -- driver: /devices/vDevWithControlMetaUnits/controls/test1/on",
		"driver -> /devices/testDevice/controls/createVDevWithControlMetaUnits: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaUnits/controls/test1/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaUnits/controls/test1/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaUnits/controls/test1/meta/type: [value] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaUnits/controls/test1/meta: [{\"order\":1,\"readonly\":true,\"type\":\"value\",\"units\":\"W\"}] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaUnits/controls/test1: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaUnits/meta/driver: [wbrules] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaUnits/meta/name: [] (QoS 1, retained)",
		"driver -> /devices/vDevWithControlMetaUnits/meta: [{\"driver\":\"wbrules\"}] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/createVDevWithControlMetaUnits/on: [1] (QoS 1)",
	)
	s.VerifyEmpty()
}

func (s *RuleMetaSuite) TestVirtualDeviceControlEnum() {
	tests := []struct {
		name     string
		enum     string
		expected string
	}{
		{
			name:     "strings",
			enum:     `{0: "Ожидание", 1: "Зима[ЦО+ГВС]", 2: "Лето[ГВС]", 3: "Отопление[ЦО]"}`,
			expected: `{"0":{"en":"Ожидание"},"1":{"en":"Зима[ЦО+ГВС]"},"2":{"en":"Лето[ГВС]"},"3":{"en":"Отопление[ЦО]"}}`,
		},
		{
			name:     "localized",
			enum:     `{0: {en: "Idle", ru: "Ожидание"}, 1: {en: "Winter", ru: "Зима[ЦО+ГВС]"}}`,
			expected: `{"0":{"en":"Idle","ru":"Ожидание"},"1":{"en":"Winter","ru":"Зима[ЦО+ГВС]"}}`,
		},
		{
			name:     "mixed",
			enum:     `{0: "Ожидание", 1: {en: "Winter", ru: "Зима[ЦО+ГВС]"}}`,
			expected: `{"0":{"en":"Ожидание"},"1":{"en":"Winter","ru":"Зима[ЦО+ГВС]"}}`,
		},
	}
	for _, controlType := range []string{"value", "text"} {
		value := "0"
		if controlType == "text" {
			value = `"0"`
		}
		for _, tt := range tests {
			deviceID := "enum_" + controlType + "_" + tt.name
			s.Ck("defineVirtualDevice()", s.engine.EvalScript(fmt.Sprintf(`
				defineVirtualDevice(%q, {
					cells: {
						mode: {type: %q, value: %s, readonly: false, enum: %s}
					}
				});
			`, deviceID, controlType, value, tt.enum)))
			deviceTopic := "/devices/" + deviceID
			controlTopic := deviceTopic + "/controls/mode"
			s.VerifyUnordered(
				"Subscribe -- driver: "+controlTopic+"/on",
				"driver -> "+controlTopic+"/meta/order: [1] (QoS 1, retained)",
				"driver -> "+controlTopic+"/meta/readonly: [0] (QoS 1, retained)",
				"driver -> "+controlTopic+"/meta/type: ["+controlType+"] (QoS 1, retained)",
				"driver -> "+controlTopic+`/meta: [{"enum":`+tt.expected+`,"order":1,"readonly":false,"type":"`+controlType+`"}] (QoS 1, retained)`,
				"driver -> "+controlTopic+": [0] (QoS 1, retained)",
				"driver -> "+deviceTopic+"/meta/driver: [wbrules] (QoS 1, retained)",
				"driver -> "+deviceTopic+"/meta/name: [] (QoS 1, retained)",
				"driver -> "+deviceTopic+`/meta: [{"driver":"wbrules"}] (QoS 1, retained)`,
			)
		}
	}
	s.VerifyEmpty()
}

func TestRuleMetaSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleMetaSuite),
	)
}
