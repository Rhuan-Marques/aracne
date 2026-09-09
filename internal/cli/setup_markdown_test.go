package cli

import "testing"

func TestUpdateMarkdownIntegrationSegmentWritesEmptyFile(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n"
	got := updateMarkdownIntegrationSegment("", segment)
	if got != segment {
		t.Fatalf("updated content = %q, want %q", got, segment)
	}
}

// A FIRST-TIME insertion APPENDS. It used to land after the leading headings, which for the
// overwhelmingly common shape -- an H1 title followed by prose -- dropped a second H1 between
// the title and the body it introduces: the author's paragraphs then read as the body of
// aracne's block and their `##` sections became subsections of it. The block is found again by
// its heading, so its position is free, and the end of the file is the only place that cannot
// orphan someone else's content.
func TestUpdateMarkdownIntegrationSegmentAppendsBelowTheDocument(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nThis is it for Aracne Project Integration\n"
	existing := "# CLAUDE.md\n\nUser instructions stay here.\n"
	want := "# CLAUDE.md\n\nUser instructions stay here.\n\n" + segment

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}

func TestUpdateMarkdownIntegrationSegmentKeepsFrontmatterAndTitleTogether(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nThis is it for Aracne Project Integration\n"
	existing := "---\nname: docs\n---\n\n# AGENTS.md\n\nUser instructions stay here.\n"
	want := existing + "\n" + segment

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}

// A block already present is replaced WHERE IT IS, so an existing install does not migrate to
// the bottom of the file and re-order a document someone has since edited around.
func TestUpdateMarkdownIntegrationSegmentReplacesOnlyLTPBlock(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n"
	existing := "# CLAUDE.md\n\nBefore.\n\n# Aracne Project Integration\n\nold\n\nGood Luck in your task.\n\nAfter.\n"
	want := "# CLAUDE.md\n\nBefore.\n\n# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n\nAfter.\n"

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}

// A block whose closing line the reader edited away is still aracne's, and is replaced up to
// the next top-level heading rather than having a second copy prepended above it.
func TestUpdateMarkdownIntegrationSegmentReplacesABlockMissingItsClosingLine(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n"
	existing := "# CLAUDE.md\n\nBefore.\n\n# Aracne Project Integration\n\nold, closing line deleted\n\n# Mine\n\nAfter.\n"
	want := "# CLAUDE.md\n\nBefore.\n\n" + segment + "# Mine\n\nAfter.\n"

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}

func TestUpdateMarkdownIntegrationSegmentPreservesCRLF(t *testing.T) {
	segment := "# Aracne Project Integration\n\nnew\n\nThis is it for Aracne Project Integration\n"
	existing := "# CLAUDE.md\r\n\r\nUser instructions stay here.\r\n"
	want := "# CLAUDE.md\r\n\r\nUser instructions stay here.\r\n\r\n" +
		"# Aracne Project Integration\r\n\r\nnew\r\n\r\nThis is it for Aracne Project Integration\r\n"

	got := updateMarkdownIntegrationSegment(existing, segment)
	if got != want {
		t.Fatalf("updated content = %q, want %q", got, want)
	}
}
