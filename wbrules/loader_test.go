package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"io/ioutil"
	"os"
	"path"
	"testing"
)

type loaderFixture struct {
	wbgo.Recorder
	t                  *testing.T
	cleanup            []func()
	dir1, dir2, f5, f6 string
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
		dir1, dir2, "", "",
		nil,
	}
	fixture.loader = NewLoader("\\.js$", func(filePath string) error {
		if bs, err := ioutil.ReadFile(filePath); err != nil {
			t.Fatalf("failed to load file %s: %s", filePath, err)
		} else {
			fixture.Rec("L: %s", string(bs))
		}
		return nil
	})

	fixture.writeFile(dir1, "f1.js", "// f1")
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
	for _, f := range fixture.cleanup {
		f()
	}
}

func TestLoader(t *testing.T) {
	fixture := newLoaderFixture(t)
	defer fixture.tearDown()

	fixture.loader.Load(fixture.dir1)
	fixture.Verify("L: // f1", "L: // f2", "L: // f4")

	fixture.loader.Load(fixture.f5)
	fixture.Verify("L: // f5")

	// direct path specification will load even non-matching files
	fixture.loader.Load(fixture.f6)
	fixture.Verify("L: // f6")
}

// TBD: test unreadable files (like broken symlinks)
// TBD: watching (fsnotify)
