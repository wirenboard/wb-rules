package util

import "testing"

func TestCheckWbScriptCorrectSamples(t *testing.T) {
	var correctSamples = []string{
		"",
		"{{{{{{}}}}}}",
		`
{{"{}1' + 2}{"
/* { "" ' 
*/
//}}
}}
		`,
		"{{1 + 2 \n // \n}\n/* jnjknkjn \n*/}",
	}
	for num, sample := range correctSamples {
		if err := CheckWbScript(sample); err != nil {
			t.Errorf("Function result is false for correct samples. SampleNumber=%d, Error=%s", num+1, err.Error())
		}
	}
}

func TestCheckWbScriptIncorrectSamples(t *testing.T) {
	var incorrectSamples = []string{
		"}",
		"{{{{{{}}}}",
		"{{1 + 2 \n//}}\n}//}",
		"{{1 + 2 \n // \n}\n/* jnjknkjn \n*/",
	}
	for num, sample := range incorrectSamples {
		if err := CheckWbScript(sample); err == nil {
			t.Errorf("Function result is true for incorrect samples. SampleNumber=%d", num+1)
		}
	}
}

func TestWrapWbScriptToJSFunction(t *testing.T) {
	const src = "1 + 2"
	if _, result := WrapWbScriptToJSFunction(src); result != "function(module){1 + 2\n}" {
		t.Errorf("Invalid conversion")
	}
}
