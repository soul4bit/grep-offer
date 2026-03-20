package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	htemplate "html/template"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
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
	body, err := buildRegistrationConfirmationBody(username, confirmURL)
	if err != nil {
		return err
	}

	messageHeaders := strings.Join([]string{
		fmt.Sprintf("From: %s", m.from),
		fmt.Sprintf("To: %s", strings.TrimSpace(email)),
		fmt.Sprintf("Subject: %s", subject),
		body.headers,
	}, "\r\n")
	message := []byte(messageHeaders + "\r\n\r\n" + body.content)

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

	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	return client.Quit()
}

func (m *SMTPMailer) SendPasswordReset(ctx context.Context, email, username, resetURL string) error {
	if m == nil {
		return fmt.Errorf("smtp mailer is nil")
	}

	subject := mime.BEncoding.Encode("UTF-8", "Сброс пароля на grep-offer.ru")
	body, err := buildPasswordResetBody(username, resetURL)
	if err != nil {
		return err
	}

	messageHeaders := strings.Join([]string{
		fmt.Sprintf("From: %s", m.from),
		fmt.Sprintf("To: %s", strings.TrimSpace(email)),
		fmt.Sprintf("Subject: %s", subject),
		body.headers,
	}, "\r\n")
	message := []byte(messageHeaders + "\r\n\r\n" + body.content)

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

	if _, err := writer.Write(message); err != nil {
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

type emailBody struct {
	headers string
	content string
}

func buildRegistrationConfirmationBody(username, confirmURL string) (emailBody, error) {
	plain := strings.Join([]string{
		fmt.Sprintf("Привет, %s.", username),
		"",
		"Твою заявку на grep-offer.ru апрувнули.",
		"Остался один шаг: открой ссылку ниже, чтобы подтвердить почту и сразу войти в кабинет.",
		"",
		confirmURL,
		"",
		"Если кнопка в письме не откроется, просто скопируй ссылку в браузер.",
		"",
		"Если это был не ты, просто проигнорируй это письмо.",
	}, "\r\n")

	htmlBody, err := renderRegistrationConfirmationHTML(username, confirmURL)
	if err != nil {
		return emailBody{}, err
	}

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)

	plainHeader := textproto.MIMEHeader{}
	plainHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	plainHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	plainPart, err := writer.CreatePart(plainHeader)
	if err != nil {
		return emailBody{}, err
	}
	if err := writeQuotedPrintable(plainPart, plain); err != nil {
		return emailBody{}, err
	}

	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, err := writer.CreatePart(htmlHeader)
	if err != nil {
		return emailBody{}, err
	}
	if err := writeQuotedPrintable(htmlPart, htmlBody); err != nil {
		return emailBody{}, err
	}

	if err := writer.Close(); err != nil {
		return emailBody{}, err
	}

	return emailBody{
		headers: strings.Join([]string{
			"MIME-Version: 1.0",
			fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q", writer.Boundary()),
		}, "\r\n"),
		content: buffer.String(),
	}, nil
}

func buildPasswordResetBody(username, resetURL string) (emailBody, error) {
	plain := strings.Join([]string{
		fmt.Sprintf("Привет, %s.", username),
		"",
		"Кто-то попросил сбросить пароль на grep-offer.ru.",
		"Если это был ты, открой ссылку ниже и задай новый пароль:",
		"",
		resetURL,
		"",
		"Если это был не ты, просто проигнорируй письмо. Ничего не сломается.",
	}, "\r\n")

	htmlBody, err := renderPasswordResetHTML(username, resetURL)
	if err != nil {
		return emailBody{}, err
	}

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)

	plainHeader := textproto.MIMEHeader{}
	plainHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	plainHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	plainPart, err := writer.CreatePart(plainHeader)
	if err != nil {
		return emailBody{}, err
	}
	if err := writeQuotedPrintable(plainPart, plain); err != nil {
		return emailBody{}, err
	}

	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, err := writer.CreatePart(htmlHeader)
	if err != nil {
		return emailBody{}, err
	}
	if err := writeQuotedPrintable(htmlPart, htmlBody); err != nil {
		return emailBody{}, err
	}

	if err := writer.Close(); err != nil {
		return emailBody{}, err
	}

	return emailBody{
		headers: strings.Join([]string{
			"MIME-Version: 1.0",
			fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q", writer.Boundary()),
		}, "\r\n"),
		content: buffer.String(),
	}, nil
}

