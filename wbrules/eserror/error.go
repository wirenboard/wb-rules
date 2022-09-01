package eserror

type ESError struct {
	Message   string
	Traceback ESTraceback
}

func (err ESError) Error() string {
	return err.Message
}

type ESLocation struct {
	Filename string
	Line     int
}

type ESTraceback []ESLocation
