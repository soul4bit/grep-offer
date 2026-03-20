package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type SMTPMailer struct {
	host     string
	port     int
	secure   bool
	username string
	password string
	from     string
}

func NewSMTPMailer(host string, port int, secure bool, username, password, from string) *SMTPMailer {
	return &SMTPMailer{
		host:     strings.TrimSpace(host),
		port:     port,
		secure:   secure,
		username: strings.TrimSpace(username),
		password: password,
		from:     strings.TrimSpace(from),
	}
}

func (m *SMTPMailer) SendRegistrationConfirmation(ctx context.Context, email, username, confirmURL string) error {
	if m == nil {
		return fmt.Errorf("smtp mailer is nil")
	}

	subject := mime.BEncoding.Encode("UTF-8", "Подтверди регистрацию на grep-offer.ru")
	body := strings.Join([]string{
		fmt.Sprintf("Привет, %s.", username),
		"",
		"Твою заявку на grep-offer.ru апрувнули.",
		"Остался один шаг: открой ссылку ниже, чтобы подтвердить почту и сразу войти в кабинет.",
		"",
		confirmURL,
		"",
		"Если это был не ты, просто проигнорируй это письмо.",
	}, "\r\n")

	message := strings.Join([]string{
		fmt.Sprintf("From: %s", m.from),
		fmt.Sprintf("To: %s", strings.TrimSpace(email)),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	client, err := m.newClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	if m.username != "" {
		if err := m.authenticate(client); err != nil {
			return err
		}
	}

	if err := client.Mail(m.from); err != nil {
		return err
	}
	if err := client.Rcpt(strings.TrimSpace(email)); err != nil {
		return err
	}

	writer, err := client.Data()
	if err != nil {
		return err
	}

	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	return client.Quit()
}

func (m *SMTPMailer) newClient(ctx context.Context) (*smtp.Client, error) {
	address := fmt.Sprintf("%s:%d", m.host, m.port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}

	if m.secure {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: m.host,
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return smtp.NewClient(tlsConn, m.host)
	}

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{
			ServerName: m.host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

	return client, nil
}

func (m *SMTPMailer) authenticate(client *smtp.Client) error {
	ok, mechanisms := client.Extension("AUTH")
	if !ok {
		return fmt.Errorf("smtp server does not advertise AUTH")
	}

	supported := map[string]bool{}
	for _, mechanism := range strings.Fields(strings.ToUpper(strings.TrimSpace(mechanisms))) {
		supported[mechanism] = true
	}

	switch {
	case supported["LOGIN"]:
		return client.Auth(loginAuth{
			username: m.username,
			password: m.password,
		})
	case supported["PLAIN"]:
		return client.Auth(smtp.PlainAuth("", m.username, m.password, m.host))
	default:
		return fmt.Errorf("smtp server does not support a compatible auth mechanism: %s", mechanisms)
	}
}

type loginAuth struct {
	username string
	password string
}

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}

	prompt := strings.TrimSpace(strings.ToLower(string(fromServer)))
	switch {
	case strings.Contains(prompt, "username"), strings.Contains(prompt, "login"):
		return []byte(a.username), nil
	case strings.Contains(prompt, "password"):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected AUTH LOGIN challenge: %q", string(fromServer))
	}
}
