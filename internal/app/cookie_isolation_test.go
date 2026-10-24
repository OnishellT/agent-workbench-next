package app

import (
	"net/http"
	"testing"
)

func TestCookieCannotAuthenticateAnotherInstance(t *testing.T) {
	firstCfg := testConfig(t)
	first, err := New(firstCfg, testAssets(), testModules(t, firstCfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, first.Handler(), firstCfg)

	secondCfg := testConfig(t)
	secondCfg.AuthToken = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKL"
	second, err := New(secondCfg, testAssets(), testModules(t, secondCfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodGet, secondCfg.PublicURL+"/api/bootstrap", "127.0.0.1:18888", nil, "")
	addCookie(request, cookie)
	response := serve(second.Handler(), request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("instance B accepted instance A cookie: %d %s", response.Code, response.Body.String())
	}
}
