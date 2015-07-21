package wbrules

import (
	"github.com/contactless/wbgo"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ScriptFixture struct {
	*wbgo.Fixture
	wd           string
	ScriptTmpDir string
	rmScriptDir  func()
}

func NewScriptFixture(t *testing.T) (f *ScriptFixture) {
	f = &ScriptFixture{Fixture: wbgo.NewFixture(t)}
	var err error
	f.wd, err = os.Getwd()
	f.Ckf("Getwd", err)
	f.ScriptTmpDir, f.rmScriptDir = wbgo.SetupTempDir(f.T())
	return
}

func (f *ScriptFixture) TearDownScripts() {
	f.rmScriptDir()
}

func (f *ScriptFixture) ScriptPath(script string) string {
	return filepath.Join(f.ScriptTmpDir, script)
}

func (f *ScriptFixture) ensureTargetDirs(targetName string) (targetPath string) {
	targetPath = f.ScriptPath(targetName)
	if strings.Contains(targetName, "/") {
		// the target file is under a subdir
		f.Ckf("MkdirAll", os.MkdirAll(filepath.Dir(targetPath), 0777))
	}
	return
}

func (f *ScriptFixture) readSourceScript(sourceName string) []byte {
	data, err := ioutil.ReadFile(filepath.Join(f.wd, sourceName))
	f.Ckf("ReadFile()", err)
	return data
}

func (f *ScriptFixture) ReadSourceScript(sourceName string) string {
	return string(f.readSourceScript(sourceName))
}

func (f *ScriptFixture) CopyScriptToTempDir(sourceName, targetName string) (targetPath string) {
	data := f.readSourceScript(sourceName)
	targetPath = f.ensureTargetDirs(targetName)
	f.Ckf("WriteFile", ioutil.WriteFile(targetPath, data, 0777))
	return
}

func (f *ScriptFixture) WriteScript(filename, content string) string {
	targetPath := f.ensureTargetDirs(filename)
	f.Ckf("WriteFile()", ioutil.WriteFile(targetPath, []byte(content), 0777))
	return targetPath
}
