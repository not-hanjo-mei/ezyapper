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