func renderRegistrationConfirmationHTML(username, confirmURL string) (string, error) {
	const templateSource = `<!doctype html>
<html lang="ru">
<body style="margin:0;padding:0;background:#0b100d;color:#f6f2e8;font-family:'Segoe UI',Arial,sans-serif;">
  <div style="padding:32px 16px;background:radial-gradient(circle at top left, rgba(216,255,97,0.12), transparent 35%),linear-gradient(180deg,#090c0a 0%,#101512 100%);">
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:640px;margin:0 auto;border-collapse:collapse;">
      <tr>
        <td style="padding:0 0 18px 0;color:#d8ff61;font-size:12px;letter-spacing:1.6px;text-transform:uppercase;font-family:Consolas,monospace;">
          grep-offer.ru / approved
        </td>
      </tr>
      <tr>
        <td style="border:1px solid rgba(255,255,255,0.1);border-radius:24px;background:rgba(16,21,18,0.92);box-shadow:0 18px 48px rgba(0,0,0,0.28);padding:28px 28px 24px;">
          <p style="margin:0 0 12px 0;color:#65e0b2;font-size:12px;letter-spacing:1.4px;text-transform:uppercase;font-family:Consolas,monospace;">доступ одобрен</p>
          <h1 style="margin:0 0 16px 0;font-size:32px;line-height:1.05;color:#f6f2e8;font-weight:800;">Привет, {{.Username}}.</h1>
          <p style="margin:0 0 14px 0;font-size:16px;line-height:1.7;color:#d8d2c2;">
            Твою заявку на <strong style="color:#f6f2e8;">grep-offer.ru</strong> апрувнули.
            Остался один шаг: подтвердить почту и сразу открыть сессию в кабинете.
          </p>

          <div style="margin:24px 0 24px 0;">
            <a href="{{.ConfirmURL}}" style="display:inline-block;padding:14px 22px;border-radius:999px;background:linear-gradient(135deg,#d8ff61 0%,#f3ffad 100%);color:#10140f;text-decoration:none;font-weight:800;">Подтвердить почту и войти</a>
          </div>

          <div style="margin:0 0 20px 0;padding:14px 16px;border:1px solid rgba(101,224,178,0.16);border-radius:18px;background:rgba(255,255,255,0.025);">
            <p style="margin:0 0 8px 0;color:#65e0b2;font-size:12px;letter-spacing:1.3px;text-transform:uppercase;font-family:Consolas,monospace;">если кнопка не сработала</p>
            <p style="margin:0;color:#a0ab9e;font-size:14px;line-height:1.7;word-break:break-word;">{{.ConfirmURL}}</p>
          </div>

          <p style="margin:0;color:#a0ab9e;font-size:14px;line-height:1.7;">
            Если это был не ты, просто проигнорируй письмо. Никаких магических действий с твоей стороны больше не нужно.
          </p>
        </td>
      </tr>
    </table>
  </div>
</body>
</html>`

	data := struct {
		Username   string
		ConfirmURL string
	}{
		Username:   username,
		ConfirmURL: confirmURL,
	}

	tmpl, err := htemplate.New("registration-email").Parse(templateSource)
	if err != nil {
		return "", err
	}

	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		return "", err
	}

	return buffer.String(), nil
}

func renderPasswordResetHTML(username, resetURL string) (string, error) {
	const templateSource = `<!doctype html>
<html lang="ru">
<body style="margin:0;padding:0;background:#0b100d;color:#f6f2e8;font-family:'Segoe UI',Arial,sans-serif;">
  <div style="padding:32px 16px;background:radial-gradient(circle at top left, rgba(255,132,90,0.12), transparent 35%),linear-gradient(180deg,#090c0a 0%,#101512 100%);">
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:640px;margin:0 auto;border-collapse:collapse;">
      <tr>
        <td style="padding:0 0 18px 0;color:#ff845a;font-size:12px;letter-spacing:1.6px;text-transform:uppercase;font-family:Consolas,monospace;">
          grep-offer.ru / reset password
        </td>
      </tr>
      <tr>
        <td style="border:1px solid rgba(255,255,255,0.1);border-radius:24px;background:rgba(16,21,18,0.92);box-shadow:0 18px 48px rgba(0,0,0,0.28);padding:28px 28px 24px;">
          <p style="margin:0 0 12px 0;color:#ff845a;font-size:12px;letter-spacing:1.4px;text-transform:uppercase;font-family:Consolas,monospace;">сброс пароля</p>
          <h1 style="margin:0 0 16px 0;font-size:32px;line-height:1.05;color:#f6f2e8;font-weight:800;">Привет, {{.Username}}.</h1>
          <p style="margin:0 0 14px 0;font-size:16px;line-height:1.7;color:#d8d2c2;">
            Кто-то попросил сбросить пароль на <strong style="color:#f6f2e8;">grep-offer.ru</strong>.
            Если это был ты, открой ссылку ниже и задай новый пароль без очередного похода в support-level-hell.
          </p>

          <div style="margin:24px 0 24px 0;">
            <a href="{{.ResetURL}}" style="display:inline-block;padding:14px 22px;border-radius:999px;background:linear-gradient(135deg,#ff845a 0%,#ffc0a8 100%);color:#10140f;text-decoration:none;font-weight:800;">Задать новый пароль</a>
          </div>

          <div style="margin:0 0 20px 0;padding:14px 16px;border:1px solid rgba(255,132,90,0.16);border-radius:18px;background:rgba(255,255,255,0.025);">
            <p style="margin:0 0 8px 0;color:#ff845a;font-size:12px;letter-spacing:1.3px;text-transform:uppercase;font-family:Consolas,monospace;">если кнопка не сработала</p>
            <p style="margin:0;color:#a0ab9e;font-size:14px;line-height:1.7;word-break:break-word;">{{.ResetURL}}</p>
          </div>

          <p style="margin:0;color:#a0ab9e;font-size:14px;line-height:1.7;">
            Если это был не ты, просто проигнорируй письмо. Пароль сам по себе не поменяется.
          </p>
        </td>
      </tr>
    </table>
  </div>
</body>
</html>`

	data := struct {
		Username string
		ResetURL string
	}{
		Username: username,
		ResetURL: resetURL,
	}

	tmpl, err := htemplate.New("password-reset-email").Parse(templateSource)
	if err != nil {
		return "", err
	}

	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		return "", err
	}

	return buffer.String(), nil
}

func writeQuotedPrintable(writer io.Writer, content string) error {
	encoder := quotedprintable.NewWriter(writer)
	if _, err := encoder.Write([]byte(content)); err != nil {
		_ = encoder.Close()
		return err
	}
	return encoder.Close()
}
