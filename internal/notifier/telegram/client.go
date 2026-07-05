package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BlackRaincoat/versentry/internal/netutil"
	"github.com/BlackRaincoat/versentry/internal/notifier/httpretry"
)

// buildHTTPClient returns an HTTP client that uses proxyURL when set.
func buildHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	return netutil.BuildHTTPClient(proxyURL, timeout)
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (n *Notifier) sendMessageWithRetry(ctx context.Context, text string) error {
	cfg := httpretry.Config{
		MaxAttempts:       n.maxAttempts,
		RetryDelay:        n.retryDelay,
		MaxRetryAfter:     httpretry.DefaultMaxRetryAfter,
		BackoffMultiplier: httpretry.DefaultBackoffMultiplier,
		Sleep:             n.sleep,
		LogComponent:      "telegram",
		LogFields:         []any{"proxy", maskProxyURL(n.proxyURL)},
	}

	err := httpretry.Do(ctx, cfg, classifyTelegramResponse, func(ctx context.Context) httpretry.AttemptResult {
		return n.sendMessageOnce(ctx, text)
	})
	if err != nil {
		return n.redactErr(err)
	}
	return nil
}

func (n *Notifier) sendMessageOnce(ctx context.Context, text string) httpretry.AttemptResult {
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", n.apiURL, n.token)

	form := url.Values{}
	form.Set("chat_id", n.chatID)
	form.Set("text", text)
	form.Set("parse_mode", n.parseMode)
	form.Set("disable_web_page_preview", "true")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return httpretry.AttemptResult{Err: fmt.Errorf("create request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := n.client.Do(req)
	if err != nil {
		return httpretry.AttemptResult{Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return httpretry.AttemptResult{Err: fmt.Errorf("read response: %w", err)}
	}

	return httpretry.AttemptResult{
		StatusCode: resp.StatusCode,
		Body:       body,
		RetryAfter: resp.Header.Get("Retry-After"),
	}
}

func classifyTelegramResponse(status int, body []byte, retryAfterHeader string, netErr error) httpretry.Outcome {
	if netErr != nil {
		if errors.Is(netErr, context.Canceled) || errors.Is(netErr, context.DeadlineExceeded) {
			return httpretry.Outcome{Err: fmt.Errorf("send request: %w", netErr), Retryable: false}
		}
		return httpretry.Outcome{Err: fmt.Errorf("send request: %w", netErr), Retryable: true}
	}

	var apiResp telegramResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		retryable := status >= 500
		return httpretry.Outcome{
			Err:       fmt.Errorf("telegram HTTP %d: %s", status, strings.TrimSpace(string(body))),
			Retryable: retryable,
		}
	}

	if status == http.StatusOK && apiResp.OK {
		return httpretry.Outcome{}
	}

	code := apiResp.ErrorCode
	if code == 0 {
		code = status
	}

	switch {
	case status == http.StatusTooManyRequests || code == http.StatusTooManyRequests:
		return telegramRateLimitOutcome(apiResp, retryAfterHeader)
	case status == http.StatusBadRequest || code == http.StatusBadRequest:
		return telegramClientOutcome(status, apiResp, "bad request")
	case status == http.StatusUnauthorized || code == http.StatusUnauthorized:
		return telegramClientOutcome(status, apiResp, "unauthorized")
	case status == http.StatusForbidden || code == http.StatusForbidden:
		return telegramClientOutcome(status, apiResp, "forbidden")
	case status == http.StatusNotFound || code == http.StatusNotFound:
		return telegramClientOutcome(status, apiResp, "not found")
	case status >= 500 && status < 600:
		return telegramRetryableOutcome(status, apiResp)
	case status >= 400 && status < 500:
		return telegramClientOutcome(status, apiResp, "client error")
	default:
		if apiResp.OK {
			return httpretry.Outcome{}
		}
		return telegramClientOutcome(status, apiResp, "api error")
	}
}

func telegramRateLimitOutcome(api telegramResponse, retryAfterHeader string) httpretry.Outcome {
	retryAfter := time.Duration(api.Parameters.RetryAfter) * time.Second
	if retryAfter <= 0 && retryAfterHeader != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfterHeader)); err == nil && secs > 0 {
			retryAfter = time.Duration(secs) * time.Second
		}
	}
	if retryAfter > httpretry.DefaultMaxRetryAfter {
		return httpretry.Outcome{
			Err:       fmt.Errorf("telegram rate limited for %s (exceeds %s cap)", retryAfter, httpretry.DefaultMaxRetryAfter),
			Retryable: false,
		}
	}
	if retryAfter <= 0 {
		retryAfter = httpretry.DefaultRetryDelay
	}
	return httpretry.Outcome{
		Err:        fmt.Errorf("telegram rate limited (HTTP 429)"),
		Retryable:  true,
		RetryAfter: retryAfter,
	}
}

func telegramRetryableOutcome(status int, api telegramResponse) httpretry.Outcome {
	desc := api.Description
	if desc == "" {
		desc = "server error"
	}
	return httpretry.Outcome{
		Err:       fmt.Errorf("telegram API error (HTTP %d): %s", status, desc),
		Retryable: true,
	}
}

func telegramClientOutcome(status int, api telegramResponse, kind string) httpretry.Outcome {
	desc := api.Description
	if desc == "" {
		desc = kind
	}
	return httpretry.Outcome{
		Err:       fmt.Errorf("telegram API error (HTTP %d): %s", status, desc),
		Retryable: false,
	}
}

// redactErr returns an error whose message has secrets removed.
func (n *Notifier) redactErr(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(maskSecrets(err.Error(), n.token))
}

// maskSecrets redacts the bot token (and URL-encoded forms) from any string.
func maskSecrets(s, token string) string {
	if s == "" || token == "" {
		return s
	}
	masked := maskToken(token)
	s = strings.ReplaceAll(s, "bot"+token, "bot"+masked)
	s = strings.ReplaceAll(s, token, masked)
	if enc := url.QueryEscape(token); enc != token {
		s = strings.ReplaceAll(s, "bot"+enc, "bot"+masked)
		s = strings.ReplaceAll(s, enc, masked)
	}
	if enc := url.PathEscape(token); enc != token {
		s = strings.ReplaceAll(s, "bot"+enc, "bot"+masked)
		s = strings.ReplaceAll(s, enc, masked)
	}
	return s
}

// maskToken redacts the secret part of a Telegram bot token.
func maskToken(token string) string {
	if token == "" {
		return "***"
	}
	id, _, ok := strings.Cut(token, ":")
	if !ok || id == "" {
		return "***"
	}
	return id + ":***"
}

// maskProxyURL redacts the password in a proxy URL for safe logging.
func maskProxyURL(raw string) string {
	return netutil.MaskProxyURL(raw)
}
