package mail

import (
	"context"
	"fmt"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

type mockMailClient struct {
	messages    []Message
	message     *Message
	sentID      string
	err         error
	lastTo      string
	lastCC      string
	lastSubject string
	lastBody    string
	lastThread  string
	lastMsgID   string
	lastAdd     []string
	lastRemove  []string
}

func (m *mockMailClient) ListMessages(_ context.Context, _ string, _ int) ([]Message, error) {
	return m.messages, m.err
}

func (m *mockMailClient) GetMessage(_ context.Context, messageID string) (*Message, error) {
	m.lastMsgID = messageID
	if m.err != nil {
		return nil, m.err
	}
	return m.message, nil
}

func (m *mockMailClient) SearchMessages(_ context.Context, _ string, _ int) ([]Message, error) {
	return m.messages, m.err
}

func (m *mockMailClient) SendMessage(_ context.Context, to, cc, subject, body, threadID string) (string, error) {
	m.lastTo = to
	m.lastCC = cc
	m.lastSubject = subject
	m.lastBody = body
	m.lastThread = threadID
	return m.sentID, m.err
}

func (m *mockMailClient) ModifyLabels(_ context.Context, messageID string, addLabels, removeLabels []string) error {
	m.lastMsgID = messageID
	m.lastAdd = addLabels
	m.lastRemove = removeLabels
	return m.err
}

func (m *mockMailClient) TrashMessage(_ context.Context, messageID string) error {
	m.lastMsgID = messageID
	return m.err
}

func TestListMessages(t *testing.T) {
	client := &mockMailClient{
		messages: []Message{
			{ID: "1", From: "alice@test.com", Subject: "Hello", Date: "Mon, 1 Jan"},
			{ID: "2", From: "bob@test.com", Subject: "Meeting", Date: "Mon, 1 Jan"},
		},
	}

	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"action": "list"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
	msgs, ok := result.Content.([]Message)
	if !ok {
		t.Fatalf("expected []Message, got %T", result.Content)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

func TestListMessagesEmpty(t *testing.T) {
	client := &mockMailClient{messages: []Message{}}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	msgs, ok := result.Content.([]Message)
	if !ok {
		t.Fatalf("expected []Message, got %T", result.Content)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestListMessagesCustomLabel(t *testing.T) {
	client := &mockMailClient{messages: []Message{}}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "label": "SENT"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Action != "list" {
		t.Errorf("expected action=list, got %s", result.Action)
	}
}

func TestReadMessage(t *testing.T) {
	client := &mockMailClient{
		message: &Message{
			ID: "msg1", From: "alice@test.com", Subject: "Hello",
			Body: "Hi there", Date: "Mon, 1 Jan",
		},
	}

	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"action": "read", "message_id": "msg1"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
	msg, ok := result.Content.(*Message)
	if !ok {
		t.Fatalf("expected *Message, got %T", result.Content)
	}
	if msg.ID != "msg1" {
		t.Errorf("expected id=msg1, got %s", msg.ID)
	}
}

func TestReadMessageMissingID(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read"})

	testutil.AssertFatalError(t, result)
}

func TestSearchMessages(t *testing.T) {
	client := &mockMailClient{
		messages: []Message{
			{ID: "1", From: "alice@test.com", Subject: "Invoice"},
		},
	}

	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"action": "search", "query": "invoice"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
}

func TestSearchMessagesEmptyQuery(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search"})

	testutil.AssertFatalError(t, result)
}

func TestSendMessage(t *testing.T) {
	client := &mockMailClient{sentID: "sent123"}
	handler := NewHandler(client, nil)

	input := envelope.New("input", "test")
	input.Content = "Hello, this is the email body."
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{
		"action":  "send",
		"to":      "bob@test.com",
		"subject": "Test Subject",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
	if client.lastTo != "bob@test.com" {
		t.Errorf("expected to=bob@test.com, got %s", client.lastTo)
	}
	if client.lastSubject != "Test Subject" {
		t.Errorf("expected subject=Test Subject, got %s", client.lastSubject)
	}
	if client.lastBody != "Hello, this is the email body." {
		t.Errorf("expected body match, got %s", client.lastBody)
	}
}

func TestSendMessageMissingTo(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	input.Content = "body"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "send"})

	testutil.AssertFatalError(t, result)
}

func TestSendMessageEmptyBody(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "send", "to": "bob@test.com"})

	testutil.AssertFatalError(t, result)
}

func TestSendMessageWithThread(t *testing.T) {
	client := &mockMailClient{sentID: "reply1"}
	handler := NewHandler(client, nil)

	input := envelope.New("input", "test")
	input.Content = "Reply body"
	input.ContentType = envelope.ContentText

	handler(input, map[string]string{
		"action":    "send",
		"to":        "bob@test.com",
		"subject":   "Re: Test",
		"thread_id": "thread123",
	})

	if client.lastThread != "thread123" {
		t.Errorf("expected thread_id=thread123, got %s", client.lastThread)
	}
}

func TestArchiveMessage(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "archive", "message_id": "msg1"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if client.lastMsgID != "msg1" {
		t.Errorf("expected message_id=msg1, got %s", client.lastMsgID)
	}
	if len(client.lastRemove) != 1 || client.lastRemove[0] != "INBOX" {
		t.Errorf("expected remove=[INBOX], got %v", client.lastRemove)
	}
}

func TestArchiveMessageMissingID(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "archive"})

	testutil.AssertFatalError(t, result)
}

