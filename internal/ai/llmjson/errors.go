// Package llmjson validates structured JSON from LLM responses and tool arguments.
package llmjson

import (
	"errors"
	"fmt"
)

type Kind int

const (
	KindParse Kind = iota
	KindSchema
)

type OutputError struct {
	Kind Kind
	Msg  string
	Err  error
}

func (e *OutputError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *OutputError) Unwrap() error { return e.Err }

func IsOutputError(err error) bool {
	var oe *OutputError
	return errors.As(err, &oe)
}

func IsParseError(err error) bool {
	var oe *OutputError
	return errors.As(err, &oe) && oe.Kind == KindParse
}

func IsSchemaError(err error) bool {
	var oe *OutputError
	return errors.As(err, &oe) && oe.Kind == KindSchema
}

func ParseError(msg string, err error) error {
	return &OutputError{Kind: KindParse, Msg: msg, Err: err}
}

func SchemaError(msg string, err error) error {
	if err == nil {
		return &OutputError{Kind: KindSchema, Msg: msg}
	}
	return &OutputError{Kind: KindSchema, Msg: msg, Err: err}
}
