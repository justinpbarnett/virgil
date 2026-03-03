package envelope

import (
	"fmt"
	"reflect"
)

// Validate checks structural invariants of an envelope.
// Returns nil if valid, or a descriptive error for the first violation found.
func Validate(env Envelope) error {
	// Required fields
	if env.Pipe == "" {
		return fmt.Errorf("envelope: Pipe is empty")
	}
	if env.Action == "" {
		return fmt.Errorf("envelope from pipe %q: Action is empty", env.Pipe)
	}
	if env.Timestamp.IsZero() {
		return fmt.Errorf("envelope from pipe %q: Timestamp is zero", env.Pipe)
	}

	// Error field consistency
	if env.Error != nil {
		if env.Error.Message == "" {
			return fmt.Errorf("envelope from pipe %q: Error.Message is empty", env.Pipe)
		}
		switch env.Error.Severity {
		case SeverityFatal, SeverityError, SeverityWarn:
			// valid
		default:
			return fmt.Errorf("envelope from pipe %q: Error.Severity %q is not one of fatal/error/warn", env.Pipe, env.Error.Severity)
		}
	}

	// ContentType and content shape consistency
	// Only validate content type if content is present and there's no error
	// (error envelopes don't require content)
	if env.Content != nil && env.Error == nil {
		switch env.ContentType {
		case ContentText, ContentList, ContentStructured, ContentBinary:
			// known type — check shape below
		case "":
			return fmt.Errorf("envelope from pipe %q: Content is set but ContentType is empty", env.Pipe)
		default:
			return fmt.Errorf("envelope from pipe %q: ContentType %q is not one of text/list/structured/binary", env.Pipe, env.ContentType)
		}

		if err := validateContentShape(env.Pipe, env.ContentType, env.Content); err != nil {
			return err
		}
	}

	return nil
}

func validateContentShape(pipe, contentType string, content any) error {
	rv := reflect.ValueOf(content)
	kind := rv.Kind()

	switch contentType {
	case ContentText:
		if kind != reflect.String {
			return fmt.Errorf("envelope from pipe %q: ContentType is %q but Content is %T", pipe, contentType, content)
		}
	case ContentList:
		if kind != reflect.Slice && kind != reflect.Array {
			return fmt.Errorf("envelope from pipe %q: ContentType is %q but Content is %T", pipe, contentType, content)
		}
	case ContentStructured:
		if kind != reflect.Map && kind != reflect.Struct {
			// Pointer to struct is also acceptable
			if kind == reflect.Ptr && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct {
				return nil
			}
			return fmt.Errorf("envelope from pipe %q: ContentType is %q but Content is %T", pipe, contentType, content)
		}
	case ContentBinary:
		if kind == reflect.String {
			return nil
		}
		if kind != reflect.Slice || rv.Type().Elem().Kind() != reflect.Uint8 {
			return fmt.Errorf("envelope from pipe %q: ContentType is %q but Content is %T (want string or []byte)", pipe, contentType, content)
		}
	}
	return nil
}
