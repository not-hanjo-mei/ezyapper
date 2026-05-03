package bot

import "testing"

func TestHandleEditedMessagePhaseReceived(t *testing.T) {
	b := &Bot{}
	cancelCalled := false
	pm := &ProcessingMessage{
		MessageID:       "msg-1",
		Content:         "old",
		OriginalContent: "old",
		Phase:           PhaseReceived,
		CancelFunc: func() {
			cancelCalled = true
		},
	}

	reprocess := b.handleEditedMessage(pm, "new")
	if reprocess {
		t.Fatal("expected no reprocess in PhaseReceived")
	}
	if cancelCalled {
		t.Fatal("expected cancel not to be called in PhaseReceived")
	}
	if pm.GetContent() != "new" {
		t.Fatalf("expected updated content, got %q", pm.GetContent())
	}
	if pm.GetEditCount() != 1 {
		t.Fatalf("expected edit count 1, got %d", pm.GetEditCount())
	}
}

func TestHandleEditedMessagePhaseDeciding(t *testing.T) {
	b := &Bot{}
	cancelCalled := false
	pm := &ProcessingMessage{
		MessageID:       "msg-1",
		Content:         "old",
		OriginalContent: "old",
		Phase:           PhaseDeciding,
		CancelFunc: func() {
			cancelCalled = true
		},
	}

	reprocess := b.handleEditedMessage(pm, "new")
	if !reprocess {
		t.Fatal("expected reprocess in PhaseDeciding")
	}
	if !cancelCalled {
		t.Fatal("expected cancel to be called in PhaseDeciding")
	}
	if pm.GetContent() != "new" {
		t.Fatalf("expected updated content, got %q", pm.GetContent())
	}
}

func TestHandleEditedMessagePhaseSending(t *testing.T) {
	b := &Bot{}
	cancelCalled := false
	pm := &ProcessingMessage{
		MessageID:       "msg-1",
		Content:         "old",
		OriginalContent: "old",
		Phase:           PhaseSending,
		CancelFunc: func() {
			cancelCalled = true
		},
	}

	reprocess := b.handleEditedMessage(pm, "new")
	if reprocess {
		t.Fatal("expected no reprocess in PhaseSending")
	}
	if !cancelCalled {
		t.Fatal("expected cancel to be called in PhaseSending")
	}
	if pm.GetContent() != "new" {
		t.Fatalf("expected updated content, got %q", pm.GetContent())
	}
}

func TestHandleEditedMessageNilMessage(t *testing.T) {
	b := &Bot{}
	if b.handleEditedMessage(nil, "new") {
		t.Fatal("expected false for nil processing message")
	}
}

func TestRemoveProcessingMessageIfMatch_DoesNotDeleteWrongPointer(t *testing.T) {
	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}

	pm1 := b.registerProcessingMessage("msg-race", "chan-1", "user-1", "original")
	if pm1 == nil {
		t.Fatal("registerProcessingMessage returned nil")
	}

	// Simulate edit: old goroutine's cleanup calls clearProcessingMessage with pm1
	// but a new PM (pm2) has already been registered with the same messageID.
	pm2 := b.registerProcessingMessage("msg-race", "chan-1", "user-1", "edited")
	if pm2 == nil {
		t.Fatal("second registerProcessingMessage returned nil")
	}
	if pm1 == pm2 {
		t.Fatal("expected different pointers for old and new ProcessingMessage")
	}

	// Old goroutine cleanup runs: must NOT delete pm2.
	b.clearProcessingMessage(pm1, "msg-race")

	if _, exists := b.processingMessages["msg-race"]; !exists {
		t.Error("pm2 was incorrectly deleted by old goroutine's clearProcessingMessage")
	}

	// Verify the stored pointer is still pm2.
	p := b.getProcessingMessage("msg-race")
	if p != pm2 {
		t.Error("stored pointer is not pm2 after old goroutine cleanup")
	}

	// New goroutine cleanup runs: should delete pm2.
	b.clearProcessingMessage(pm2, "msg-race")
	if _, exists := b.processingMessages["msg-race"]; exists {
		t.Error("pm2 was not deleted by its own clearProcessingMessage")
	}
}

func TestRemoveProcessingMessageIfMatch_NilPointer(t *testing.T) {
	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}

	b.registerProcessingMessage("msg-nil", "chan-1", "user-1", "content")

	// clearProcessingMessage with nil pm should be a no-op.
	b.clearProcessingMessage(nil, "msg-nil")

	if _, exists := b.processingMessages["msg-nil"]; !exists {
		t.Error("PM should still exist when clearProcessingMessage called with nil pm")
	}
}
