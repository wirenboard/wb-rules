package wbrules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
	bolt "go.etcd.io/bbolt"
)

func TestOpenPersistentDBRecoversInvalidFile(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "persistent.db")
	invalidContents := []byte("not a bbolt database")
	assert := require.New(t)
	assert.NoError(os.WriteFile(filename, invalidContents, 0600))
	db, err := openPersistentDB(filename, 0640)
	assert.NoError(err)
	assert.NotNil(db)
	assert.NoError(db.Close())

	db, err = bolt.Open(filename, 0640, nil)
	assert.NoError(err)
	assert.NoError(db.Close())
}

func TestOpenPersistentDBRecoversSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "data")
	target := filepath.Join(targetDir, "persistent.db")
	filename := filepath.Join(dir, "persistent.db")
	invalidContents := []byte("invalid database behind a symlink")
	assert := require.New(t)
	assert.NoError(os.Mkdir(targetDir, 0755))
	assert.NoError(os.WriteFile(target, invalidContents, 0600))
	assert.NoError(os.Symlink(filepath.Join("data", "persistent.db"), filename))
	db, err := openPersistentDB(filename, 0640)
	assert.NoError(err)
	assert.NotNil(db)
	assert.NoError(db.Close())

	linkTarget, err := os.Readlink(filename)
	assert.NoError(err)
	assert.Equal(filepath.Join("data", "persistent.db"), linkTarget)

	db, err = bolt.Open(target, 0640, nil)
	assert.NoError(err)
	assert.NoError(db.Close())
}

func TestPersistentStorageStrictMode(t *testing.T) {
	db, err := openPersistentDB(filepath.Join(t.TempDir(), "persistent.db"), 0600)
	require.NoError(t, err)
	defer db.Close()
	engine := &ESEngine{persistentDB: db}
	f := newESContextFactory()
	ctx := f.newESContext(nil, "")
	defer ctx.DestroyHeap()

	ctx.PushGlobalObject()
	ctx.DefineFunctions(map[string]func(*ESContext) int{
		"_wbPersistentName": engine.esPersistentName,
		"_wbPersistentSet":  engine.esPersistentSet,
		"_wbPersistentGet":  engine.esPersistentGet,
	})
	ctx.Pop()
	require.NoError(t, ctx.LoadScriptFromString("storage_setup.js", `
		var __wbVdevPrototype = {};
		function require() { return {}; }
	`))
	require.NoError(t, ctx.LoadScript("../scripts/lib.js"))

	tests := []struct {
		name     string
		script   string
		expected string
	}{
		{
			"storable object",
			`storage["foo"] = StorableObject({ name: "Temperature", value: 26.3 });`,
			`{"name":"Temperature","value":26.3}`,
		},
		{
			"standalone object update",
			`var obj = StorableObject({ value: 26.3 }); obj.value = 0; storage.foo = obj;`,
			`{"value":0}`,
		},
		{
			"stored object update",
			`var obj = StorableObject({ value: 26.3 }); storage.foo = obj; obj.value = 0;`,
			`{"value":0}`,
		},
		{
			"loaded object update",
			`storage.foo = StorableObject({ value: 26.3 }); storage.foo.value = 0;`,
			`{"value":0}`,
		},
		{
			"nested object update",
			`storage.foo = StorableObject({ nested: { value: 26.3 } }); storage.foo.nested.value = 0;`,
			`{"nested":{"value":0}}`,
		},
		{"false", `storage.foo = false;`, `false`},
		{"zero", `storage.foo = 0;`, `0`},
		{"empty string", `storage.foo = "";`, `""`},
		{"null deletes", `storage.foo = 42; storage.foo = null;`, ""},
		{"undefined deletes", `storage.foo = 42; storage.foo = undefined;`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, ctx.LoadScriptFromString("strict_storage.js", `
				"use strict";
				var storage = new PersistentStorage("storageName", {global: true});
			`+tt.script))
			require.NoError(t, db.View(func(tx *bolt.Tx) error {
				bucket := tx.Bucket([]byte("storageName"))
				require.NotNil(t, bucket)
				value := bucket.Get([]byte("foo"))
				if tt.expected == "" {
					require.Nil(t, value)
				} else {
					require.JSONEq(t, tt.expected, string(value))
				}
				return nil
			}))
		})
	}
}

type PersistentStorageSuite struct {
	RuleSuiteBase
	tmpDir string
}

func (s *PersistentStorageSuite) SetupFixture() {
	var err error

	// we need to create separated temp directory because persistent DB file
	// should be keeped between tests
	s.tmpDir, err = os.MkdirTemp("", "wbrulestest")
	if err != nil {
		s.FailNow("can't create temp directory")
	}
	wbgong.Debug.Printf("created temp dir %s", s.tmpDir)
}

