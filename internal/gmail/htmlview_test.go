package gmail

import (
	"encoding/base64"
	"strings"
	"testing"
)

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func TestHTMLPageStripsActiveContent(t *testing.T) {
	src := `<html><head><meta http-equiv="refresh" content="0;url=https://evil.example">` +
		`<base href="https://evil.example/"><script>alert(1)</script></head>` +
		`<body onload="x()"><p onclick="y()">Hi</p><iframe src="https://evil.example"></iframe>` +
		`<a href="javascript:z()">bad</a><a href="https://ok.example">ok</a><object data="x"></object></body></html>`
	page := HTMLPage(src, PageHeader{Subject: "S"}, nil, false)
	for _, banned := range []string{"<script", "alert(1)", "onload", "onclick", "<iframe", "javascript:", "<base", "refresh", "<object"} {
		if strings.Contains(page, banned) {
			t.Errorf("page still contains %q", banned)
		}
	}
	if !strings.Contains(page, `href="https://ok.example"`) || !strings.Contains(page, `rel="noopener noreferrer"`) {
		t.Errorf("safe link lost or not hardened:\n%s", page)
	}
	if !strings.HasPrefix(page, "<!doctype html>") {
		t.Errorf("missing doctype")
	}
}

func TestHTMLPageBlocksRemoteByDefault(t *testing.T) {
	src := `<p><img src="https://track.example/pixel.gif"></p>`
	blocked := HTMLPage(src, PageHeader{}, nil, false)
	if !strings.Contains(blocked, `img-src data:;`) || !strings.Contains(blocked, "Remote images are blocked") {
		t.Errorf("remote images not blocked:\n%s", blocked)
	}
	allowed := HTMLPage(src, PageHeader{}, nil, true)
	if !strings.Contains(allowed, `img-src data: https: http:;`) || strings.Contains(allowed, "Remote images are blocked") {
		t.Errorf("remote images not allowed:\n%s", allowed)
	}
	// The policy must come first in <head>, before anything it governs.
	if i, j := strings.Index(blocked, "Content-Security-Policy"), strings.Index(blocked, "<p>"); i < 0 || i > j {
		t.Errorf("CSP is not ahead of the content")
	}
}

func TestHTMLPageInlinesCIDImages(t *testing.T) {
	src := `<img src="cid:logo@x"><img src="CID:missing@x">`
	page := HTMLPage(src, PageHeader{}, map[string]Image{"logo@x": {MimeType: "image/png", Data: []byte("PNG")}}, false)
	if !strings.Contains(page, `src="data:image/png;base64,`+base64.StdEncoding.EncodeToString([]byte("PNG"))+`"`) {
		t.Errorf("cid image not inlined:\n%s", page)
	}
	if !strings.Contains(page, `src="CID:missing@x"`) {
		t.Errorf("unknown cid should be left alone:\n%s", page)
	}
}

func TestHTMLPageEscapesHeader(t *testing.T) {
	page := HTMLPage(`<p>body</p>`, PageHeader{Subject: `<script>x</script> & co`, From: `"A" <a@x.example>`}, nil, false)
	if strings.Contains(page, "<script>x</script>") {
		t.Errorf("header not escaped:\n%s", page)
	}
	if !strings.Contains(page, "&lt;script&gt;x&lt;/script&gt; &amp; co") || !strings.Contains(page, "&lt;a@x.example&gt;") {
		t.Errorf("header text missing:\n%s", page)
	}
}

func part(mime, data string, headers ...Header) Part {
	p := Part{MimeType: mime, Headers: headers}
	p.Body.Data, p.Body.Size = b64(data), len(data)
	return p
}

func TestHTMLBodyAndInlineImages(t *testing.T) {
	pdf := part("application/pdf", "", Header{Name: "Content-ID", Value: "<doc@x>"})
	pdf.Filename = "a.pdf"
	m := Message{ID: "m1", Payload: Part{MimeType: "multipart/related", Parts: []Part{
		part("text/html", "<p>hello</p>"),
		part("image/png", "PNG", Header{Name: "Content-ID", Value: "<logo@x>"}),
		pdf,
	}}}
	if s, ok := m.HTMLBody(); !ok || s != "<p>hello</p>" {
		t.Errorf("HTMLBody = %q, %v", s, ok)
	}
	imgs := m.InlineImages()
	if len(imgs) != 1 || imgs[0].ContentID != "logo@x" || imgs[0].MessageID != "m1" {
		t.Errorf("InlineImages = %+v", imgs)
	}
	if _, ok := (Message{Payload: part("text/plain", "hi")}).HTMLBody(); ok {
		t.Errorf("plain-text message reported an HTML body")
	}
}
