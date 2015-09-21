package appkit

import (
	"fmt"
)

type Error interface {
	GetCode() string
	GetMessage() string
	GetData() interface{}

	IsInternal() bool

	GetErrors() []error
	AddError(error)

	Error() string
}

type AppError struct {
	Code     string      `json:"code,omitempty"`
	Message  string      `json:"title,omitempty"`
	Data     interface{} `json:"-"`
	Internal bool
	Errors   []error
}

// Ensure error implements the error interface.
var _ Error = (*AppError)(nil)

func (e AppError) GetCode() string {
	return e.Code
}

func (e AppError) GetMessage() string {
	return e.Message
}

func (e AppError) GetData() interface{} {
	return e.Data
}

func (e AppError) IsInternal() bool {
	return e.Internal
}

func (e AppError) GetErrors() []error {
	return e.Errors
}

func (e AppError) AddError(err error) {
	e.Errors = append(e.Errors, err)
}

func (e AppError) Error() string {
	s := e.Code
	if e.Message != "" {
		s += ": " + e.Message
	}

	if e.Data != nil {
		s += "\n" + fmt.Sprintf("%+v", e.Data)
	}

	return s
}

func WrapError(err error, code, msg string) *AppError {
	wrap := &AppError{
		Code:    code,
		Message: err.Error(),
		Errors:  []error{err},
	}

	if msg != "" {
		wrap.Message = msg + ":" + wrap.Message
	}

	return wrap
}

func IsError(err Error, code string) bool {
	return err.GetCode() == code
}
