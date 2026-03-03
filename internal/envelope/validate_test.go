package envelope

import (
	"strings"
	"testing"
	"time"
)

func validBase() Envelope {
	return Envelope{
		Pipe:      "test",
		Action:    "run",
		Timestamp: time.Now(),
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		env     Envelope
		wantErr string // substring of expected error, empty means valid
	}{
		// Valid cases
		{
			name: "valid text envelope",
			env: func() Envelope {
				e := validBase()
				e.Content = "hello world"
				e.ContentType = ContentText
				return e
			}(),
		},
		{
			name: "valid list envelope",
			env: func() Envelope {
				e := validBase()
				e.Content = []any{"item1", "item2"}
				e.ContentType = ContentList
				return e
			}(),
		},
		{
			name: "valid structured envelope with map",
			env: func() Envelope {
				e := validBase()
				e.Content = map[string]any{"key": "value"}
				e.ContentType = ContentStructured
				return e
			}(),
		},
		{
			name: "valid structured envelope with struct",
			env: func() Envelope {
				e := validBase()
				e.Content = struct{ Name string }{Name: "test"}
				e.ContentType = ContentStructured
				return e
			}(),
		},
		{
			name: "valid binary envelope with string",
			env: func() Envelope {
				e := validBase()
				e.Content = "binarydata"
				e.ContentType = ContentBinary
				return e
			}(),
		},
		{
			name: "valid binary envelope with bytes",
			env: func() Envelope {
				e := validBase()
				e.Content = []byte{0x01, 0x02}
				e.ContentType = ContentBinary
				return e
			}(),
		},
		{
			name: "binary ContentType with non-byte slice",
			env: func() Envelope {
				e := validBase()
				e.Content = []int{1, 2, 3}
				e.ContentType = ContentBinary
				return e
			}(),
			wantErr: `ContentType is "binary"`,
		},
		{
			name: "valid error envelope (fatal, no content)",
			env: func() Envelope {
				e := validBase()
				e.Error = FatalError("something went wrong")
				return e
			}(),
		},
		{
			name: "valid warn envelope with partial content",
			env: func() Envelope {
				e := validBase()
				e.Content = "partial result"
				e.ContentType = ContentText
				e.Error = WarnError("partial results only")
				return e
			}(),
		},
		{
			name: "side-effect only (nil content, nil error)",
			env:  validBase(),
		},
		{
			name: "nil content with ContentType set (content may be intentionally cleared)",
			env: func() Envelope {
				e := validBase()
				e.ContentType = ContentText
				return e
			}(),
		},
		{
			name: "error envelope with fatal severity and no content",
			env: func() Envelope {
				e := validBase()
				e.Error = FatalError("fatal error")
				return e
			}(),
		},
		// Invalid: required fields
		{
			name: "empty Pipe",
			env: func() Envelope {
				e := validBase()
				e.Pipe = ""
				return e
			}(),
			wantErr: "Pipe is empty",
		},
		{
			name: "empty Action",
			env: func() Envelope {
				e := validBase()
				e.Action = ""
				return e
			}(),
			wantErr: "Action is empty",
		},
		{
			name: "zero Timestamp",
			env: func() Envelope {
				e := validBase()
				e.Timestamp = time.Time{}
				return e
			}(),
			wantErr: "Timestamp is zero",
		},
		// Invalid: ContentType
		{
			name: "content set but ContentType empty",
			env: func() Envelope {
				e := validBase()
				e.Content = "hello"
				e.ContentType = ""
				return e
			}(),
			wantErr: "ContentType is empty",
		},
		{
			name: "unknown ContentType",
			env: func() Envelope {
				e := validBase()
				e.Content = "hello"
				e.ContentType = "unknown"
				return e
			}(),
			wantErr: `ContentType "unknown" is not one of`,
		},
		// Invalid: content shape mismatches
		{
			name: "ContentType list but Content is string",
			env: func() Envelope {
				e := validBase()
				e.Content = "not a list"
				e.ContentType = ContentList
				return e
			}(),
			wantErr: `ContentType is "list" but Content is string`,
		},
		{
			name: "ContentType text but Content is slice",
			env: func() Envelope {
				e := validBase()
				e.Content = []string{"a", "b"}
				e.ContentType = ContentText
				return e
			}(),
			wantErr: `ContentType is "text" but Content is []string`,
		},
		{
			name: "ContentType structured but Content is string",
			env: func() Envelope {
				e := validBase()
				e.Content = "not a map"
				e.ContentType = ContentStructured
				return e
			}(),
			wantErr: `ContentType is "structured" but Content is string`,
		},
		// Invalid: Error field consistency
		{
			name: "Error with empty Message",
			env: func() Envelope {
				e := validBase()
				e.Error = &EnvelopeError{Message: "", Severity: SeverityFatal}
				return e
			}(),
			wantErr: "Error.Message is empty",
		},
		{
			name: "Error with unknown Severity",
			env: func() Envelope {
				e := validBase()
				e.Error = &EnvelopeError{Message: "oops", Severity: "critical"}
				return e
			}(),
			wantErr: `Error.Severity "critical" is not one of`,
		},
		// Edge: JSON-deserialized content uses generic types
		{
			name: "list ContentType with []any (JSON-deserialized)",
			env: func() Envelope {
				e := validBase()
				e.Content = []any{"item1", "item2"}
				e.ContentType = ContentList
				return e
			}(),
		},
		{
			name: "structured ContentType with map[string]any (JSON-deserialized)",
			env: func() Envelope {
				e := validBase()
				e.Content = map[string]any{"key": "value"}
				e.ContentType = ContentStructured
				return e
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.env)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected nil error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
			}
		})
	}
}
