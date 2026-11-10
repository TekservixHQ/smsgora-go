// Package smsgora is a Go client for the SMS Gora SMS Gateway API.
//
// SMS Gora turns Android phones with ordinary SIM cards into an SMS gateway:
// your server calls the API, a linked phone sends the text, and replies and
// delivery reports come back to you.
//
// Docs: https://tekservix.com/smsgora/docs/
package smsgora

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Version of this client.
const Version = "1.0.0"

// DefaultBaseURL is the SMS Gora API endpoint.
const DefaultBaseURL = "https://tekservix.com/smsgora/api/v1"

// Error is returned when the API answers with an error.
type Error struct {
	Status     int                 // HTTP status code
	Message    string              // message from the API
	Errors     map[string][]string // field errors, if any
	Body       []byte              // raw answer
	RetryAfter time.Duration       // set on HTTP 429
}

func (e *Error) Error() string { return fmt.Sprintf("smsgora: %s (HTTP %d)", e.Message, e.Status) }

// Client talks to the SMS Gora API. Create one with New and reuse it.
type Client struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// New returns a client for the given API key (create one in SMS Gora -> API keys).
func New(apiKey string) *Client {
	return &Client{APIKey: apiKey, BaseURL: DefaultBaseURL, HTTPClient: &http.Client{Timeout: 30 * time.Second}}
}

