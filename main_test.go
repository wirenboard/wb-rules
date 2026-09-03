package main

import (
	"errors"
	"testing"
)

func TestExitCodeForFailure(t *testing.T) {
	testCases := []struct {
		name     string
		reason   any
		expected int
	}{
		{
			name:     "not authorized",
			reason:   "network Error : not Authorized",
			expected: 2,
		},
		{
			name:     "bad user name or password",
			reason:   errors.New("bad user name or password"),
			expected: 2,
		},
		{
			name:     "connection refused",
			reason:   "network Error : dial tcp 127.0.0.1:1883: connect: connection refused",
			expected: 1,
		},
		{
			name:     "unexpected panic",
			reason:   errors.New("unexpected failure"),
			expected: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := exitCodeForFailure(testCase.reason); actual != testCase.expected {
				t.Errorf("exitCodeForFailure(%q) = %d, want %d", testCase.reason, actual, testCase.expected)
			}
		})
	}
}
