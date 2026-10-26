package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// sender delivers a message to a destination and reports the attempts made.
type sender interface {
	send(ctx context.Context, kind string, d Destination, m Message) (attempts int, err error)
}

type netSender struct {
	client  *http.Client
	dialer  *net.Dialer
	version string
	backoff []time.Duration
}

// errBlockedAddress is returned for destinations on link-local, multicast,
// or unspecified addresses (for example a cloud metadata service).
var errBlockedAddress = errors.New("destination address is not allowed (link-local, multicast, or unspecified)")

// safeControl refuses connections to addresses no notification should reach.
// Private networks stay allowed: internal webhooks and mail relays are common.
func safeControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return errBlockedAddress
	}
	return nil
}

func newSender(version string) *netSender {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: safeControl}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns: 10, IdleConnTimeout: 60 * time.Second, Proxy: http.ProxyFromEnvironment}
	return &netSender{
		client: &http.Client{Transport: tr, Timeout: 20 * time.Second,
			// A redirect could move the request (and its body) elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		dialer: d, version: version, backoff: []time.Duration{2 * time.Second, 10 * time.Second},
	}
}

func (s *netSender) send(ctx context.Context, kind string, d Destination, m Message) (int, error) {
	attempts := 0
	for {
		attempts++
		retry, err := s.once(ctx, kind, d, m)
		if err == nil || !retry || attempts > len(s.backoff) {
			return attempts, err
		}
		select {
		case <-ctx.Done():
			return attempts, err
		case <-time.After(s.backoff[attempts-1]):
		}
	}
}

func (s *netSender) once(ctx context.Context, kind string, d Destination, m Message) (retry bool, err error) {
	switch kind {
	case KindWebhook:
		return s.post(ctx, d.URL, m.webhookBody(), d.Secret, m.Event)
	case KindSlack:
		return s.post(ctx, d.URL, m.slackBody(), "", m.Event)
	case KindEmail:
		return true, s.mail(ctx, d, m)
	}
	return false, fmt.Errorf("unknown channel kind %q", kind)
}

// post sends a JSON body. Errors never contain the URL: webhook URLs often
// carry tokens (Slack's always do).
func (s *netSender) post(ctx context.Context, rawURL string, body []byte, secret, event string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return false, errors.New("invalid destination URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "LockLens/"+s.version)
	req.Header.Set("X-LockLens-Event", event)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req.Header.Set("X-LockLens-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return !errors.Is(err, errBlockedAddress), fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return true, fmt.Errorf("destination answered HTTP %d", resp.StatusCode)
	}
	return false, fmt.Errorf("destination answered HTTP %d", resp.StatusCode)
}

// headerValue keeps a value on one header line.
func headerValue(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

func (s *netSender) mail(ctx context.Context, d Destination, m Message) error {
	addr := net.JoinHostPort(d.SMTPHost, strconv.Itoa(d.SMTPPort))
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := s.dialer.DialContext(dctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connect to the mail server: %w", err)
	}
	deadline, _ := dctx.Deadline()
	_ = conn.SetDeadline(deadline)
	tlsConf := &tls.Config{ServerName: d.SMTPHost, MinVersion: tls.VersionTLS12}
	if d.TLS == TLSImplicit {
		conn = tls.Client(conn, tlsConf)
	}
	c, err := smtp.NewClient(conn, d.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail server greeting: %w", err)
	}
	defer c.Close()
	if d.TLS == TLSStart {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server does not offer STARTTLS")
		}
		if err := c.StartTLS(tlsConf); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if d.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", d.Username, d.Password, d.SMTPHost)); err != nil {
			return fmt.Errorf("mail server authentication: %w", err)
		}
	}
	if err := c.Mail(d.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, to := range d.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: LockLens <%s>\r\n", d.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(d.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", headerValue(m.Subject())))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\nAuto-Submitted: auto-generated\r\n\r\n")
	b.WriteString(strings.ReplaceAll(m.Text(), "\n", "\r\n"))
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("send the message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("send the message: %w", err)
	}
	return c.Quit()
}
