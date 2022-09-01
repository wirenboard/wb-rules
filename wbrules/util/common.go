package util

import (
	"fmt"
)

type ContextType int

const (
	Code ContextType = iota
	SingleQuotes
	DoubleQuotes
	SingleLineComment
	MiltiLineComment
)

func CheckWbScript(src string) error {
	var prevCh int32 = ' '
	var braceCounter int = 0
	var row int = 1
	var col int = 0
	var contextType = Code

	var printRowCol = func() string {
		return fmt.Sprintf("(%d:%d)", row, col)
	}

	for _, ch := range src {
		col++
		switch ch {
		case '{':
			if contextType == Code {
				braceCounter++
			}
		case '}':
			if contextType == Code {
				braceCounter--
			}
		case '\n':
			row++
			prevCh = ' '
			switch contextType {
			case SingleLineComment:
				contextType = Code
			case SingleQuotes:
				fallthrough
			case DoubleQuotes:
				if prevCh != '\\' {
					return fmt.Errorf("String format error")
				}
			}
			col = 0
		case '/':
			if prevCh == '/' && contextType == Code {
				contextType = SingleLineComment
			} else if prevCh == '*' && contextType == MiltiLineComment {
				contextType = Code
			}
		case '*':
			if contextType == Code && prevCh == '/' {
				contextType = MiltiLineComment
			}
		case '"':
			switch contextType {
			case Code:
				contextType = DoubleQuotes
			case DoubleQuotes:
				contextType = Code
			}
		case '\'':
			switch contextType {
			case Code:
				contextType = SingleQuotes
			case SingleQuotes:
				contextType = Code
			}
		}
		if braceCounter < 0 {
			return fmt.Errorf("%s Missing opening bracket", printRowCol())
		}
		prevCh = ch
	}
	if braceCounter > 0 {
		return fmt.Errorf("Missing closed bracket")
	}
	if contextType == MiltiLineComment {
		return fmt.Errorf("Multiline comment is not closed")
	}
	if contextType == SingleQuotes || contextType == DoubleQuotes {
		return fmt.Errorf("String is not closed")
	}
	return nil
}

func WrapWbScriptToJSFunction(src string) (error, string) {
	if err := CheckWbScript(src); err != nil {
		return err, ""
	}
	result := "function(module){" + src + "\n}"
	return nil, result
}
