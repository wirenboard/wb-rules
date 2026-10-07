package wbrules

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

func checkScriptContent(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, expected, string(content))
}

func checkNoTemporaryScripts(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, ".*"))
	require.NoError(t, err)
	require.Empty(t, paths, "temporary script files were not removed")
}

func TestWriteFileAtomicFailurePreservesOriginal(t *testing.T) {
	const original = `log("original script");`
	dir := t.TempDir()
	path := filepath.Join(dir, "script.js")
	require.NoError(t, os.WriteFile(path, []byte(original), 0640))
	before, err := os.Stat(path)
	require.NoError(t, err)

	// Inject a copy error only after supplying replacement bytes to the real
	// atomic writer, exercising cleanup of a partially written temporary file.
	partial := strings.NewReader(`log("partial`)
	content := io.MultiReader(partial, iotest.ErrReader(syscall.ENOSPC))
	err = wbgong.WriteFileAtomic(path, content, 0644)
	require.ErrorIs(t, err, syscall.ENOSPC)
	require.Zero(t, partial.Len(), "failure must occur after consuming replacement bytes")

	checkScriptContent(t, path, original)
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after), "failed write replaced the original inode")
	require.Equal(t, before.Mode().Perm(), after.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "temporary script files were not removed")
	require.Equal(t, "script.js", entries[0].Name())
}

type RuleEditorSuite struct {
	RuleSuiteBase
}

func (s *RuleEditorSuite) SetupTest() {
	s.SetupSkippingDefs()
}

func (s *RuleEditorSuite) TestSaveScriptAtomically() {
	editor := NewEditor(s.engine)
	args := EditorSaveArgs{Path: "sub/script.js", Content: `log("first version");`}
	var reply EditorSaveResponse
	s.Require().NoError(editor.Save(&args, &reply))
	s.Equal(args.Path, reply.Path)
	s.Nil(reply.Error)
	s.Verify("[info] first version", "[changed] sub/script.js")
	path := s.DataFilePath(args.Path)
	checkScriptContent(s.T(), path, args.Content)
	s.Require().NoError(os.Chmod(path, 0640))
	old, err := os.Open(path)
	s.Require().NoError(err)
	defer old.Close()

	args.Content = `log("second version");`
	s.Require().NoError(editor.Save(&args, &reply))
	s.Equal(args.Path, reply.Path)
	s.Nil(reply.Error)
	checkScriptContent(s.T(), path, args.Content)
	oldContent, err := io.ReadAll(old)
	s.Require().NoError(err)
	s.Equal(`log("first version");`, string(oldContent))
	info, err := os.Stat(path)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0640), info.Mode().Perm())
	s.Verify("[info] second version", "[changed] sub/script.js")

	// DirWatcher's notification after replacement must not reload the script twice.
	s.Require().NoError(s.engine.LiveLoadFile(path))
	s.VerifyEmpty()
	checkNoTemporaryScripts(s.T(), s.DataFilePath("sub"))
	s.EnsureNoErrorsOrWarnings()
}

func (s *RuleEditorSuite) TestSaveDisabledScriptAtomically() {
	const name = "script.js.disabled"
	const original = `log("disabled script");`
	s.WriteDataFile(name, original)
	path := s.DataFilePath(name)
	s.Require().NoError(s.engine.LiveLoadFile(path))
	old, err := os.Open(path)
	s.Require().NoError(err)
	defer old.Close()

	editor := NewEditor(s.engine)
	args := EditorSaveArgs{Path: "script.js", Content: `log("still disabled");`}
	var reply EditorSaveResponse
	s.Require().NoError(editor.Save(&args, &reply))
	s.Equal(args.Path, reply.Path)
	s.Nil(reply.Error)
	checkScriptContent(s.T(), path, args.Content)
	oldContent, err := io.ReadAll(old)
	s.Require().NoError(err)
	s.Equal(original, string(oldContent))
	_, err = os.Stat(s.DataFilePath(args.Path))
	s.Require().ErrorIs(err, os.ErrNotExist)
	entries, err := s.engine.ListSourceFiles()
	s.Require().NoError(err)
	s.Require().Len(entries, 1)
	s.False(entries[0].Enabled)
	s.Require().NoError(s.engine.LiveLoadFile(path))
	s.VerifyEmpty()
	checkNoTemporaryScripts(s.T(), s.DataFileTempDir())
	s.EnsureNoErrorsOrWarnings()
}

func TestRuleEditorSuite(t *testing.T) {
	testutils.RunSuites(t, new(RuleEditorSuite))
}
