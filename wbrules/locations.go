package wbrules

// LocItem represents a device or rule location in the source file
type LocItem struct {
	Line int    `json:"line"`
	Name string `json:"name"`
}

// LocFileEntry represents a source file
type LocFileEntry struct {
	Devices      []LocItem `json:"devices"`
	Rules        []LocItem `json:"rules"`
	VirtualPath  string    `json:"virtualPath"`
	PhysicalPath string    `json:"-"`
}

// LocFileManager interface provides a way to access a list of source
// files
type LocFileManager interface {
	ScriptDir() string
	ListSourceFiles() ([]LocFileEntry, error)
}
