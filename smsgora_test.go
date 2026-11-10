package smsgora

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type call struct {
	method, path, key string
	body              map[string]interface{}
}

func server(t *testing.T, status int, reply string, last *call) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*last = call{method: r.Method, path: r.URL.RequestURI(), key: r.Header.Get("x-api-key")}
		if len(b) > 0 {
			_ = json.Unmarshal(b, &last.body)
		}
		if status == 429 {
			w.Header().Set("Retry-After", "7")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	c := New("sgk_test")
	c.BaseURL = srv.URL + "/"
	return c
}

func TestSendSMS(t *testing.T) {
	var last call
	c := server(t, 200, `{"success":true,"data":{"message_ids":[1873]}}`, &last)
	res, err := c.SendSMS(context.Background(), SendParams{To: []string{"+1555"}, Message: "hi"})
	if err != nil || len(res.MessageIDs) != 1 || res.MessageIDs[0] != 1873 {
		t.Fatalf("got %+v, %v", res, err)
	}
	if last.method != "POST" || last.path != "/send-sms" || last.key != "sgk_test" {
		t.Fatalf("bad call %+v", last)
	}
	if last.body["message"] != "hi" {
		t.Fatalf("bad body %+v", last.body)
	}
}

func TestSendFromDevice(t *testing.T) {
	var last call
	c := server(t, 200, `{"success":true,"data":{"message_ids":[1,2]}}`, &last)
	slot := 1
	at := time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)
	if _, err := c.SendSMS(context.Background(), SendParams{To: []string{"+1", "+2"}, Message: "x", DeviceID: 42, SimSlot: &slot, ScheduleAt: &at}); err != nil {
		t.Fatal(err)
	}
	if last.path != "/devices/42/send-sms" || last.body["sim_slot"] != float64(1) || last.body["schedule_at"] != "2026-12-01T08:00:00Z" {
		t.Fatalf("bad call %+v", last)
	}
}

func TestPathsAndQueries(t *testing.T) {
	var last call
	c := server(t, 200, `{"success":true,"data":[]}`, &last)
	ctx := context.Background()
	_, _ = c.ListReceivedSMS(ctx, 3, url.Values{"limit": {"5"}})
	if last.path != "/devices/3/get-received-sms?limit=5" {
		t.Fatalf("received path %s", last.path)
	}
	_, _ = c.ListMessages(ctx, url.Values{"status": {"sent"}})
	if last.path != "/messages?status=sent" {
		t.Fatalf("list path %s", last.path)
	}
	_ = c.DeleteWebhook(ctx, 9)
	if last.method != "DELETE" || last.path != "/webhooks/9" {
		t.Fatalf("delete %+v", last)
	}
	_, _ = c.SendBulkSMS(ctx, 42, "t", []BulkMessage{{Recipients: []string{"+1"}}})
	if last.path != "/devices/42/send-bulk-sms" || last.body["messageTemplate"] != "t" {
		t.Fatalf("bulk %+v", last)
	}
}

func TestErrors(t *testing.T) {
	var last call
	c := server(t, 429, `{"success":false,"message":"Too many requests"}`, &last)
	_, err := c.GetAccount(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Status != 429 || e.RetryAfter != 7*time.Second || e.Message != "Too many requests" {
		t.Fatalf("got %v", err)
	}
	if _, err := New("").GetAccount(context.Background()); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestVerifyWebhook(t *testing.T) {
	raw := []byte(`{"event":"sms.received"}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(raw)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifyWebhook(raw, sig, "s3cret") || VerifyWebhook(raw, sig, "nope") || VerifyWebhook(raw, "", "s3cret") {
		t.Fatal("webhook verification wrong")
	}
}
