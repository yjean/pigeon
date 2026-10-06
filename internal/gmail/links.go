package gmail

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Link is a link found in a message.
type Link struct {
	Text string // anchor text ("" for bare URLs)
	URL  string
}

// SafeURL reports whether a link may be handed to the OS opener. Email
// content is untrusted: only web and mail links, never file:// or app schemes.
func SafeURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != "" || u.Path != ""
	}
	return false
}

var bareURL = regexp.MustCompile(`https?://[^\s<>"'(){}\[\]]+`)

// Links returns the message's links: anchors of the HTML part (their real
// targets) plus bare URLs of the text part, deduplicated, in reading order.
func (m Message) Links() []Link {
	var out []Link
	seen := map[string]bool{}
	add := func(l Link) {
		l.URL = strings.TrimSpace(l.URL)
		if seen[l.URL] || !SafeURL(l.URL) {
			return
		}
		seen[l.URL] = true
		out = append(out, l)
	}
	if p := findPart(m.Payload, "text/html"); p != nil {
		for _, l := range HTMLLinks(decodePart(p)) {
			add(l)
		}
	}
	if p := findPart(m.Payload, "text/plain"); p != nil {
		for _, u := range bareURL.FindAllString(decodePart(p), -1) {
			add(Link{URL: strings.TrimRight(u, ".,;:!?*")})
		}
	}
	return out
}

// HTMLLinks extracts <a href> targets with their visible text.
func HTMLLinks(src string) []Link {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return nil
	}
	var out []Link
	var text func(n *html.Node, b *strings.Builder)
	text = func(n *html.Node, b *strings.Builder) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Img {
			for _, a := range n.Attr {
				if a.Key == "alt" {
					b.WriteString(a.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			text(c, b)
		}
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.A {
			for _, a := range n.Attr {
				if a.Key == "href" && a.Val != "" {
					var b strings.Builder
					text(n, &b)
					out = append(out, Link{Text: strings.Join(strings.Fields(b.String()), " "), URL: a.Val})
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// Attachment is a file attached to a message.
type Attachment struct {
	MessageID    string
	Filename     string
	MimeType     string
	Size         int
	Inline       bool   // e.g. images embedded in a signature
	attachmentID string // fetched separately
	data         string // small parts carry their data inline
}

// AttachmentParts lists the message's attachments, real ones before inline images.
func (m Message) AttachmentParts() []Attachment {
	var files, inline []Attachment
	var walk func(p Part)
	walk = func(p Part) {
		if p.Filename != "" {
			a := Attachment{MessageID: m.ID, Filename: p.Filename, MimeType: p.MimeType, Size: p.Body.Size,
				attachmentID: p.Body.AttachmentID, data: p.Body.Data}
			disp := strings.ToLower(p.Header("Content-Disposition"))
			a.Inline = strings.HasPrefix(disp, "inline") || (disp == "" && p.Header("Content-ID") != "")
			if a.Inline {
				inline = append(inline, a)
			} else {
				files = append(files, a)
			}
		}
		for _, c := range p.Parts {
			walk(c)
		}
	}
	walk(m.Payload)
	return append(files, inline...)
}

// Attachments lists attachment file names.
func (m Message) Attachments() []string {
	var out []string
	for _, a := range m.AttachmentParts() {
		if !a.Inline {
			out = append(out, a.Filename)
		}
	}
	return out
}

// Download returns the attachment's bytes.
func (c *Client) Download(ctx context.Context, a Attachment) ([]byte, error) {
	data := a.data
	if data == "" {
		var resp struct {
			Data string `json:"data"`
		}
		path := "messages/" + url.PathEscape(a.MessageID) + "/attachments/" + url.PathEscape(a.attachmentID)
		if err := c.do(ctx, http.MethodGet, path, nil, nil, &resp); err != nil {
			return nil, err
		}
		data = resp.Data
	}
	b, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		b, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(data, "="))
	}
	return b, err
}
