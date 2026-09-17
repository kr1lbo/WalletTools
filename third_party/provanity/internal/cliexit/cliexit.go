// Package cliexit defines the small exit-code helpers shared between the
// provanity and provanity-worker binaries. The interfaces (ExitCode, SuppressMainPrint)
// match the contract that each main.go inspects to control process exit and
// duplicate stderr printing.
package cliexit

import (
	"fmt"

	"github.com/spf13/cobra"
)

type CodedError struct {
	Code int
	Err  error
}

func (e CodedError) Error() string { return e.Err.Error() }
func (e CodedError) Unwrap() error { return e.Err }
func (e CodedError) ExitCode() int { return e.Code }

func WithCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return CodedError{Code: code, Err: err}
}

type PrintedError struct {
	CodedError
}

func (e PrintedError) SuppressMainPrint() bool { return true }

func Printed(cmd *cobra.Command, code int, message string) error {
	fmt.Fprintln(cmd.ErrOrStderr(), message)
	return PrintedError{CodedError: CodedError{Code: code, Err: fmt.Errorf("%s", message)}}
}
