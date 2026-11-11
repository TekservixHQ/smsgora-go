# SMS Gora Go client

[![Go Reference](https://pkg.go.dev/badge/github.com/tekservixhq/smsgora-go.svg)](https://pkg.go.dev/github.com/tekservixhq/smsgora-go)

**SMS Gora SMS Gateway** - official Go client: send and receive SMS through your own Android phones. By Tekservix.

Go client for the [SMS Gora SMS Gateway](https://tekservix.com/smsgora/) API.

SMS Gora turns Android phones with ordinary SIM cards into an SMS gateway. Your server calls the API, a linked phone sends the text, and replies and delivery reports come back to you. There's no sender ID registration and no per-message fee from us: your mobile operator bills the texts like any SMS.

- Standard library only, Go 1.21+
- `context.Context` on every call, typed errors
- Webhook signature check built in

## Install

```bash
go get github.com/tekservixhq/smsgora-go
```

## Quick start

Create an API key in the SMS Gora dashboard (SMS Gora -> API keys) and keep it on your server.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	smsgora "github.com/tekservixhq/smsgora-go"
)

func main() {
	sms := smsgora.New(os.Getenv("SMSGORA_KEY"))

	res, err := sms.SendSMS(context.Background(), smsgora.SendParams{
		To:      []string{"+15551234567"},
		Message: "Your verification code is 482913",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.MessageIDs) // [1873]
}
```

## Sending

```go
ctx := context.Background()

// Several recipients (up to 1,000 per call)
sms.SendSMS(ctx, smsgora.SendParams{To: []string{"+15551234567", "+447700900123"}, Message: "Your order has shipped"})

// From a chosen phone and SIM, scheduled for later
slot := 1 // 0 = SIM 1, 1 = SIM 2
at := time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)
sms.SendSMS(ctx, smsgora.SendParams{
	To: []string{"+15551234567"}, Message: "Reminder: your appointment is tomorrow at 10:00",
	DeviceID: 42, SimSlot: &slot, ScheduleAt: &at,
})

// Different texts for different people in one call (up to 5,000 recipients)
sms.SendBulkSMS(ctx, 42, "Hello, your parcel is ready for pickup.", []smsgora.BulkMessage{
	{Recipients: []string{"+15551234567"}, Message: "Hi Aline, parcel #1042 is ready."},
	{Recipients: []string{"+15557654321"}},
})
```

## Delivery status and incoming SMS

```go
msg, _ := sms.GetMessage(ctx, 1873)
fmt.Println(msg["status"]) // queued -> dispatched -> sent -> delivered (or failed)

sms.CancelMessage(ctx, 1874) // works while queued or scheduled

inbox, _ := sms.ListReceivedSMS(ctx, 0, url.Values{"since": {"2026-11-01"}, "limit": {"50"}})
phones, _ := sms.ListDevices(ctx)
account, _ := sms.GetAccount(ctx)
```

Anything else in the [API reference](https://tekservix.com/smsgora/docs/) is one call away:

```go
var batch map[string]any
err := sms.Request(ctx, "GET", "/batches/311", nil, &batch)
```

## Webhooks

Add your HTTPS URL in SMS Gora -> Webhooks (or with `CreateWebhook`) and keep the secret. Each request carries `X-SmsGora-Signature: sha256=...`, an HMAC-SHA256 of the raw body:

```go
http.HandleFunc("/sms/webhook", func(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	if !smsgora.VerifyWebhook(raw, r.Header.Get("X-SmsGora-Signature"), os.Getenv("SMSGORA_WEBHOOK_SECRET")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK) // answer within 10 seconds; queue slow work

	var event struct {
		Event string `json:"event"`
		Data  struct {
			From    string `json:"from"`
			Message string `json:"message"`
		} `json:"data"`
	}
	json.Unmarshal(raw, &event)
	if event.Event == "sms.received" {
		log.Println(event.Data.From, event.Data.Message)
	}
})
```

## Errors

API errors are `*smsgora.Error` with `Status`, `Message`, field `Errors` and, on 429, `RetryAfter`.

```go
var apiErr *smsgora.Error
if errors.As(err, &apiErr) && apiErr.Status == 429 {
	time.Sleep(apiErr.RetryAfter) // rate limit (300 requests/minute per key) or plan limit reached
}
```

Never ship the key inside a mobile or desktop app: anyone could read it. Call the API from your server.

## Migrating from TextBee

SMS Gora accepts TextBee-style requests, so most integrations switch by changing the base URL and key. See the [compatibility notes](https://tekservix.com/smsgora/docs/).

## More from Tekservix

- [WAservix WhatsApp API Gateway](https://tekservix.com/waservix/): send WhatsApp messages from your own number (`go get github.com/tekservixhq/waservix-go`)
- [AI Sales Agent](https://tekservix.com/ai-sales-agent): answers customers on WhatsApp and SMS using your website's content
- [Website to App](https://tekservix.com/mobile-apps): turn your website into Android and iOS apps
- SMS Gora is also available for Node.js (`npm install smsgora`), Python (`pip install smsgora`) and PHP (`composer require tekservix/smsgora`)

## License

MIT
