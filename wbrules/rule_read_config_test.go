package wbrules

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleReadConfigSuite struct {
	RuleSuiteBase
	configDir string
	cleanup   func()
}

func (s *RuleReadConfigSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_read_config.js")
	s.configDir, s.cleanup = testutils.SetupTempDir(s.T())
	s.publish("/devices/somedev/controls/readSampleConfig/meta/type", "text", "somedev/readSampleConfig")
	s.Verify("tst -> /devices/somedev/controls/readSampleConfig/meta/type: [text] (QoS 1, retained)")
}

func (s *RuleReadConfigSuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
	s.RuleSuiteBase.TearDownTest()
}

func (s *RuleReadConfigSuite) WriteConfig(filename, text string) (configPath string) {
	configPath = filepath.Join(s.configDir, "conf.json")
	// note that this is JSON config which supports comments, not just json
	ioutil.WriteFile(configPath, []byte(text), 0777)
	return
}

func (s *RuleReadConfigSuite) TryReadingConfig(configPath string) {
	s.publish("/devices/somedev/controls/readSampleConfig", configPath, "somedev/readSampleConfig")
}

func (s *RuleReadConfigSuite) verifyReadConfRuleLog(configPath string, msgs ...interface{}) {
	msgs = append([]interface{}{
		fmt.Sprintf(
			"tst -> /devices/somedev/controls/readSampleConfig: [%s] (QoS 1, retained)",
			configPath),
	}, msgs...)
	s.Verify(msgs...)
}

func (s *RuleReadConfigSuite) TestReadConfig() {
	configPath := s.WriteConfig("conf.json", "{ // whatever! \n\"xyz\": 42 }")
	s.TryReadingConfig(configPath)
	s.verifyReadConfRuleLog(configPath, "[info] config: {\"xyz\":42}")
}

func (s *RuleReadConfigSuite) TestReadConfigErrors() {
	configPath := filepath.Join(s.configDir, "nosuchconf.json")
	s.TryReadingConfig(configPath)
	s.verifyReadConfRuleLog(
		configPath,
		fmt.Sprintf("[error] failed to open config file: %s", configPath),
		"[error] readConfig error!")
	s.EnsureGotErrors()

	configPath = s.WriteConfig("badconf.json", "{")
	s.TryReadingConfig(configPath)
	s.verifyReadConfRuleLog(
		configPath,
		fmt.Sprintf("[error] failed to parse json: %s", configPath),
		"[error] readConfig error!")
	s.EnsureGotErrors()
}

func TestRuleReadConfigSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleReadConfigSuite),
	)
}
