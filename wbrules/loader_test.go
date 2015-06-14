package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"io/ioutil"
	"os"
	"path"
	"testing"
	"time"
)

type LoaderSuite struct {
	wbgo.Suite
	*wbgo.Recorder
	cleanup            []func()
	dir1, dir2, subdir string
	f1, f5, f6         string
	loader             *Loader
}

func (s *LoaderSuite) T() *testing.T {
	return s.Suite.T()
}

func (s *LoaderSuite) SetupTest() {
	s.Suite.SetupTest()
	s.cleanup = make([]func(), 2)
	s.dir1, s.cleanup[0] = wbgo.SetupTempDir(s.T())
	s.dir2, s.cleanup[1] = wbgo.SetupTempDir(s.T())

	s.subdir = path.Join(s.dir1, "subdir")
	if err := os.Mkdir(s.subdir, 0777); err != nil {
		s.Require().Fail("error creating subdir", "%s", err)
	}

	s.Recorder = wbgo.NewRecorder(s.T())
	s.loader = NewLoader("\\.js$", func(filePath string, reloaded bool) error {
		bs, err := ioutil.ReadFile(filePath)
		switch {
		case err != nil:
			return err
		case reloaded:
			s.Rec("R: %s", string(bs))
		default:
			s.Rec("L: %s", string(bs))
		}
		return nil
	})

	// make tests a bit quicker
	s.loader.SetDelay(100 * time.Millisecond)
	s.SetEmptyWaitTime(200 * time.Millisecond)

	s.f1 = s.writeFile(s.dir1, "f1.js", "// f1")
	s.writeFile(s.dir1, "f2.js", "// f2")
	s.writeFile(s.dir1, "f3.js.noload", "// f3 (not loaded)")
	s.writeFile(s.subdir, "f4.js", "// f4")

	s.f5 = s.writeFile(s.dir2, "f5.js", "// f5")
	s.f6 = s.writeFile(s.dir2, "f6.js", "// f6")
}

func (s *LoaderSuite) writeFile(dir, filename, content string) string {
	fullPath := path.Join(dir, filename)
	if err := ioutil.WriteFile(fullPath, []byte(content), 0777); err != nil {
		s.Require().Fail("failed to write file", "%s: %s", fullPath, err)
	}
	return fullPath
}

func (s *LoaderSuite) TearDownTest() {
	s.VerifyEmpty()
	s.loader.Stop()
	for _, f := range s.cleanup {
		f()
	}
	s.Suite.TearDownTest()
}

func (s *LoaderSuite) loadDir1() {
	s.loader.Load(s.dir1)
	s.Verify("L: // f1", "L: // f2", "L: // f4")
}

func (s *LoaderSuite) loadAll() {
	s.loadDir1()

	s.loader.Load(s.f5)
	s.Verify("L: // f5")

	// direct path specification will load even non-matching files
	s.loader.Load(s.f6)
	s.Verify("L: // f6")

	s.VerifyEmpty()
}

func (s *LoaderSuite) TestPlainLoading() {
	s.loadAll()
}

func (s *LoaderSuite) TestAddingNewFile() {
	s.loadDir1()

	s.writeFile(s.dir1, "f2_1.js", "// f2_1")
	s.Verify("R: // f2_1")

	s.writeFile(s.subdir, "f4_1.js", "// f4_1")
	s.Verify("R: // f4_1")

	// add a non-matching file
	s.writeFile(s.dir1, "whatever.txt", "noload")
	s.VerifyEmpty()

	// make sure the new files are watched properly
	s.writeFile(s.dir1, "f2_1.js", "// f2_1 (changed)")
	s.writeFile(s.subdir, "f4_1.js", "// f4_1 (changed)")
	s.Verify("R: // f2_1 (changed)", "R: // f4_1 (changed)")
}

func (s *LoaderSuite) TestModification() {
	s.loadAll()

	s.writeFile(s.dir1, "f1.js", "// f1 (changed)")
	s.Verify("R: // f1 (changed)")

	s.writeFile(s.dir2, "f5.js", "// f5 (changed)")
	s.Verify("R: // f5 (changed)")
}

func (s *LoaderSuite) TestRenaming() {
	s.loadAll()

	os.Rename(s.f1, path.Join(s.dir1, "f1_renamed.js"))
	s.Verify("R: // f1")

	// make sure the file is still watched after rename
	s.writeFile(s.dir1, "f1_renamed.js", "// f1_renamed (changed)")
	s.Verify("R: // f1_renamed (changed)")

	// when an explicitly specified file is renamed, it's no longer watched
	os.Rename(s.f5, path.Join(s.dir2, "f5_renamed.js"))
	s.writeFile(s.dir2, "f5_renamed.js", "// f5_renamed (changed)")
	s.VerifyEmpty()

	// FIXME: should track directories of explicitly specified files
	// to see when they reappear

	newSubdir := path.Join(s.dir1, "subdir_renamed")
	os.Rename(s.subdir, newSubdir)
	s.Verify("R: // f4")

	// make sure the directory is still watched after rename
	s.writeFile(newSubdir, "f4.js", "// f4 (changed)")
	s.Verify("R: // f4 (changed)")
}

func (s *LoaderSuite) TestFileRemoval() {
	s.loadAll()

	s.writeFile(s.dir1, "f1.js", "// f1 (should be ignored)")
	// make it likely that change event is not swallowed
	// due to the following deletion
	time.Sleep(50 * time.Millisecond)
	os.RemoveAll(s.subdir)
	os.Remove(s.f1)
	os.Remove(s.f5)

	// VerifyEmpty() is invoked during teardown
}

func (s *LoaderSuite) TestUnreadableFiles() {
	err := os.Symlink(path.Join(s.dir1, "blabla.js"), path.Join(s.dir1, "test.js"))
	if err != nil {
		s.Require().Fail("failed to create symlink", "%s", err)
	}
	s.loadAll()
	s.EnsureGotWarnings()

	err = os.Symlink(path.Join(s.dir1, "blabla1.js"), path.Join(s.dir1, "test1.js"))
	if err != nil {
		s.Require().Fail("failed to create symlink", "%s", err)
	}

	// must have s.VerifyEmpty() here so the warnings have time to appear
	s.VerifyEmpty()
	s.EnsureGotWarnings()
}

func (s *LoaderSuite) TestStoppingLoader() {
	s.loadAll()
	s.loader.Stop()

	s.writeFile(s.dir1, "f2_1.js", "// f2_1")
	s.writeFile(s.dir2, "f5.js", "// f5 (changed)")
	s.VerifyEmpty()
}

func TestLoaderSuite(t *testing.T) {
	wbgo.RunSuites(t, new(LoaderSuite))
}
