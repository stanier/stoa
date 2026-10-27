package authdclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckLoginCallback_RequiresTheStartingBrowsersCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?login_id=L1&code=C", nil)
	if _, _, err := CheckLoginCallback(req); !errors.Is(err, ErrLoginMismatch) {
		t.Fatalf("no cookie: err = %v, want ErrLoginMismatch", err)
	}

	req.AddCookie(&http.Cookie{Name: LoginCookie, Value: "someone-elses-login"})
	if _, _, err := CheckLoginCallback(req); !errors.Is(err, ErrLoginMismatch) {
		t.Fatalf("other login's cookie: err = %v, want ErrLoginMismatch", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/callback?login_id=L1&code=C", nil)
	req.AddCookie(&http.Cookie{Name: LoginCookie, Value: "L1"})
	id, code, err := CheckLoginCallback(req)
	if err != nil || id != "L1" || code != "C" {
		t.Fatalf("matching cookie: id=%q code=%q err=%v", id, code, err)
	}
}

func TestExchangeLoginCode(t *testing.T) {
	authd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/oauth/login/exchange" || body["login_id"] != "L1" || body["code"] != "C" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"session_token": "tok", "llm_key": "key"})
	}))
	defer authd.Close()

	tok, key, err := ExchangeLoginCode(t.Context(), authd.Client(), authd.URL, "L1", "C")
	if err != nil || tok != "tok" || key != "key" {
		t.Fatalf("exchange: tok=%q key=%q err=%v", tok, key, err)
	}
	if _, _, err := ExchangeLoginCode(t.Context(), authd.Client(), authd.URL, "L1", "wrong"); err == nil {
		t.Fatal("exchange with a wrong code succeeded")
	}
}
