package main

import (
	"net/url"
	"testing"
)

func TestAttachmentAccessNavigationOnlyAcceptsExactSameOriginAttachmentLinks(t *testing.T) {
	base := "http://127.0.0.1:8787/"
	query := url.Values{"chat": {"chat-12345678"}, "path": {"/Users/example/合同.docx"}}.Encode()
	valid := base + "__desktop/file-access?" + query
	target, ok := attachmentAccessNavigation(base, valid)
	if !ok || target.ChatID != "chat-12345678" || target.Path != "/Users/example/合同.docx" {
		t.Fatalf("valid attachment navigation rejected: %+v %v", target, ok)
	}
	for _, bad := range []string{
		"http://127.0.0.1:8788/__desktop/file-access?" + query,
		"https://127.0.0.1:8787/__desktop/file-access?" + query,
		"http://user@127.0.0.1:8787/__desktop/file-access?" + query,
		"https://evil.test/__desktop/file-access?" + query,
		base + "__desktop/%66ile-access?" + query,
		base + "__desktop/file-access/?" + query,
		valid + "#file",
		valid + "&chat=chat-12345678",
		valid + "&path=/Users/example/other.docx",
		valid + "&extra=1",
		base + "__desktop/file-access?chat=bad&path=/one",
		base + "__desktop/file-access?chat=chat-12345678&path=relative.docx",
		base + "__desktop/file-access?chat=chat-12345678&path=/one%00",
		base + "__desktop/file-access?chat=chat-12345678&path=/one%0a",
		"/__desktop/file-access?" + query,
	} {
		if _, ok := attachmentAccessNavigation(base, bad); ok {
			t.Errorf("unsafe attachment navigation accepted: %q", bad)
		}
	}
}

func TestAttachmentChooserRequiresExactlyTheReferencedFile(t *testing.T) {
	for _, tc := range []struct {
		paths []string
		want  bool
	}{
		{[]string{"/Users/example/合同.docx"}, true},
		{nil, false},
		{[]string{"/Users/example"}, false},
		{[]string{"/Users/example/other.docx"}, false},
		{[]string{"/Users/example/合同.docx", "/Users/example/other.docx"}, false},
	} {
		if got := selectedAttachmentPath("/Users/example/合同.docx", tc.paths); got != tc.want {
			t.Errorf("selected paths %q = %v, want %v", tc.paths, got, tc.want)
		}
	}
}
