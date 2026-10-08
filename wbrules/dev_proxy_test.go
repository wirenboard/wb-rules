package wbrules

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevProxyStrictMode(t *testing.T) {
	f := newESContextFactory()
	ctx := f.newESContext(nil, "")
	defer ctx.DestroyHeap()

	// Isolate the JavaScript runtime from the driver while recording control writes.
	require.NoError(t, ctx.LoadScriptFromString("dev_proxy_setup.js", `
		var __wbVdevPrototype = {};
		function require() { return {}; }
		var lastWrite;
		function _wbDevObject(name) { return {name: name}; }
		function _wbCellObject(device, name) {
			return {
				setValue: function(value) { lastWrite = [device.name, name, value]; },
				setMeta: function(value) { lastWrite = [device.name, name, value]; }
			};
		}
	`))
	require.NoError(t, ctx.LoadScript("../scripts/lib.js"))

	tests := []struct {
		name     string
		script   string
		expected string
	}{
		{"flat true", `dev["buzzer/enabled"] = true;`, `["buzzer","enabled",{"v":true}]`},
		{"flat false", `dev["buzzer/enabled"] = false;`, `["buzzer","enabled",{"v":false}]`},
		{"nested true", `dev["buzzer"]["enabled"] = true;`, `["buzzer","enabled",{"v":true}]`},
		{"nested false", `dev["buzzer"]["enabled"] = false;`, `["buzzer","enabled",{"v":false}]`},
		{"flat metadata", `dev["buzzer/enabled#error"] = "error";`, `["buzzer","enabled",{"k":"error","v":"error"}]`},
		{"flat empty metadata", `dev["buzzer/enabled#error"] = "";`, `["buzzer","enabled",{"k":"error","v":""}]`},
		{"nested metadata", `dev["buzzer"]["enabled#error"] = "error";`, `["buzzer","enabled",{"k":"error","v":"error"}]`},
		{"nested empty metadata", `dev["buzzer"]["enabled#error"] = "";`, `["buzzer","enabled",{"k":"error","v":""}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, ctx.LoadScriptFromString("strict.js", `"use strict"; lastWrite = null; `+tt.script))
			require.Zero(t, ctx.PevalString("JSON.stringify(lastWrite)"))
			assert.Equal(t, tt.expected, ctx.SafeToString(-1))
			ctx.Pop()
		})
	}
}