func (s *PersistentStorageSuite) TearDownFixture() {
	os.RemoveAll(s.tmpDir)
}

func (s *PersistentStorageSuite) SetupTest() {
	s.PersistentDBFile = s.tmpDir + "/test_persistent.db"
	s.VdevStorageFile = s.tmpDir + "/test-vdev.db"
	s.SetupSkippingDefs()
	s.LiveLoadScriptToDir("testrules_persistent.js", s.tmpDir)
	s.SkipTill("[info] loaded file 1")
	s.LiveLoadScriptToDir("testrules_persistent_2.js", s.tmpDir)
	s.SkipTill("[info] loaded file 2")
}

func (s *PersistentStorageSuite) TearDownTest() {
	s.RuleSuiteBase.TearDownTest()
}

func (s *PersistentStorageSuite) TestPersistentStorage() {
	s.publish("/devices/vdev/controls/write/on", "1", "vdev/write")

	s.VerifyUnordered(
		"tst -> /devices/vdev/controls/write/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/write: [1] (QoS 1, retained)",
		"[info] pure object is not created",
		"[info] pure subobject is not created",
		"[info] for-in keys: name,foo,baz,sub",
		"[info] Object.keys: name,foo,baz,sub",
		"[info] write objects 42, \"HelloWorld\", {\"name\":\"MyObj\",\"foo\":\"bar\",\"baz\":84,\"sub\":{\"hello\":\"world\"}}",
	)
}

// try to read from persistent storage
func (s *PersistentStorageSuite) TestPersistentStorage2() {
	s.publish("/devices/vdev/controls/read/on", "1", "vdev/read")
	s.VerifyUnordered(
		"tst -> /devices/vdev/controls/read/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/read: [1] (QoS 1, retained)",
		"[info] read objects 42, \"HelloWorld\", {\"name\":\"MyObj\",\"foo\":\"bar\",\"baz\":84,\"sub\":{\"hello\":\"world\"}}",
		"[info] read objects 42, \"HelloWorld\", {\"name\":\"MyObj\",\"foo\":\"bar\",\"baz\":84,\"sub\":{\"hello\":\"earth\"}}",
	)
}

// test local storages in different files
func (s *PersistentStorageSuite) TestLocalPersistentStorage() {
	// write values
	s.publish("/devices/vdev/controls/localWrite1/on", "1", "vdev/localWrite1")
	s.SkipTill("[info] file1: write to local PS")

	s.publish("/devices/vdev/controls/localWrite2/on", "1", "vdev/localWrite2")
	s.SkipTill("[info] file2: write to local PS")

	// now read values
	s.publish("/devices/vdev/controls/localRead1/on", "1", "vdev/localRead1")
	s.SkipTill("[info] file1: read objects \"hello_from_1\", undefined")

	s.publish("/devices/vdev/controls/localRead2/on", "1", "vdev/localRead2")
	s.SkipTill("[info] file2: read objects undefined, \"hello_from_2\"")
}

func (s *PersistentStorageSuite) TestLocalPersistentStorage2() {
	// now read values
	s.publish("/devices/vdev/controls/localRead1/on", "1", "vdev/localRead1")
	s.SkipTill("[info] file1: read objects \"hello_from_1\", undefined")

	s.publish("/devices/vdev/controls/localRead2/on", "1", "vdev/localRead2")
	s.SkipTill("[info] file2: read objects undefined, \"hello_from_2\"")
}

func (s *PersistentStorageSuite) TestPersistentStorageTransactionErrors() {
	s.Require().NoError(s.engine.ClosePersistentDB())

	err := s.engine.EvalScript(`
		var ps = new PersistentStorage('test_storage_errors', { global: true });
		try {
			ps.key = 42;
		} catch (e) {
			log('persistent write failed: ' + e);
		}
		try {
			ps.key;
		} catch (e) {
			log('persistent read failed: ' + e);
		}
	`)
	s.Require().NoError(err)

	s.VerifyUnordered(
		"[error] can't update persistent storage test_storage_errors/key: database not open",
		"[info] persistent write failed: Error: can't update persistent storage test_storage_errors/key: database not open",
		"[error] can't read persistent storage test_storage_errors/key: database not open",
		"[info] persistent read failed: Error: can't read persistent storage test_storage_errors/key: database not open",
	)
}

func TestPersistentStorageSuite(t *testing.T) {
	s := new(PersistentStorageSuite)
	s.SetupFixture()
	defer s.TearDownFixture()
	testutils.RunSuites(t, s)
}