// Request calls any documented endpoint and decodes the `data` field into out (which may be nil).
func (c *Client) Request(ctx context.Context, method, path string, body, out interface{}) error {
	if c.APIKey == "" {
		return errors.New("smsgora: APIKey is required (create one in SMS Gora -> API keys)")
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "smsgora-go/"+Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	var env struct {
		Success *bool               `json:"success"`
		Message string              `json:"message"`
		Errors  map[string][]string `json:"errors"`
		Data    json.RawMessage     `json:"data"`
	}
	jsonErr := json.Unmarshal(raw, &env)
	if res.StatusCode >= 400 || (jsonErr == nil && env.Success != nil && !*env.Success) {
		e := &Error{Status: res.StatusCode, Message: env.Message, Errors: env.Errors, Body: raw}
		if e.Message == "" {
			e.Message = http.StatusText(res.StatusCode)
		}
		if s, perr := strconv.ParseFloat(res.Header.Get("Retry-After"), 64); perr == nil {
			e.RetryAfter = time.Duration(s * float64(time.Second))
		}
		return e
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if jsonErr == nil && env.Data != nil {
		return json.Unmarshal(env.Data, out)
	}
	return json.Unmarshal(raw, out)
}

// SendParams describes an SMS to send.
type SendParams struct {
	To         []string   // up to 1,000 numbers in international format
	Message    string     // up to 1,600 characters
	DeviceID   int64      // optional: send from this phone
	SimSlot    *int       // optional: 0 = SIM 1, 1 = SIM 2
	ScheduleAt *time.Time // optional: send later
}

// SendResult is returned by SendSMS.
type SendResult struct {
	MessageIDs []int64 `json:"message_ids"`
	BatchID    *int64  `json:"batch_id,omitempty"`
	Status     string  `json:"status,omitempty"`
}

// SendSMS sends an SMS. It picks your best online phone unless DeviceID is set.
func (c *Client) SendSMS(ctx context.Context, p SendParams) (*SendResult, error) {
	body := map[string]interface{}{"recipients": p.To, "message": p.Message}
	if p.SimSlot != nil {
		body["sim_slot"] = *p.SimSlot
	}
	if p.ScheduleAt != nil {
		body["schedule_at"] = p.ScheduleAt.UTC().Format(time.RFC3339)
	}
	path := "/send-sms"
	if p.DeviceID != 0 {
		path = "/devices/" + strconv.FormatInt(p.DeviceID, 10) + "/send-sms"
	}
	var out SendResult
	if err := c.Request(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BulkMessage is one entry of SendBulkSMS.
type BulkMessage struct {
	Recipients []string `json:"recipients"`
	Message    string   `json:"message,omitempty"`
}

// SendBulkSMS sends different texts to different people in one call (up to 5,000 recipients).
// Entries without their own Message use messageTemplate.
func (c *Client) SendBulkSMS(ctx context.Context, deviceID int64, messageTemplate string, messages []BulkMessage) (map[string]interface{}, error) {
	body := map[string]interface{}{"messages": messages}
	if messageTemplate != "" {
		body["messageTemplate"] = messageTemplate
	}
	var out map[string]interface{}
	err := c.Request(ctx, http.MethodPost, "/devices/"+strconv.FormatInt(deviceID, 10)+"/send-bulk-sms", body, &out)
	return out, err
}

// GetMessage returns one message. Status is queued, dispatched, sent, delivered, failed or cancelled.
func (c *Client) GetMessage(ctx context.Context, id int64) (map[string]interface{}, error) {
	return c.getMap(ctx, "/messages/"+strconv.FormatInt(id, 10))
}

// CancelMessage cancels a queued or scheduled message.
func (c *Client) CancelMessage(ctx context.Context, id int64) error {
	return c.Request(ctx, http.MethodPost, "/messages/"+strconv.FormatInt(id, 10)+"/cancel", nil, nil)
}

// ListMessages lists messages. Filters: direction (in/out), status, device_id, phone, since, after_id, page, limit.
func (c *Client) ListMessages(ctx context.Context, filters url.Values) (interface{}, error) {
	var out interface{}
	err := c.Request(ctx, http.MethodGet, "/messages"+query(filters), nil, &out)
	return out, err
}

// ListReceivedSMS lists incoming SMS for all phones (deviceID 0) or one phone.
func (c *Client) ListReceivedSMS(ctx context.Context, deviceID int64, filters url.Values) (interface{}, error) {
	path := "/received-sms"
	if deviceID != 0 {
		path = "/devices/" + strconv.FormatInt(deviceID, 10) + "/get-received-sms"
	}
	var out interface{}
	err := c.Request(ctx, http.MethodGet, path+query(filters), nil, &out)
	return out, err
}

// GetBatch returns a batch created by sending to several recipients.
func (c *Client) GetBatch(ctx context.Context, id int64) (map[string]interface{}, error) {
	return c.getMap(ctx, "/batches/"+strconv.FormatInt(id, 10))
}

// ListDevices lists your linked Android phones.
func (c *Client) ListDevices(ctx context.Context) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	err := c.Request(ctx, http.MethodGet, "/devices", nil, &out)
	return out, err
}

// GetDevice returns one phone.
func (c *Client) GetDevice(ctx context.Context, id int64) (map[string]interface{}, error) {
	return c.getMap(ctx, "/devices/"+strconv.FormatInt(id, 10))
}

// GetAccount returns your account, plan and usage.
func (c *Client) GetAccount(ctx context.Context) (map[string]interface{}, error) {
	return c.getMap(ctx, "/account")
}

// ListWebhooks lists webhooks registered by API.
func (c *Client) ListWebhooks(ctx context.Context) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	err := c.Request(ctx, http.MethodGet, "/webhooks", nil, &out)
	return out, err
}

// CreateWebhook registers a webhook. The answer includes the secret (only returned once).
// events defaults to ["sms.received"]; deviceID 0 means all phones.
func (c *Client) CreateWebhook(ctx context.Context, webhookURL string, events []string, deviceID int64) (map[string]interface{}, error) {
	if len(events) == 0 {
		events = []string{"sms.received"}
	}
	body := map[string]interface{}{"url": webhookURL, "events": events, "device_id": nil}
	if deviceID != 0 {
		body["device_id"] = deviceID
	}
	var out map[string]interface{}
	err := c.Request(ctx, http.MethodPost, "/webhooks", body, &out)
	return out, err
}

// DeleteWebhook removes a webhook.
func (c *Client) DeleteWebhook(ctx context.Context, id int64) error {
	return c.Request(ctx, http.MethodDelete, "/webhooks/"+strconv.FormatInt(id, 10), nil, nil)
}

func (c *Client) getMap(ctx context.Context, path string) (map[string]interface{}, error) {
	var out map[string]interface{}
	err := c.Request(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func query(v url.Values) string {
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// VerifyWebhook checks the X-SmsGora-Signature header (sha256=<hex HMAC-SHA256 of the raw body>).
// Pass the raw request body, before JSON decoding.
func VerifyWebhook(rawBody []byte, signature, secret string) bool {
	if len(rawBody) == 0 || signature == "" || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}
