package db

// NullStr returns nil for empty strings, making SQL INSERTs store NULL.
func NullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
