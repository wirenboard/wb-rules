package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"io/ioutil"
	"os"
	"path"
	"testing"
	"time"
)

type loaderFixture struct {
	wbgo.Recorder
	t                  *testing.T
	cleanup            []func()
	dir1, dir2, subdir string
	f1, f5, f6         string
	loader             *Loader
}

func newLoaderFixture(t *testing.T) *loaderFixture {
	dir1, cleanup1 := wbgo.SetupTempDir(t)
	dir2, cleanup2 := wbgo.SetupTempDir(t)

	subdir := path.Join(dir1, "subdir")
	if err := os.Mkdir(subdir, 0777); err != nil {
		t.Fatalf("error creating subdir: %s", err)
	}

	fixture := &loaderFixture{
		*wbgo.NewRecorder(t),
		t,
		[]func(){cleanup1, cleanup2},
		dir1, dir2, subdir, "", "", "",
		nil,
	}
	fixture.loader = NewLoader("\\.js$", func(filePath string, reloaded bool) error {
		bs, err := ioutil.ReadFile(filePath)
		switch {
		case err != nil:
			return err
		case reloaded:
			fixture.Rec("R: %s", string(bs))
		default:
			fixture.Rec("L: %s", string(bs))
		}
		return nil
	})

	// make tests a bit quicker
	fixture.loader.SetDelay(100 * time.Millisecond)
	fixture.SetEmptyWaitTime(200 * time.Millisecond)

	fixture.f1 = fixture.writeFile(dir1, "f1.js", "// f1")
	fixture.writeFile(dir1, "f2.js", "// f2")
	fixture.writeFile(dir1, "f3.js.noload", "// f3 (not loaded)")
	fixture.writeFile(subdir, "f4.js", "// f4")

	fixture.f5 = fixture.writeFile(dir2, "f5.js", "// f5")
	fixture.f6 = fixture.writeFile(dir2, "f6.js", "// f6")

	wbgo.SetupTestLogging(t)

	return fixture
}

func (fixture *loaderFixture) writeFile(dir, filename, content string) string {
	fullPath := path.Join(dir, filename)
	if err := ioutil.WriteFile(fullPath, []byte(content), 0777); err != nil {
		fixture.t.Fatalf("failed to write %s: %s", fullPath, err)
	}
	return fullPath
}

func (fixture *loaderFixture) tearDown() {
	fixture.VerifyEmpty()
	fixture.loader.Stop()
	for _, f := range fixture.cleanup {
		f()
	}
	wbgo.EnsureNoErrorsOrWarnings(fixture.t)
}

func (fixture *loaderFixture) loadDir1() {
	wbgo.Debug.Printf("aaa")
	fixture.loader.Load(fixture.dir1)
	wbgo.Debug.Printf("rrr")
	fixture.Verify("L: // f1", "L: // f2", "L: // f4")
}

func (fixture *loaderFixture) loadAll() {
	fixture.loadDir1()
	wbgo.Debug.Printf("qqq")

	fixture.loader.Load(fixture.f5)
	fixture.Verify("L: // f5")

	// direct path specification will load even non-matching files
	fixture.loader.Load(fixture.f6)
	fixture.Verify("L: // f6")

	wbgo.Debug.Printf("zzz")
	fixture.VerifyEmpty()
}

func TestPlainLoading(t *testing.T) {
	fixture := newLoaderFixture(t)
	defer fixture.tearDown()

	fixture.loadAll()
}

func TestAddingNewFile(t *testing.T) {
	fixture := newLoaderFixture(t)
	defer fixture.tearDown()

	fixture.loadDir1()

	fixture.writeFile(fixture.dir1, "f2_1.js", "// f2_1")
	fixture.Verify("R: // f2_1")

	fixture.writeFile(fixture.subdir, "f4_1.js", "// f4_1")
	fixture.Verify("R: // f4_1")

	// add a non-matching file
	fixture.writeFile(fixture.dir1, "whatever.txt", "noload")
	fixture.VerifyEmpty()

	// make sure the new files are watched properly
	fixture.writeFile(fixture.dir1, "f2_1.js", "// f2_1 (changed)")
	fixture.writeFile(fixture.subdir, "f4_1.js", "// f4_1 (changed)")
	fixture.Verify("R: // f2_1 (changed)", "R: // f4_1 (changed)")
}

func TestModification(t *testing.T) {
	fixture := newLoaderFixture(t)
	defer fixture.tearDown()

	fixture.loadAll()

	fixture.writeFile(fixture.dir1, "f1.js", "// f1 (changed)")
	fixture.Verify("R: // f1 (changed)")

	fixture.writeFile(fixture.dir2, "f5.js", "// f5 (changed)")
	fixture.Verify("R: // f5 (changed)")
}

func TestRenaming(t *testing.T) {
	fixture := newLoaderFixture(t)
	defer fixture.tearDown()

	fixture.loadAll()

	os.Rename(fixture.f1, path.Join(fixture.dir1, "f1_renamed.js"))
	fixture.Verify("R: // f1")

	// make sure the file is still watched after rename
	fixture.writeFile(fixture.dir1, "f1_renamed.js", "// f1_renamed (changed)")
	fixture.Verify("R: // f1_renamed (changed)")

	// when an explicitly specified file is renamed, it's no longer watched
	os.Rename(fixture.f5, path.Join(fixture.dir2, "f5_renamed.js"))
	fixture.writeFile(fixture.dir2, "f5_renamed.js", "// f5_renamed (changed)")
	fixture.VerifyEmpty()

	// FIXME: should track directories of explicitly specified files
	// to see when they reappear

	newSubdir := path.Join(fixture.dir1, "subdir_renamed")
	os.Rename(fixture.subdir, newSubdir)
	fixture.Verify("R: // f4")

	// make sure the directory is still watched after rename
	fixture.writeFile(newSubdir, "f4.js", "// f4 (changed)")
	fixture.Verify("R: // f4 (changed)")
}

func TestFileRemoval(t *testing.T) {
	fixture := newLoaderFixture(t)
	defer fixture.tearDown()

	fixture.loadAll()

	fixture.writeFile(fixture.dir1, "f1.js", "// f1 (should be ignored)")
	// make it likely that change event is not swallowed
	// due to the following deletion
	time.Sleep(50 * time.Millisecond)
	os.RemoveAll(fixture.subdir)
	os.Remove(fixture.f1)
	os.Remove(fixture.f5)

	// VerifyEmpty() is invoked during teardown
}

func TestUnreadableFiles(t *testing.T) {
	fixture := newLoaderFixture(t)
	defer fixture.tearDown()

	err := os.Symlink(path.Join(fixture.dir1, "blabla.js"), path.Join(fixture.dir1, "test.js"))
	if err != nil {
		t.Fatalf("failed to create symlink: %s", err)
	}
	fixture.loadAll()
	wbgo.EnsureGotWarnings(t)

	err = os.Symlink(path.Join(fixture.dir1, "blabla1.js"), path.Join(fixture.dir1, "test1.js"))
	if err != nil {
		t.Fatalf("failed to create symlink: %s", err)
	}

	// must have fixture.VerifyEmpty() here so the warnings have time to appear
	fixture.VerifyEmpty()
	wbgo.EnsureGotWarnings(t)
}

func TestStoppingLoader(t *testing.T) {
	fixture := newLoaderFixture(t)
	defer fixture.tearDown()

	fixture.loadAll()
	fixture.loader.Stop()

	fixture.writeFile(fixture.dir1, "f2_1.js", "// f2_1")
	fixture.writeFile(fixture.dir2, "f5.js", "// f5 (changed)")
	fixture.VerifyEmpty()
}