func TestLabelMessage(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{
		"action":     "label",
		"message_id": "msg1",
		"label":      "IMPORTANT",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if client.lastMsgID != "msg1" {
		t.Errorf("expected message_id=msg1, got %s", client.lastMsgID)
	}
	if len(client.lastAdd) != 1 || client.lastAdd[0] != "IMPORTANT" {
		t.Errorf("expected add=[IMPORTANT], got %v", client.lastAdd)
	}
}

func TestLabelMessageMissingID(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "label", "label": "IMPORTANT"})

	testutil.AssertFatalError(t, result)
}

func TestLabelMessageMissingLabel(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "label", "message_id": "msg1"})

	testutil.AssertFatalError(t, result)
}

func TestTrashMessage(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "trash", "message_id": "msg1"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if client.lastMsgID != "msg1" {
		t.Errorf("expected message_id=msg1, got %s", client.lastMsgID)
	}
}

func TestTrashMessageMissingID(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "trash"})

	testutil.AssertFatalError(t, result)
}

func TestUnknownAction(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "explode"})

	testutil.AssertFatalError(t, result)
}

func TestNilClient(t *testing.T) {
	handler := NewHandler(nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	testutil.AssertFatalError(t, result)
}

func TestAPIError(t *testing.T) {
	client := &mockMailClient{err: fmt.Errorf("API rate limited")}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list"})

	testutil.AssertFatalError(t, result)
}

func TestAPIErrorOnRead(t *testing.T) {
	client := &mockMailClient{err: fmt.Errorf("not found")}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "message_id": "msg1"})

	testutil.AssertFatalError(t, result)
}

func TestAPIErrorOnSearch(t *testing.T) {
	client := &mockMailClient{err: fmt.Errorf("server error")}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search", "query": "test"})

	testutil.AssertFatalError(t, result)
}

func TestAPIErrorOnSend(t *testing.T) {
	client := &mockMailClient{err: fmt.Errorf("send failed")}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	input.Content = "body"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "send", "to": "bob@test.com"})

	testutil.AssertFatalError(t, result)
}

func TestAPIErrorOnArchive(t *testing.T) {
	client := &mockMailClient{err: fmt.Errorf("modify failed")}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "archive", "message_id": "msg1"})

	testutil.AssertFatalError(t, result)
}

func TestAPIErrorOnLabel(t *testing.T) {
	client := &mockMailClient{err: fmt.Errorf("modify failed")}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "label", "message_id": "msg1", "label": "X"})

	testutil.AssertFatalError(t, result)
}

func TestAPIErrorOnTrash(t *testing.T) {
	client := &mockMailClient{err: fmt.Errorf("trash failed")}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "trash", "message_id": "msg1"})

	testutil.AssertFatalError(t, result)
}

func TestTimeoutErrorIsRetryable(t *testing.T) {
	client := &mockMailClient{err: context.DeadlineExceeded}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list"})

	if result.Error == nil {
		t.Fatal("expected error")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable=true for timeout error")
	}
}

func TestDefaultAction(t *testing.T) {
	client := &mockMailClient{messages: []Message{}}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	if result.Action != "list" {
		t.Errorf("expected action=list, got %s", result.Action)
	}
}

func TestDefaultLimit(t *testing.T) {
	client := &mockMailClient{messages: []Message{}}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}

func TestEnvelopeComplianceList(t *testing.T) {
	client := &mockMailClient{
		messages: []Message{{ID: "1", From: "a@b.com", Subject: "Hi"}},
	}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list"})

	testutil.AssertEnvelope(t, result, "mail", "list")
	if result.Args == nil {
		t.Error("expected args to be non-nil")
	}
	if result.Content == nil {
		t.Error("expected content to be non-nil")
	}
	if result.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
}

func TestEnvelopeComplianceRead(t *testing.T) {
	client := &mockMailClient{
		message: &Message{ID: "msg1", From: "a@b.com", Subject: "Hi", Body: "body"},
	}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "message_id": "msg1"})

	testutil.AssertEnvelope(t, result, "mail", "read")
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

func TestEnvelopeComplianceSend(t *testing.T) {
	client := &mockMailClient{sentID: "sent1"}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	input.Content = "body text"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "send", "to": "a@b.com"})

	testutil.AssertEnvelope(t, result, "mail", "send")
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

func TestEnvelopeComplianceArchive(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "archive", "message_id": "msg1"})

	testutil.AssertEnvelope(t, result, "mail", "archive")
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

func TestEnvelopeComplianceLabel(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "label", "message_id": "msg1", "label": "X"})

	testutil.AssertEnvelope(t, result, "mail", "label")
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

func TestEnvelopeComplianceTrash(t *testing.T) {
	client := &mockMailClient{}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "trash", "message_id": "msg1"})

	testutil.AssertEnvelope(t, result, "mail", "trash")
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

func TestSendMessageWithCC(t *testing.T) {
	client := &mockMailClient{sentID: "sent1"}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	input.Content = "body"
	input.ContentType = envelope.ContentText

	handler(input, map[string]string{
		"action":  "send",
		"to":      "bob@test.com",
		"cc":      "cc@test.com",
		"subject": "Test",
	})

	if client.lastCC != "cc@test.com" {
		t.Errorf("expected cc=cc@test.com, got %s", client.lastCC)
	}
}
