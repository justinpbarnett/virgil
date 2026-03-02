package testutil

import "context"

// MockProvider implements bridge.Provider for tests.
type MockProvider struct {
	Response string
	Err      error
}

func (m *MockProvider) Complete(_ context.Context, _, user string) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	if m.Response != "" {
		return m.Response, nil
	}
	return "Mock response for: " + user, nil
}

// MockStreamProvider implements bridge.StreamingProvider for tests.
type MockStreamProvider struct {
	MockProvider
	Chunks []string
}

func (m *MockStreamProvider) CompleteStream(_ context.Context, _, _ string, onChunk func(string)) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	for _, c := range m.Chunks {
		onChunk(c)
	}
	return m.Response, nil
}
