package cli

import "testing"

func TestUpdateMarkdownIntegrationSegmentWritesEmptyFile(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n"
	got := updateMarkdownIntegrationSegment("", segment)
	if got != segment {
		t.Fatalf("updated content = %q, want %q", got, segment)
	}
}

func TestUpdateMarkdownIntegrationSegmentInsertsAfterTitle(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nThis is it for Aracne Project Integration\n"
	existing := "# CLAUDE.md\n\nUser instructions stay here.\n"
	want := "# CLAUDE.md\n\n# Aracne Project Integration\n\nnew\n\nThis is it for Aracne Project Integration\n\nUser instructions stay here.\n"

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}

func TestUpdateMarkdownIntegrationSegmentInsertsAfterFrontmatterAndTitle(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nThis is it for Aracne Project Integration\n"
	existing := "---\nname: docs\n---\n\n# AGENTS.md\n\nUser instructions stay here.\n"
	want := "---\nname: docs\n---\n\n# AGENTS.md\n\n# Aracne Project Integration\n\nnew\n\nThis is it for Aracne Project Integration\n\nUser instructions stay here.\n"

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}

func TestUpdateMarkdownIntegrationSegmentReplacesOnlyLTPBlock(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n"
	existing := "# CLAUDE.md\n\nBefore.\n\n# Aracne Project Integration\n\nold\n\nGood Luck in your task.\n\nAfter.\n"
	want := "# CLAUDE.md\n\nBefore.\n\n# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n\nAfter.\n"

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}

func TestUpdateMarkdownIntegrationSegmentPreservesCRLF(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nThis is it for Aracne Project Integration\n"
	existing := "# CLAUDE.md\r\n\r\nUser instructions stay here.\r\n"
	want := "# CLAUDE.md\r\n\r\n# Aracne Project Integration\r\n\r\nnew\r\n\r\nThis is it for Aracne Project Integration\r\n\r\nUser instructions stay here.\r\n"

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}
