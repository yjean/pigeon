package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"
)

// Outgoing is an email being composed.
type Outgoing struct {
	From       Address
	To, Cc     string // comma-separated, as typed by the user
	Subject    string
	Body       string
	ThreadID   string // set for replies/forwards so Gmail threads them
	InReplyTo  string
	References string
	Files      []File
}

// File is an attachment of an outgoing message.
type File struct {
	Name string
	Type string // MIME type; guessed when empty
	Data []byte
}

// MaxAttachments is Gmail's limit for attachments of a sent message.
const MaxAttachments = 25 << 20

// Raw renders the message as RFC 5322 bytes (UTF-8, quoted-printable body).
// It validates the recipients.
func (o Outgoing) Raw() ([]byte, error) {
	to, err := parseRecipients(o.To)
	if err != nil {
		return nil, fmt.Errorf("To: %w", err)
	}
	cc, err := parseRecipients(o.Cc)
	if err != nil {
		return nil, fmt.Errorf("Cc: %w", err)
	}
	if len(to)+len(cc) == 0 {
		return nil, errors.New("add at least one recipient")
	}

	var b bytes.Buffer
	from := mail.Address{Name: o.From.Name, Address: o.From.Email}
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	if len(to) > 0 {
		fmt.Fprintf(&b, "To: %s\r\n", joinAddrs(to))
	}
	if len(cc) > 0 {
		fmt.Fprintf(&b, "Cc: %s\r\n", joinAddrs(cc))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", o.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	if o.InReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\n", o.InReplyTo)
	}
	if o.References != "" {
		fmt.Fprintf(&b, "References: %s\r\n", o.References)
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	if len(o.Files) == 0 {
		writeTextPart(&b, o.Body)
		return b.Bytes(), nil
	}

	mw := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mw.Boundary())
	text, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {`text/plain; charset="UTF-8"`},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, err
	}
	writeQP(text, o.Body)
	for _, f := range o.Files {
		ctype := f.Type
		if ctype == "" {
			ctype = mime.TypeByExtension(strings.ToLower(filepath.Ext(f.Name)))
		}
		if ctype == "" {
			ctype = http.DetectContentType(f.Data)
		}
		name := mime.QEncoding.Encode("utf-8", f.Name)
		part, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(baseType(ctype), map[string]string{"name": name})},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": f.Name})},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, err
		}
		writeBase64Lines(part, f.Data)
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// FilesSize is the total size of the attachments.
func (o Outgoing) FilesSize() int {
	n := 0
	for _, f := range o.Files {
		n += len(f.Data)
	}
	return n
}

func writeTextPart(b *bytes.Buffer, body string) {
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	writeQP(b, body)
}

func writeQP(w io.Writer, body string) {
	qp := quotedprintable.NewWriter(w)
	_, _ = qp.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")))
	_ = qp.Close()
}

// writeBase64Lines writes base64 wrapped at 76 columns (RFC 2045).
func writeBase64Lines(w io.Writer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		io.WriteString(w, enc[:76]+"\r\n")
		enc = enc[76:]
	}
	io.WriteString(w, enc+"\r\n")
}

func baseType(ctype string) string {
	if t, _, err := mime.ParseMediaType(ctype); err == nil {
		return t
	}
	return "application/octet-stream"
}

func parseRecipients(s string) ([]*mail.Address, error) {
	s = strings.Trim(strings.TrimSpace(s), ",;")
	if s == "" {
		return nil, nil
	}
	return mail.ParseAddressList(strings.ReplaceAll(s, ";", ","))
}

func joinAddrs(list []*mail.Address) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.String()
	}
	return strings.Join(parts, ", ")
}

// Send sends the message.
func (c *Client) Send(ctx context.Context, o Outgoing) error {
	if o.FilesSize() > MaxAttachments {
		return fmt.Errorf("attachments total %d MB, over Gmail's 25 MB limit", o.FilesSize()>>20)
	}
	raw, err := o.Raw()
	if err != nil {
		return err
	}
	meta := map[string]string{}
	if o.ThreadID != "" {
		meta["threadId"] = o.ThreadID
	}
	return c.upload(ctx, "messages/send", meta, raw)
}

// upload posts a raw RFC 822 message through the media upload endpoint
// (multipart/related: JSON metadata + the message), which, unlike the JSON
// "raw" field, accepts messages up to 35 MB.
func (c *Client) upload(ctx context.Context, path string, meta any, raw []byte) error {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	p, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
	p.Write(metaJSON)
	p, _ = mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"message/rfc822"}})
	p.Write(raw)
	if err := mw.Close(); err != nil {
		return err
	}
	return c.request(ctx, http.MethodPost, uploadURL+path+"?uploadType=multipart",
		"multipart/related; boundary="+mw.Boundary(), b.Bytes(), nil)
}

