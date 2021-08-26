package wbrules

import (
	"github.com/wirenboard/wbgong/testutils"
	"os"
	"regexp"
	"testing"
)

type TestModulesSuite struct {
	RuleSuiteBase
}

func (s *TestModulesSuite) SetupTest() {
	currentDir, _ := os.Getwd()
	s.ModulesPath = currentDir + "/test-modules/"
	s.SetupSkippingDefs("testrules_modules.js", "testrules_modules_2.js")
}

func (s *TestModulesSuite) TestHelloWorld() {
	s.publish("/devices/test/controls/helloworld/on", "1", "test/helloworld")

	s.VerifyUnordered(
		"tst -> /devices/test/controls/helloworld/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/helloworld: [1] (QoS 1, retained)",
		"[info] Module helloworld init",
		"[info] Required module value: 42",
		"[info] Function test: 15",
	)
}

func (s *TestModulesSuite) TestNotFound() {
	s.publish("/devices/test/controls/error/on", "1", "test/error")

	s.VerifyUnordered(
		"tst -> /devices/test/controls/error/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/error: [1] (QoS 1, retained)",
		"[info] Module not found",
	)

	s.EnsureGotErrors()
}

func (s *TestModulesSuite) TestMultipleRequire() {
	s.publish("/devices/test/controls/multifile/on", "1", "test/multifile")

	s.VerifyUnordered(
		"tst -> /devices/test/controls/multifile/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/multifile: [1] (QoS 1, retained)",
		"[info] Module multi_init init",
		"[info] Module multi_init init",
		"[info] [1] My value of multi_init: 42",
		"[info] [2] My value of multi_init: 42",
	)
}

func (s *TestModulesSuite) TestCrossDependency() {
	s.publish("/devices/test/controls/cross/on", "1", "test/cross")

	s.Verify(
		"tst -> /devices/test/controls/cross/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/cross: [1] (QoS 1, retained)",
		"[info] Module submodule init",
		"[info] Module with_require init",
		"[info] Module loaded",
	)
}

func (s *TestModulesSuite) TestModuleParams() {
	s.publish("/devices/test/controls/params/on", "1", "test/params")

	s.Verify(
		"tst -> /devices/test/controls/params/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/params: [1] (QoS 1, retained)",
		"[info] Module params init",
		regexp.MustCompile("\\[__filename: .*/testrules_modules\\.js, module\\.filename: .*/test/params\\.js\\]"),
	)
}

func (s *TestModulesSuite) TestStaticStorage() {
	s.publish("/devices/test/controls/static/on", "1", "test/static")

	s.VerifyUnordered(
		"tst -> /devices/test/controls/static/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/static: [1] (QoS 1, retained)",
		"[info] Module static init",
		"[info] Value: 1",
		"[info] Module static init",
		"[info] Value: 2",
	)
}

func (s *TestModulesSuite) TestModulesCache() {
	s.publish("/devices/test/controls/cache/on", "1", "test/cache")

	s.VerifyUnordered(
		"tst -> /devices/test/controls/cache/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/cache: [1] (QoS 1, retained)",
		"[info] Module helloworld init",
		"[info] Value: 42",
		"[info] Value: 42",
	)

}

func TestModules(t *testing.T) {
	testutils.RunSuites(t,
		new(TestModulesSuite),
	)
}
