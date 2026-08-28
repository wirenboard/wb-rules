package wbrules

// LocItem represents a device or rule location in the source file
type LocItem struct {
	Line int    `json:"line"`
	Name string `json:"name"`
}

// LocFileEntry represents a source file
type LocFileEntry struct {
	Enabled     bool         `json:"enabled"`
	Error       *ScriptError `json:"error,omitempty"`
	VirtualPath string       `json:"virtualPath"`
	Rules       []LocItem    `json:"rules"`
	Devices     []LocItem    `json:"devices"`
	Timers      []LocItem    `json:"timers"`

	PhysicalPath string     `json:"-"`
	Context      *ESContext `json:"-"`
}

// LocFileManager interface provides a way to access a list of source
// files
type LocFileManager interface {
	ScriptDir() string
	ListSourceFiles() ([]LocFileEntry, error)
	LiveWriteScript(virtualPath, content string) error
}

// TsFileManager is the optional TypeScript extension of LocFileManager.
// The Editor detects it with a type assertion, so a LocFileManager
// implementation without TypeScript support (an out-of-repository adapter
// or test double) keeps compiling and simply answers "unsupported".
type TsFileManager interface {
	// CheckTsFile returns the cached background type-check verdict for
	// one rule file plus a status: "ready", "pending", "not-ts" or
	// "unsupported" (see TS_CHECK_* constants).
	CheckTsFile(physicalPath string) ([]TSDiag, string)
	// TsTypesContent returns the installed wb-rules.d.ts so editors can
	// validate against the API of the engine actually running.
	TsTypesContent() (string, error)
}

// ModuleResolver is an optional LocFileManager extension serving the
// Editor.ResolveModule RPC: the engine's own import resolution, so an editor
// can type imports against the module files the controller actually has.
type ModuleResolver interface {
	// ResolveModuleForEditor resolves `spec` as written in the file at
	// `fromPhysical` and returns the module's absolute path and source.
	ResolveModuleForEditor(fromPhysical, spec string) (path, content string, err error)
}

// ScriptError denotes an error that was caused by JavaScript code.
// Files with such errors are partially loaded.
type ScriptError struct {
	Message   string    `json:"message"`
	Traceback []LocItem `json:"traceback"`
}

func NewScriptError(message string, traceback []LocItem) ScriptError {
	return ScriptError{message, traceback}
}

func (err ScriptError) Error() string {
	return err.Message
}
