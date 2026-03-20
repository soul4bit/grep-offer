package notify

import (
	"strings"
	"testing"
)

func TestBuildRegistrationConfirmationBody(t *testing.T) {
	t.Parallel()

	body, err := buildRegistrationConfirmationBody("bash_bandit", "https://grep-offer.ru/register/confirm?token=test-token")
	if err != nil {
		t.Fatalf("build email body: %v", err)
	}

	if !strings.Contains(body.headers, "multipart/alternative") {
		t.Fatalf("multipart header missing: %s", body.headers)
	}

	if !strings.Contains(body.content, "text/plain; charset=UTF-8") {
		t.Fatalf("plain text part missing: %s", body.content)
	}

	if !strings.Contains(body.content, "text/html; charset=UTF-8") {
		t.Fatalf("html part missing: %s", body.content)
	}

	if !strings.Contains(body.content, "bash_bandit") {
		t.Fatalf("username missing from email body: %s", body.content)
	}

	if !strings.Contains(body.content, "https://grep-offer.ru/register/confirm?token=3Dtest-token") {
		t.Fatalf("confirm url missing from quoted-printable body: %s", body.content)
	}
}

func TestBuildPasswordResetBody(t *testing.T) {
	t.Parallel()

	body, err := buildPasswordResetBody("bash_bandit", "https://grep-offer.ru/password/reset?token=test-token")
	if err != nil {
		t.Fatalf("build reset email body: %v", err)
	}

	if !strings.Contains(body.headers, "multipart/alternative") {
		t.Fatalf("multipart header missing: %s", body.headers)
	}

	if !strings.Contains(body.content, "text/plain; charset=UTF-8") {
		t.Fatalf("plain text part missing: %s", body.content)
	}

	if !strings.Contains(body.content, "text/html; charset=UTF-8") {
		t.Fatalf("html part missing: %s", body.content)
	}

	if !strings.Contains(body.content, "bash_bandit") {
		t.Fatalf("username missing from reset email body: %s", body.content)
	}

	if !strings.Contains(body.content, "https://grep-offer.ru/password/reset?token=3Dtest-token") {
		t.Fatalf("reset url missing from quoted-printable body: %s", body.content)
	}
}
