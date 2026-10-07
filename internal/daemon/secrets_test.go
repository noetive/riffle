package daemon_test

import (
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/daemon"
)

func TestSecretIsUsableOnlyOnItsBoundOrigin(t *testing.T) {
	t.Setenv("RIFFLE_SECRET_SHOP", "hunter2")
	t.Setenv("RIFFLE_SECRET_SHOP_ORIGIN", "https://shop.example")
	var r daemon.EnvSecrets

	if v, err := r.Resolve("shop", "https://shop.example"); err != nil || v != "hunter2" {
		t.Fatalf("bound origin must resolve: %q %v", v, err)
	}
	_, err := r.Resolve("shop", "https://evil.example")
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("other origin must be refused without echoing the value: %v", err)
	}
}

func TestSecretWithoutBoundOriginIsRefused(t *testing.T) {
	t.Setenv("RIFFLE_SECRET_LOOSE", "x")
	if _, err := (daemon.EnvSecrets{}).Resolve("loose", "https://a.example"); err == nil {
		t.Fatal("a secret with no bound origin must not resolve anywhere")
	}
}

func TestUnknownSecretNamesTheNextAction(t *testing.T) {
	_, err := (daemon.EnvSecrets{}).Resolve("nope", "https://a.example")
	if err == nil || !strings.Contains(err.Error(), "RIFFLE_SECRET_NOPE") {
		t.Fatalf("error should say how to register it: %v", err)
	}
}

func TestASecretTooShortToMaskSafelyIsRefused(t *testing.T) {
	t.Setenv("RIFFLE_SECRET_PIN", "123")
	t.Setenv("RIFFLE_SECRET_PIN_ORIGIN", "https://bank.example")
	_, err := (daemon.EnvSecrets{}).Resolve("pin", "https://bank.example")
	if err == nil || strings.Contains(err.Error(), "123") {
		t.Fatalf("a three-character secret would mask ordinary text; refuse it without echoing it: %v", err)
	}
	t.Setenv("RIFFLE_SECRET_PIN", "1234")
	if v, err := (daemon.EnvSecrets{}).Resolve("pin", "https://bank.example"); err != nil || v != "1234" {
		t.Fatalf("a four-digit PIN is usable: %q %v", v, err)
	}
}
