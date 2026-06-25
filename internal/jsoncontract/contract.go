package jsoncontract

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion is the current JSON contract version shared by Go and Emacs.
const SchemaVersion = 1

// Message is the stable warning/error shape inside an Envelope.
type Message struct {
	Code      ErrorCode      `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	Retryable bool           `json:"retryable"`
}

// Envelope is the top-level JSON object returned by machine-readable safeslop
// interfaces. Data remains command-specific, but the envelope fields are stable.
type Envelope struct {
	SchemaVersion int            `json:"schema_version"`
	OK            bool           `json:"ok"`
	Data          map[string]any `json:"data"`
	Warnings      []Message      `json:"warnings"`
	Errors        []Message      `json:"errors"`
}

// OK returns a successful v1 envelope with data.
func OK(data map[string]any, warnings ...Message) Envelope {
	return Envelope{
		SchemaVersion: SchemaVersion,
		OK:            true,
		Data:          nonNilMap(data),
		Warnings:      nonNilMessages(warnings),
		Errors:        []Message{},
	}
}

// Error returns a failed v1 envelope with errors.
func Error(errors ...Message) Envelope {
	return Envelope{
		SchemaVersion: SchemaVersion,
		OK:            false,
		Data:          map[string]any{},
		Warnings:      []Message{},
		Errors:        nonNilMessages(errors),
	}
}

// NewMessage constructs a warning or error message with non-nil details.
func NewMessage(code ErrorCode, message string, retryable bool, details map[string]any) Message {
	return Message{
		Code:      code,
		Message:   message,
		Details:   nonNilMap(details),
		Retryable: retryable,
	}
}

// Marshal serializes env as indented JSON with a trailing newline. It validates
// the envelope first so all generated fixtures and CLI output stay parseable by
// both Go and Emacs clients.
func Marshal(env Envelope) ([]byte, error) {
	if err := Validate(env); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Unmarshal parses and validates a JSON contract envelope.
func Unmarshal(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return Envelope{}, err
	}
	if err := Validate(env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

// Validate checks the stable envelope invariants for v1.
func Validate(env Envelope) error {
	if env.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", env.SchemaVersion)
	}
	if env.Data == nil {
		return fmt.Errorf("data must be an object, not null")
	}
	if env.Warnings == nil {
		return fmt.Errorf("warnings must be an array, not null")
	}
	if env.Errors == nil {
		return fmt.Errorf("errors must be an array, not null")
	}
	for i, m := range env.Warnings {
		if err := validateMessage(m); err != nil {
			return fmt.Errorf("warnings[%d]: %w", i, err)
		}
	}
	for i, m := range env.Errors {
		if err := validateMessage(m); err != nil {
			return fmt.Errorf("errors[%d]: %w", i, err)
		}
	}
	if env.OK && len(env.Errors) != 0 {
		return fmt.Errorf("ok envelope must not include errors")
	}
	if !env.OK && len(env.Errors) == 0 {
		return fmt.Errorf("error envelope must include at least one error")
	}
	return nil
}

func validateMessage(m Message) error {
	if !IsValidCode(m.Code) {
		return fmt.Errorf("unknown code %q", m.Code)
	}
	if m.Message == "" {
		return fmt.Errorf("message must be non-empty")
	}
	if m.Details == nil {
		return fmt.Errorf("details must be an object, not null")
	}
	return nil
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func nonNilMessages(messages []Message) []Message {
	if messages == nil {
		return []Message{}
	}
	return messages
}
