package gmail

import (
	"encoding/base64"
	"html"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTMLBody returns the message's text/html part, decoded, and whether it has one.
func (m Message) HTMLBody() (string, bool) {
	p := findPart(m.Payload, "text/html")
	if p == nil {
		return "", false
	}
	s := decodePart(p)
	return s, strings.TrimSpace(s) != ""
}

// InlineImages lists the image parts the HTML body can reference as cid:…,
// with or without a file name.
func (m Message) InlineImages() []Attachment {
	var out []Attachment
	var walk func(p Part)
	walk = func(p Part) {
		cid := strings.Trim(strings.TrimSpace(p.Header("Content-ID")), "<>")
		if cid != "" && strings.HasPrefix(strings.ToLower(p.MimeType), "image/") {
			out = append(out, Attachment{MessageID: m.ID, Filename: p.Filename, MimeType: p.MimeType, Size: p.Body.Size,
				Inline: true, ContentID: cid, attachmentID: p.Body.AttachmentID, data: p.Body.Data})
		}
		for _, c := range p.Parts {
			walk(c)
		}
	}
	walk(m.Payload)
	return out
}

// Image is an inline image's bytes, keyed by Content-ID in HTMLPage.
type Image struct {
	MimeType string
	Data     []byte
}

// PageHeader is what HTMLPage shows above the message.
type PageHeader struct {
	Subject, From, To, Cc, Date string
}

// HTMLPage turns an HTML email into a standalone page to open in a browser.
//
// The page cannot run code or phone home on its own: scripts, frames, objects,
// <base>, meta refresh and on* handlers are removed, and a Content-Security-Policy
// allows only inline styles and data: resources. Inline images (cid:) become
// data: URIs. Remote images, fonts and stylesheets stay blocked unless
// allowRemote: they are how senders track opens (tracking pixels).
func HTMLPage(src string, h PageHeader, images map[string]Image, allowRemote bool) string {
	doc, err := xhtml.Parse(strings.NewReader(src))
	if err != nil {
		doc, _ = xhtml.Parse(strings.NewReader("<pre>" + html.EscapeString(src) + "</pre>"))
	}
	sanitize(doc, images)

	head, body := find(doc, atom.Head), find(doc, atom.Body)
	if head == nil || body == nil { // html.Parse always builds both; be safe anyway
		return ""
	}
	prepend(head, headExtras(allowRemote)...)
	prepend(body, banner(h, allowRemote)...)

	var b strings.Builder
	b.WriteString("<!doctype html>\n")
	if err := xhtml.Render(&b, doc); err != nil {
		return ""
	}
	return b.String()
}

// CSP is the policy HTMLPage applies.
func CSP(allowRemote bool) string {
	remote := ""
	if allowRemote {
		remote = " https: http:"
	}
	return "default-src 'none'; img-src data:" + remote + "; style-src 'unsafe-inline' data:" + remote +
		"; font-src data:" + remote + "; media-src data:; form-action 'none'; base-uri 'none'"
}

var dropped = map[atom.Atom]bool{
	atom.Script: true, atom.Noscript: true, atom.Iframe: true, atom.Frame: true, atom.Frameset: true,
	atom.Object: true, atom.Embed: true, atom.Applet: true, atom.Base: true,
}

func sanitize(n *xhtml.Node, images map[string]Image) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == xhtml.ElementNode && (dropped[c.DataAtom] || isMetaRefresh(c)) {
			n.RemoveChild(c)
		} else {
			if c.Type == xhtml.ElementNode {
				cleanAttrs(c, images)
			}
			sanitize(c, images)
		}
		c = next
	}
}

func isMetaRefresh(n *xhtml.Node) bool {
	return n.DataAtom == atom.Meta && strings.EqualFold(attr(n, "http-equiv"), "refresh")
}

func cleanAttrs(n *xhtml.Node, images map[string]Image) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		if strings.HasPrefix(key, "on") || key == "srcdoc" || key == "formaction" {
			continue
		}
		if (key == "href" || key == "src" || key == "action") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
			continue
		}
		if key == "src" || key == "background" {
			if v := strings.TrimSpace(a.Val); len(v) > 4 && strings.EqualFold(v[:4], "cid:") {
				if img, ok := images[v[4:]]; ok {
					a.Val = "data:" + img.MimeType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
				}
			}
		}
		kept = append(kept, a)
	}
	n.Attr = kept
	if n.DataAtom == atom.A {
		setAttr(n, "rel", "noopener noreferrer")
	}
}

func headExtras(allowRemote bool) []*xhtml.Node {
	return []*xhtml.Node{
		el(atom.Meta, "charset", "utf-8"),
		el(atom.Meta, "http-equiv", "Content-Security-Policy", "content", CSP(allowRemote)),
		el(atom.Meta, "name", "referrer", "content", "no-referrer"),
		withText(el(atom.Style), `.pigeon-header{font:14px/1.5 system-ui,sans-serif;color:#222;background:#f4f4f5;`+
			`border-bottom:1px solid #ddd;padding:12px 16px;margin:0 0 12px}`+
			`.pigeon-header h1{font-size:18px;margin:0 0 6px}.pigeon-header div{color:#555}`+
			`.pigeon-header .pigeon-note{margin-top:6px;color:#8a5a00}`),
	}
}

func banner(h PageHeader, allowRemote bool) []*xhtml.Node {
	box := el(atom.Div, "class", "pigeon-header")
	subject := h.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	box.AppendChild(withText(el(atom.H1), subject))
	for _, row := range [][2]string{{"From", h.From}, {"To", h.To}, {"Cc", h.Cc}, {"Date", h.Date}} {
		if row[1] != "" {
			box.AppendChild(withText(el(atom.Div), row[0]+": "+row[1]))
		}
	}
	if !allowRemote {
		box.AppendChild(withText(el(atom.Div, "class", "pigeon-note"),
			"Remote images are blocked (they can tell the sender you opened this). "+
				"In pigeon, O opens the message with them."))
	}
	return []*xhtml.Node{box}
}

func el(a atom.Atom, kv ...string) *xhtml.Node {
	n := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: a, Data: a.String()}
	for i := 0; i+1 < len(kv); i += 2 {
		n.Attr = append(n.Attr, xhtml.Attribute{Key: kv[i], Val: kv[i+1]})
	}
	return n
}

func withText(n *xhtml.Node, s string) *xhtml.Node {
	n.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: s}) // Render escapes it
	return n
}

func prepend(parent *xhtml.Node, nodes ...*xhtml.Node) {
	first := parent.FirstChild
	for _, n := range nodes {
		parent.InsertBefore(n, first)
	}
}

func find(n *xhtml.Node, a atom.Atom) *xhtml.Node {
	if n.Type == xhtml.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, a); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *xhtml.Node, key, val string) {
	for i, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, xhtml.Attribute{Key: key, Val: val})
}
