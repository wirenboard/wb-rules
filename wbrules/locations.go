package wbrules

// LocItem represents a device or rule location in the source file
type LocItem struct {
	Line int    `json:"line"`
	Name string `json:"name"`
}

// LocFileEntry represents a source file
type LocFileEntry struct {
	Devices      []LocItem    `json:"devices"`
	Error        *ScriptError `json:"error,omitempty"`
	Rules        []LocItem    `json:"rules"`
	VirtualPath  string       `json:"virtualPath"`
	PhysicalPath string       `json:"-"`
}

// LocFileManager interface provides a way to access a list of source
// files
type LocFileManager interface {
	ScriptDir() string
	ListSourceFiles() ([]LocFileEntry, error)
	LiveWriteScript(virtualPath, content string) error
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
