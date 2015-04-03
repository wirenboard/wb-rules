package wbrules

import (
	"bytes"
	"fmt"
	wbgo "github.com/contactless/wbgo"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type CommandResult struct {
	ExitStatus          int
	CapturedOutput      string
	CapturedErrorOutput string
}

func captureCommandOutput(pipe io.ReadCloser, wg *sync.WaitGroup, result *string, e *error) {
	wg.Add(1)
	go func() {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, pipe); err == nil {
			*result = string(buf.Bytes())
		} else {
			*e = err
		}
		wg.Done()
	}()
}

func Spawn(name string, args []string, captureOutput bool, captureErrorOutput bool, input *string) (*CommandResult, error) {
	r := &CommandResult{0, "", ""}
	var err error
	var stdinPipe io.WriteCloser
	var stdoutPipe io.ReadCloser
	var stderrPipe io.ReadCloser
	cmd := exec.Command(name, args...)
	if input != nil {
		if stdinPipe, err = cmd.StdinPipe(); err != nil {
			return nil, fmt.Errorf("cmd.StdinPipe() failed: %s", err)
		}
	}
	if captureOutput {
		if stdoutPipe, err = cmd.StdoutPipe(); err != nil {
			return nil, fmt.Errorf("cmd.StdoutPipe() failed: %s", err)
		}
	}
	if captureErrorOutput {
		if stderrPipe, err = cmd.StderrPipe(); err != nil {
			return nil, fmt.Errorf("cmd.StderrPipe() failed: %s", err)
		}
	} else {
		cmd.Stderr = os.Stderr
	}

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("cmd.Start() failed: %s", err)
	}

	if stdinPipe != nil || stdoutPipe != nil || stderrPipe != nil {
		var wg sync.WaitGroup
		if stdinPipe != nil {
			wg.Add(1)
			go func() {
				io.WriteString(stdinPipe, *input)
				stdinPipe.Close()
				wg.Done()
			}()
		}
		if stderrPipe != nil {
			captureCommandOutput(stderrPipe, &wg, &r.CapturedErrorOutput, &err)
		}
		if stdoutPipe != nil {
			captureCommandOutput(stdoutPipe, &wg, &r.CapturedOutput, &err)
		}
		wg.Wait()
		if err != nil {
			return nil, fmt.Errorf("error capturing output: %s", err)
		}
	}

	if err = cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			r.ExitStatus = exitErr.Sys().(syscall.WaitStatus).ExitStatus()
			wbgo.Debug.Printf("command '%s': error: exit status: %d", cmd, r.ExitStatus)
		} else {
			return nil, err
		}
	}

	return r, nil
}
