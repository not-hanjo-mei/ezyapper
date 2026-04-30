package bot

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/plugin"

	"github.com/bwmarrin/discordgo"
)

func TestCloseAllClosers_Nil(t *testing.T) {
	closeAllClosers(nil)
}

func TestCloseAllClosers_Empty(t *testing.T) {
	closeAllClosers([]io.Closer{})
}

func TestCloseAllClosers_AllNil(t *testing.T) {
	closeAllClosers([]io.Closer{nil, nil})
}

type testCloser struct {
	closed  bool
	failErr error
}

func (c *testCloser) Close() error {
	c.closed = true
	return c.failErr
}

func TestCloseAllClosers_Success(t *testing.T) {
	c1 := &testCloser{}
	c2 := &testCloser{}
	closeAllClosers([]io.Closer{c1, c2})
	if !c1.closed || !c2.closed {
		t.Fatal("expected both closers to be closed")
	}
}

func TestCloseAllClosers_WithError(t *testing.T) {
	c1 := &testCloser{failErr: io.ErrUnexpectedEOF}
	closeAllClosers([]io.Closer{c1})
	if !c1.closed {
		t.Fatal("expected closer to be called even on error")
	}
}

func TestCleanupUploadedTempFiles_Empty(t *testing.T) {
	cleanupUploadedTempFiles(nil)
	cleanupUploadedTempFiles([]string{})
}

func TestCleanupUploadedTempFiles_EmptyPath(t *testing.T) {
	cleanupUploadedTempFiles([]string{"", "  "})
}

func TestCleanupUploadedTempFiles_NonExistent(t *testing.T) {
	cleanupUploadedTempFiles([]string{filepath.Join(t.TempDir(), "nonexistent.txt")})
}

func TestCleanupUploadedTempFiles_RealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	cleanupUploadedTempFiles([]string{path})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected file to be removed")
	}
}

func TestPluginFilesToLocalUploadFiles_Nil(t *testing.T) {
	result := pluginFilesToLocalUploadFiles(nil)
	if result != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestPluginFilesToLocalUploadFiles_Empty(t *testing.T) {
	result := pluginFilesToLocalUploadFiles([]plugin.LocalFile{})
	if result != nil {
		t.Fatal("expected nil for empty slice")
	}
}

func TestPluginFilesToLocalUploadFiles_Conversion(t *testing.T) {
	input := []plugin.LocalFile{
		{Path: "test.txt", Name: "upload.txt", ContentType: "text/plain", Data: []byte("hello"), DeleteAfterUpload: true},
	}
	result := pluginFilesToLocalUploadFiles(input)
	if len(result) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result))
	}
	if result[0].Path != "test.txt" || result[0].Name != "upload.txt" {
		t.Fatalf("unexpected values: %+v", result[0])
	}
	if result[0].ContentType != "text/plain" {
		t.Fatalf("expected text/plain, got %s", result[0].ContentType)
	}
	if !result[0].DeleteAfterUpload {
		t.Fatal("expected DeleteAfterUpload=true")
	}
}

func TestBuildDiscordFiles_Empty(t *testing.T) {
	files, closers, cleanup, err := buildDiscordFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if files != nil || closers != nil || cleanup != nil {
		t.Fatal("expected all nil for empty input")
	}
}

func TestBuildDiscordFiles_FromData(t *testing.T) {
	localFiles := []localUploadFile{
		{Data: []byte("hello"), Name: "test.txt", ContentType: "text/plain"},
	}
	files, closers, cleanup, err := buildDiscordFiles(localFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Name != "test.txt" {
		t.Fatalf("expected test.txt, got %s", files[0].Name)
	}
	if files[0].ContentType != "text/plain" {
		t.Fatalf("expected text/plain, got %s", files[0].ContentType)
	}
	if len(closers) != 0 {
		t.Fatalf("expected empty closers for data-based files, got %d", len(closers))
	}
	defer closeAllClosers(closers)
	defer cleanupUploadedTempFiles(cleanup)

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(files[0].Reader); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello" {
		t.Fatalf("expected 'hello', got %q", buf.String())
	}
}

func TestBuildDiscordFiles_FromData_DefaultName(t *testing.T) {
	localFiles := []localUploadFile{
		{Data: []byte("content")},
	}
	files, _, _, err := buildDiscordFiles(localFiles)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Name != "upload.bin" {
		t.Fatalf("expected upload.bin, got %s", files[0].Name)
	}
}

func TestBuildDiscordFiles_FromData_DefaultNameFromPath(t *testing.T) {
	localFiles := []localUploadFile{
		{Data: []byte("content"), Path: "/tmp/realname.png"},
	}
	files, _, _, err := buildDiscordFiles(localFiles)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Name != "realname.png" {
		t.Fatalf("expected realname.png, got %s", files[0].Name)
	}
}

func TestBuildDiscordFiles_FromPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	localFiles := []localUploadFile{
		{Path: path, Name: "renamed.txt"},
	}
	files, closers, cleanup, err := buildDiscordFiles(localFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAllClosers(closers)
	defer cleanupUploadedTempFiles(cleanup)

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Name != "renamed.txt" {
		t.Fatalf("expected renamed.txt, got %s", files[0].Name)
	}
	if len(closers) != 1 {
		t.Fatalf("expected 1 closer, got %d", len(closers))
	}
}

func TestBuildDiscordFiles_MissingPath(t *testing.T) {
	localFiles := []localUploadFile{
		{Path: "/nonexistent/file.txt"},
	}
	_, _, _, err := buildDiscordFiles(localFiles)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "open upload file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildDiscordFiles_NoDataNoPath(t *testing.T) {
	localFiles := []localUploadFile{
		{Name: "orphan.txt"},
	}
	_, _, _, err := buildDiscordFiles(localFiles)
	if err == nil {
		t.Fatal("expected error for file with neither data nor path")
	}
	if !strings.Contains(err.Error(), "requires either data or path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildDiscordFiles_DeleteAfterUpload(t *testing.T) {
	localFiles := []localUploadFile{
		{Data: []byte("hello"), Path: "/tmp/test.bin", Name: "test.bin", DeleteAfterUpload: true},
	}
	_, _, cleanup, err := buildDiscordFiles(localFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleanup) != 1 {
		t.Fatalf("expected 1 cleanup path, got %d", len(cleanup))
	}
}

func TestBuildBotMessage(t *testing.T) {
	sentMsg := &discordgo.Message{
		ID:        "msg-1",
		ChannelID: "ch-1",
		Content:   "hello world",
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			GuildID: "guild-1",
		},
	}

	s := &discordgo.Session{}
	s.State = discordgo.NewState()
	s.State.User = &discordgo.User{
		ID:       "bot-1",
		Username: "MyBot",
	}

	b := &Bot{}
	msg := b.buildBotMessage(sentMsg, m, s)

	if msg.ID != "msg-1" {
		t.Fatalf("expected msg-1, got %s", msg.ID)
	}
	if msg.AuthorID != "bot-1" {
		t.Fatalf("expected bot-1, got %s", msg.AuthorID)
	}
	if msg.Username != "MyBot" {
		t.Fatalf("expected MyBot, got %s", msg.Username)
	}
	if !msg.IsBot {
		t.Fatal("expected IsBot=true")
	}
	if msg.GuildID != "guild-1" {
		t.Fatalf("expected guild-1, got %s", msg.GuildID)
	}
	if msg.Content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", msg.Content)
	}
}
