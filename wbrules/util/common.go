package util

import (
	"fmt"
	"github.com/wirenboard/wb-rules/wbrules/eserror"
)

type ContextType int

const (
	Code ContextType = iota
	SingleQuotesString
	DoubleQuotesString
	TemplateString
	SingleLineComment
	MiltiLineComment
)

func CheckWbScript(src string) error {
	var prevCh int32 = ' '
	var braceCounter int = 0
	var row int = 1
	var col int = 0
	var contextType = Code

	var createEsError = func(errorStr string) eserror.ESError {
		err := eserror.ESError{
			errorStr + fmt.Sprintf(" (line: %d column: %d)", row, col),
			eserror.ESTraceback{
				{Filename: "", Line: row},
			},
		}
		return err
	}

	var quotesStringSwitcher = func(ctTarget ContextType, ct ContextType) ContextType {
		switch ct {
		case Code:
			return ctTarget
		case ctTarget:
			if prevCh != '\\' {
				return Code
			}
		}
		return ct
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
			prevCh = ' '
			switch contextType {
			case SingleLineComment:
				contextType = Code
			case SingleQuotesString:
				fallthrough
			case DoubleQuotesString:
				if prevCh != '\\' {
					return createEsError(fmt.Sprintf("String format error"))
				}
			}
			row++
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
			contextType = quotesStringSwitcher(DoubleQuotesString, contextType)
		case '\'':
			contextType = quotesStringSwitcher(SingleQuotesString, contextType)
		case '`':
			contextType = quotesStringSwitcher(TemplateString, contextType)
		}
		if braceCounter < 0 {
			return createEsError(fmt.Sprintf("Missing opening bracket"))
		}
		prevCh = ch
	}

	if braceCounter > 0 {
		return createEsError(fmt.Sprintf("Missing closed bracket"))
	}
	if contextType == MiltiLineComment {
		return createEsError(fmt.Sprintf("Multiline comment is not closed"))
	}
	if contextType == SingleQuotesString || contextType == DoubleQuotesString {
		return createEsError(fmt.Sprintf("String is not closed"))
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