// SaveDraft stores the message in Drafts. Recipients may be empty.
func (c *Client) SaveDraft(ctx context.Context, o Outgoing) error {
	if strings.TrimSpace(o.To+o.Cc) == "" {
		o.To = o.From.Email // Raw requires a recipient; harmless placeholder for a draft
	}
	raw, err := o.Raw()
	if err != nil {
		return err
	}
	msg := map[string]string{}
	if o.ThreadID != "" {
		msg["threadId"] = o.ThreadID
	}
	return c.upload(ctx, "drafts", map[string]any{"message": msg}, raw)
}

// DisplayName returns the primary send-as display name ("" if unset).
func (c *Client) DisplayName(ctx context.Context) (string, error) {
	var resp struct {
		SendAs []struct {
			DisplayName string `json:"displayName"`
			IsPrimary   bool   `json:"isPrimary"`
		} `json:"sendAs"`
	}
	if err := c.do(ctx, http.MethodGet, "settings/sendAs", nil, nil, &resp); err != nil {
		return "", err
	}
	for _, s := range resp.SendAs {
		if s.IsPrimary {
			return s.DisplayName, nil
		}
	}
	return "", nil
}

// --- reply / forward helpers ---

// Format renders an address for an editable field: `Name <email>`.
func (a Address) Format() string {
	if a.Name == "" {
		return a.Email
	}
	if strings.ContainsAny(a.Name, `,;<>"@()`) {
		return fmt.Sprintf("%q <%s>", a.Name, a.Email)
	}
	return a.Name + " <" + a.Email + ">"
}

func formatList(list []Address) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.Format()
	}
	return strings.Join(parts, ", ")
}

// Reply prepares a reply (or reply-all) to the last message of the thread.
func Reply(t *Thread, me Address, all bool) Outgoing {
	last := t.Messages[len(t.Messages)-1]
	from := ParseAddress(last.Header("From"))
	isMe := func(a Address) bool { return strings.EqualFold(a.Email, me.Email) }

	var to, cc []Address
	if isMe(from) { // replying to my own message: same recipients again
		to = ParseAddressList(last.Header("To"))
		if all {
			cc = ParseAddressList(last.Header("Cc"))
		}
	} else {
		if rt := ParseAddressList(last.Header("Reply-To")); len(rt) > 0 {
			to = rt
		} else {
			to = []Address{from}
		}
		if all {
			seen := map[string]bool{strings.ToLower(me.Email): true}
			for _, a := range to {
				seen[strings.ToLower(a.Email)] = true
			}
			for _, a := range append(ParseAddressList(last.Header("To")), ParseAddressList(last.Header("Cc"))...) {
				if k := strings.ToLower(a.Email); !seen[k] {
					seen[k] = true
					cc = append(cc, a)
				}
			}
		}
	}

	msgID := last.Header("Message-ID")
	refs := strings.TrimSpace(last.Header("References") + " " + msgID)
	return Outgoing{
		From:       me,
		To:         formatList(to),
		Cc:         formatList(cc),
		Subject:    prefixSubject("Re: ", t.Messages[0].Header("Subject")),
		Body:       "\n\n" + attribution(last) + "\n" + quote(last.Body()),
		ThreadID:   t.ID,
		InReplyTo:  msgID,
		References: refs,
	}
}

// Forward prepares a forward of the last message of the thread (text only).
func Forward(t *Thread, me Address) Outgoing {
	last := t.Messages[len(t.Messages)-1]
	var b strings.Builder
	b.WriteString("\n\n---------- Forwarded message ---------\n")
	fmt.Fprintf(&b, "From: %s\n", last.Header("From"))
	fmt.Fprintf(&b, "Date: %s\n", last.Time().Local().Format("Mon, Jan 2, 2006 at 3:04 PM"))
	fmt.Fprintf(&b, "Subject: %s\n", last.Header("Subject"))
	fmt.Fprintf(&b, "To: %s\n", last.Header("To"))
	if cc := last.Header("Cc"); cc != "" {
		fmt.Fprintf(&b, "Cc: %s\n", cc)
	}
	b.WriteString("\n")
	b.WriteString(last.Body())
	return Outgoing{
		From:     me,
		Subject:  prefixSubject("Fwd: ", t.Messages[0].Header("Subject")),
		Body:     b.String(),
		ThreadID: t.ID,
	}
}

func prefixSubject(prefix, subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), strings.ToLower(prefix)) {
		return subject
	}
	return prefix + subject
}

func attribution(m Message) string {
	return fmt.Sprintf("On %s, %s wrote:", m.Time().Local().Format("Mon, Jan 2, 2006 at 3:04 PM"), m.Header("From"))
}

func quote(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ">") {
			lines[i] = ">" + l
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}
