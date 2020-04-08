// Common string constants definitions

package wbrules

const (
	// Virtual devices
	VDEV_DESCR_PROP_TITLE    = "title"
	VDEV_DESCR_PROP_CELLS    = "cells"
	VDEV_DESCR_PROP_CONTROLS = "controls"

	VDEV_CONTROL_DESCR_PROP_TYPE         = "type"
	VDEV_CONTROL_DESCR_PROP_FORCEDEFAULT = "forceDefault"
	VDEV_CONTROL_DESCR_PROP_VALUE        = "value"
	VDEV_CONTROL_DESCR_PROP_READONLY     = "readonly"
	VDEV_CONTROL_DESCR_PROP_WRITEABLE    = "writeable"
	// FIXME: deprecated
	VDEV_CONTROL_DESCR_PROP_MAX = "max"

	// default value for 'readonly'
	VDEV_CONTROL_READONLY_DEFAULT = true

	// default 'max' value for 'range' type
	// FIXME: deprecated
	VDEV_CONTROL_RANGE_MAX_DEFAULT = 255.0

	JS_DEVPROXY_FUNC_SETVALUE     = "setValue"
	JS_DEVPROXY_FUNC_SETVALUE_ARG = "v"
	JS_DEVPROXY_FUNC_RAWVALUE     = "rawValue"
	JS_DEVPROXY_FUNC_VALUE        = "value"
	JS_DEVPROXY_FUNC_VALUE_RET    = "v"
	JS_DEVPROXY_FUNC_ISCOMPLETE   = "isComplete"
)
