package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// dsml builds a DeepSeek-style marker tag, e.g. tag("calls") -> "<|<pipe>calls...".
func dsml(suffix string) string {
	return "<" + "\uFF5C" + suffix
}

func TestSanitizeSideQuestionContent_StripsToolCallMarkup(t *testing.T) {
	leaked := dsml("calls>\n") + dsml("invoke name=\"exec\">echo hi\n") + dsml("calls>")
	if got := sanitizeSideQuestionContent("Sure! Here is the answer.\n" + leaked); got != "Sure! Here is the answer." {
		t.Fatalf("sanitize = %q, want clean prefix only", got)
	}
}

func TestSanitizeSideQuestionContent_MarkupOnlyBecomesNotice(t *testing.T) {
	got := sanitizeSideQuestionContent(dsml("calls>\n") + dsml("invoke/>\n") + dsml("calls>"))
	if got != sideQuestionToolCallNotice {
		t.Fatalf("sanitize = %q, want tool-call notice", got)
	}
}

func TestSanitizeSideQuestionContent_PlainTextUntouched(t *testing.T) {
	const plain = "The exec tool is disabled; use /btw for direct questions."
	if got := sanitizeSideQuestionContent(plain); got != plain {
		t.Fatalf("sanitize = %q, want unchanged %q", got, plain)
	}
}

func TestSideQuestionResponseContent_SanitizesContent(t *testing.T) {
	resp := &providers.LLMResponse{Content: "Answer." + dsml("calls>") + "noise"}
	if got := sideQuestionResponseContent(resp); got != "Answer." {
		t.Fatalf("sideQuestionResponseContent = %q, want %q", got, "Answer.")
	}
}

func TestInferMediaType(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		contentType string
		want        string
	}{
		{
			name:        "png content type",
			filename:    "diagram",
			contentType: "image/png",
			want:        "image",
		},
		{
			name:        "jpeg extension fallback",
			filename:    "photo.JPG",
			contentType: "",
			want:        "image",
		},
		{
			name:        "svg content type is file",
			filename:    "diagram",
			contentType: "image/svg+xml",
			want:        "file",
		},
		{
			name:        "svg content type with parameters is file",
			filename:    "diagram",
			contentType: "image/svg+xml; charset=utf-8",
			want:        "file",
		},
		{
			name:        "svg extension fallback is file",
			filename:    "diagram.SVG",
			contentType: "",
			want:        "file",
		},
		{
			name:        "audio content type",
			filename:    "voice",
			contentType: "audio/ogg",
			want:        "audio",
		},
		{
			name:        "ogg application content type",
			filename:    "voice.ogg",
			contentType: "application/ogg",
			want:        "audio",
		},
		{
			name:        "video extension fallback",
			filename:    "clip.MP4",
			contentType: "",
			want:        "video",
		},
		{
			name:        "unknown type",
			filename:    "archive.bin",
			contentType: "application/octet-stream",
			want:        "file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferMediaType(tt.filename, tt.contentType)
			if got != tt.want {
				t.Fatalf("inferMediaType(%q, %q) = %q, want %q", tt.filename, tt.contentType, got, tt.want)
			}
		})
	}
}
