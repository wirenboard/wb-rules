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
