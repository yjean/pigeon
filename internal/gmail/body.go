package gmail

import (
	"encoding/base64"
	"io"
	"mime"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/encoding/htmlindex"
)

// Body returns the readable text of a message (format=full): the text/plain
// part when there is one, otherwise the text/html part converted to text.
func (m Message) Body() string {
	if p := findPart(m.Payload, "text/plain"); p != nil {
		if s := decodePart(p); strings.TrimSpace(s) != "" {
			return normalize(s)
		}
	}
	if p := findPart(m.Payload, "text/html"); p != nil {
		return normalize(HTMLToText(decodePart(p)))
	}
	return ""
}

// Attachments lists attachment file names.
func (m Message) Attachments() []string {
	var out []string
	var walk func(p Part)
	walk = func(p Part) {
		if p.Filename != "" {
			out = append(out, p.Filename)
		}
		for _, c := range p.Parts {
			walk(c)
		}
	}
	walk(m.Payload)
	return out
}

func findPart(p Part, mimeType string) *Part {
	if strings.EqualFold(p.MimeType, mimeType) && p.Filename == "" && p.Body.Data != "" {
		return &p
	}
	for _, c := range p.Parts {
		if f := findPart(c, mimeType); f != nil {
			return f
		}
	}
	return nil
}

func decodePart(p *Part) string {
	data, err := base64.URLEncoding.DecodeString(p.Body.Data)
	if err != nil {
		data, _ = base64.RawURLEncoding.DecodeString(strings.TrimRight(p.Body.Data, "="))
	}
	_, params, _ := mime.ParseMediaType(p.Header("Content-Type"))
	if cs := strings.ToLower(params["charset"]); cs != "" && cs != "utf-8" && cs != "us-ascii" {
		if enc, err := htmlindex.Get(cs); err == nil {
			if out, err := io.ReadAll(enc.NewDecoder().Reader(strings.NewReader(string(data)))); err == nil {
				return string(out)
			}
		}
	}
	return string(data)
}

var (
	manyBlankLines = regexp.MustCompile(`\n{3,}`)
	trailingSpace  = regexp.MustCompile(`[ \t\x{00a0}]+\n`)
)

func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "‌", "") // zero-width non-joiners used as preheader padding
	s = trailingSpace.ReplaceAllString(s, "\n")
	s = manyBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// StripQuoted removes the quoted history of a reply ("> ..." lines and the
// "On <date>, X wrote:" line introducing them), which the thread already shows.
func StripQuoted(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), ">") {
			continue
		}
		out = append(out, l)
	}
	// Drop the attribution line(s), possibly wrapped over two lines.
	for len(out) > 0 {
		last := strings.TrimSpace(out[len(out)-1])
		if last == "" {
			out = out[:len(out)-1]
			continue
		}
		if strings.HasSuffix(last, "wrote:") || strings.HasSuffix(last, "a écrit :") || strings.HasSuffix(last, "a écrit:") {
			out = out[:len(out)-1]
			if n := len(out); n > 0 && strings.HasPrefix(strings.TrimSpace(out[n-1]), "On ") {
				out = out[:n-1]
			} else if n > 0 && strings.HasPrefix(strings.TrimSpace(out[n-1]), "Le ") {
				out = out[:n-1]
			}
		}
		break
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// HTMLToText renders an HTML email as plain text.
func HTMLToText(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return src
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			t := strings.Join(strings.Fields(n.Data), " ")
			if t == "" {
				if strings.ContainsAny(n.Data, " \t\n") && b.Len() > 0 && !strings.HasSuffix(b.String(), " ") && !strings.HasSuffix(b.String(), "\n") {
					b.WriteByte(' ')
				}
				return
			}
			if (n.Data[0] == ' ' || n.Data[0] == '\n') && b.Len() > 0 && !strings.HasSuffix(b.String(), " ") && !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte(' ')
			}
			b.WriteString(t)
			if last := n.Data[len(n.Data)-1]; last == ' ' || last == '\n' {
				b.WriteByte(' ')
			}
			return
		case html.ElementNode:
			switch n.DataAtom {
			case atom.Script, atom.Style, atom.Head, atom.Title, atom.Img:
				return
			case atom.Br:
				b.WriteByte('\n')
				return
			case atom.Li:
				newline(&b)
				b.WriteString("• ")
			case atom.P, atom.Div, atom.Tr, atom.Table, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
				atom.Ul, atom.Ol, atom.Blockquote, atom.Pre, atom.Section, atom.Header, atom.Footer, atom.Hr:
				newline(&b)
			case atom.Td, atom.Th:
				if !strings.HasSuffix(b.String(), "\n") && b.Len() > 0 {
					b.WriteByte(' ')
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.P, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Table, atom.Blockquote, atom.Pre, atom.Ul, atom.Ol:
				newline(&b)
				b.WriteByte('\n')
			case atom.Div, atom.Tr, atom.Li, atom.Section:
				newline(&b)
			}
		}
	}
	walk(doc)
	// Trim spaces at line starts left by inline whitespace.
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}

func newline(b *strings.Builder) {
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
}
