// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProviderFailureReceiptOmitsPotentialSecretsFromStderr(t *testing.T) {
	err := providerExitError(context.Background(), []byte("API_TOKEN=fixture-secret\nlogin failed"), errors.New("exit status 1"))
	if err == nil || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "API_TOKEN") {
		t.Fatalf("provider failure leaked stderr: %v", err)
	}
	if err.Error() != "el proceso del proveedor falló; se omitió stderr por privacidad" {
		t.Fatalf("unexpected sanitized provider failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := providerExitError(ctx, []byte("secret"), errors.New("exit")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled provider error=%v, want context.Canceled", err)
	}
}
