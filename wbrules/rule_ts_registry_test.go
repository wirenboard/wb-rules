package wbrules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The pure renderer: shape, escaping, and the empty case.
func TestRenderControlsRegistry(t *testing.T) {
	assert.Equal(t, "", renderControlsRegistry(nil, nil), "no controls -> no registry file")

	out := renderControlsRegistry([]controlRef{
		{"climate/temperature", "temperature"},
		{"living/lamp", "switch"},
	}, nil)
	assert.Contains(t, out, "interface WbControls {")
	assert.Contains(t, out, `"climate/temperature": "temperature";`)
	assert.Contains(t, out, `"living/lamp": "switch";`)

	// keys/values are escaped for a TS string literal
	assert.Contains(t,
		renderControlsRegistry([]controlRef{{`a"b/c`, "value"}}, nil),
		`"a\"b/c": "value";`)

	// exotic control chars use JSON/TS-safe \uXXXX escapes, not strconv.Quote's
	// \a / \UXXXXXXXX which TS rejects (would break every rule's check)
	bell := renderControlsRegistry([]controlRef{{"a\x07b/c", "value"}}, nil)
	assert.Contains(t, bell, "\\u0007")
	assert.NotContains(t, bell, "\\a")

	// aliases become declare-var lines; a non-identifier alias is skipped
	// (loose), never a broken declaration file
	withAliases := renderControlsRegistry([]controlRef{{"a/b", "value"}}, []string{"heaterOn", "not an ident", "_ok$1"})
	assert.Contains(t, withAliases, "declare var heaterOn: any;")
	assert.Contains(t, withAliases, "declare var _ok$1: any;")
	assert.NotContains(t, withAliases, "not an ident")

	// aliases alone still produce a declaration file
	aliasesOnly := renderControlsRegistry(nil, []string{"lampOn"})
	assert.Contains(t, aliasesOnly, "declare var lampOn: any;")
}

// The diagnostic-path matcher must recognize the registry/types file even when
// tsgo prints it relative to a cwd that isn't its ancestor (a "../.." path) -
// the suffix-only check TestPathsRefSameFile covers doesn't handle that.
func TestPathsRefSameFileResolvesRelative(t *testing.T) {
	// a "../.." relative path (cwd not an ancestor of /tmp) resolves and matches
	wd, err := os.Getwd()
	assert.NoError(t, err)
	if rel, err := filepath.Rel(wd, "/tmp/reg.d.ts"); err == nil {
		assert.True(t, pathsRefSameFile(rel, "/tmp/reg.d.ts"))
	}
	assert.False(t, pathsRefSameFile("/tmp/a.d.ts", "/tmp/b.d.ts"))
}
