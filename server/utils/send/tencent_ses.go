package send

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Jinnrry/pmail/dto/parsemail"
	"github.com/Jinnrry/pmail/services/outbound"
	"github.com/Jinnrry/pmail/utils/context"
	log "github.com/sirupsen/logrus"
)

const (
	tencentSESHost    = "ses.tencentcloudapi.com"
	tencentSESService = "ses"
	tencentSESVersion = "2020-10-02"
	tencentSESAction  = "SendEmail"
)

type tencentSESSimple struct {
	HTML string `json:"Html,omitempty"`
	Text string `json:"Text,omitempty"`
}

type tencentSESAttachment struct {
	Content  string `json:"Content"`
	FileName string `json:"FileName"`
}

type tencentSESSendRequest struct {
	FromEmailAddress string                 `json:"FromEmailAddress"`
	Destination      []string               `json:"Destination,omitempty"`
	Cc               []string               `json:"Cc,omitempty"`
	Bcc              []string               `json:"Bcc,omitempty"`
	ReplyToAddresses string                 `json:"ReplyToAddresses,omitempty"`
	Subject          string                 `json:"Subject"`
	Simple           *tencentSESSimple      `json:"Simple,omitempty"`
	Attachments      []tencentSESAttachment `json:"Attachments,omitempty"`
	TriggerType      int                    `json:"TriggerType"`
	SmtpMessageID    string                 `json:"SmtpMessageId,omitempty"`
	HeaderFrom       string                 `json:"HeaderFrom,omitempty"`
}

type tencentSESAPIError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

type tencentSESResponse struct {
	Response struct {
		Error     *tencentSESAPIError `json:"Error,omitempty"`
		RequestID string              `json:"RequestId"`
		MessageID string              `json:"MessageId"`
	} `json:"Response"`
}

// tryTencentSES handles normal PMail compose/send messages when Tencent SES API
// is selected. Tencent Cloud currently documents Simple as a legacy/special-
// permission field; accounts without that permission will receive the provider
// error instead of silently falling back to Direct MX.
func tryTencentSES(ctx *context.Context, e *parsemail.Email) (bool, error, map[string]error) {
	cfg, err := outbound.Get()
	if err != nil {
		return true, fmt.Errorf("read outbound settings: %w", err), nil
	}
	if cfg.Mode != outbound.ModeTencentSES {
		return false, nil, nil
	}

	err = sendTencentSES(ctx, cfg, e)
	if err != nil {
		return true, err, map[string]error{"tencent_ses": err}
	}
	return true, nil, map[string]error{}
}

// rejectTencentSESRawMode prevents raw forwarding paths from accidentally
// bypassing the selected API provider and falling back to direct-to-MX.
func rejectTencentSESRawMode() (bool, error, map[string]error) {
	cfg, err := outbound.Get()
	if err != nil {
		return true, fmt.Errorf("read outbound settings: %w", err), nil
	}
	if cfg.Mode != outbound.ModeTencentSES {
		return false, nil, nil
	}
	err = errors.New("Tencent SES API mode does not support raw forwarding; use SMTP Relay for raw forwarded messages")
	return true, err, map[string]error{"tencent_ses": err}
}

func sendTencentSES(ctx *context.Context, cfg outbound.Config, e *parsemail.Email) error {
	if e == nil || e.From == nil {
		return errors.New("Tencent SES: missing sender")
	}
	if len(e.To)+len(e.Cc)+len(e.Bcc) == 0 {
		return errors.New("Tencent SES: missing recipient")
	}
	if len(e.Text) == 0 && len(e.HTML) == 0 {
		return errors.New("Tencent SES: missing email content")
	}
	if len(e.Attachments) > 10 {
		return errors.New("Tencent SES: at most 10 attachments are supported")
	}

	request := tencentSESSendRequest{
		FromEmailAddress: cfg.TencentFromAddress,
		Destination:      emailAddresses(e.To),
		Cc:               emailAddresses(e.Cc),
		Bcc:              emailAddresses(e.Bcc),
		ReplyToAddresses: replyToAddress(e),
		Subject:          e.Subject,
		Simple: &tencentSESSimple{
			HTML: encodeBase64(e.HTML),
			Text: encodeBase64(e.Text),
		},
		TriggerType: cfg.TencentTriggerType,
		HeaderFrom:  headerFrom(e.From),
	}
	if e.MsgID != "" {
		request.SmtpMessageID = "<" + strings.Trim(e.MsgID, "<>") + ">"
	}

	var attachmentBytes int
	for _, attachment := range e.Attachments {
		if attachment == nil {
			continue
		}
		attachmentBytes += len(attachment.Content)
		request.Attachments = append(request.Attachments, tencentSESAttachment{
			Content:  base64.StdEncoding.EncodeToString(attachment.Content),
			FileName: attachment.Filename,
		})
	}
	// Tencent Cloud recommends keeping original attachment data within about
	// 4 MiB because Base64 expands the API request body.
	if attachmentBytes > 4*1024*1024 {
		return errors.New("Tencent SES: total attachment size exceeds 4 MiB")
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("Tencent SES: encode request: %w", err)
	}

	response, err := callTencentSES(cfg, payload)
	if err != nil {
		return err
	}
	log.WithContext(ctx).Infof("Tencent SES accepted email: message_id=%s request_id=%s", response.MessageID, response.RequestID)
	return nil
}

type tencentSESResult struct {
	MessageID string
	RequestID string
}

func callTencentSES(cfg outbound.Config, payload []byte) (tencentSESResult, error) {
	const contentType = "application/json; charset=utf-8"
	now := time.Now().UTC()
	timestamp := now.Unix()
	date := now.Format("2006-01-02")

	canonicalHeaders := "content-type:" + contentType + "\n" + "host:" + tencentSESHost + "\n"
	signedHeaders := "content-type;host"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + sha256Hex(payload)
	credentialScope := date + "/" + tencentSESService + "/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + fmt.Sprintf("%d", timestamp) + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))

	secretDate := hmacSHA256([]byte("TC3"+cfg.TencentSecretKey), []byte(date))
	secretService := hmacSHA256(secretDate, []byte(tencentSESService))
	secretSigning := hmacSHA256(secretService, []byte("tc3_request"))
	signature := hex.EncodeToString(hmacSHA256(secretSigning, []byte(stringToSign)))
	authorization := fmt.Sprintf(
		"TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		cfg.TencentSecretID, credentialScope, signedHeaders, signature,
	)

	req, err := http.NewRequest(http.MethodPost, "https://"+tencentSESHost+"/", bytes.NewReader(payload))
	if err != nil {
		return tencentSESResult{}, fmt.Errorf("Tencent SES: create request: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", tencentSESHost)
	req.Header.Set("X-TC-Action", tencentSESAction)
	req.Header.Set("X-TC-Version", tencentSESVersion)
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-TC-Region", cfg.TencentRegion)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return tencentSESResult{}, fmt.Errorf("Tencent SES request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return tencentSESResult{}, fmt.Errorf("Tencent SES: read response: %w", err)
	}
	var decoded tencentSESResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return tencentSESResult{}, fmt.Errorf("Tencent SES returned HTTP %d with an invalid response", resp.StatusCode)
	}
	if decoded.Response.Error != nil {
		return tencentSESResult{}, fmt.Errorf(
			"Tencent SES %s: %s (request_id=%s)",
			decoded.Response.Error.Code,
			decoded.Response.Error.Message,
			decoded.Response.RequestID,
		)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tencentSESResult{}, fmt.Errorf("Tencent SES returned HTTP %d (request_id=%s)", resp.StatusCode, decoded.Response.RequestID)
	}
	if decoded.Response.MessageID == "" {
		return tencentSESResult{}, fmt.Errorf("Tencent SES accepted no message id (request_id=%s)", decoded.Response.RequestID)
	}
	return tencentSESResult{MessageID: decoded.Response.MessageID, RequestID: decoded.Response.RequestID}, nil
}

func emailAddresses(users []*parsemail.User) []string {
	result := make([]string, 0, len(users))
	for _, user := range users {
		if user != nil && strings.TrimSpace(user.EmailAddress) != "" {
			result = append(result, strings.TrimSpace(user.EmailAddress))
		}
	}
	return result
}

func replyToAddress(e *parsemail.Email) string {
	if len(e.ReplyTo) > 0 && e.ReplyTo[0] != nil && e.ReplyTo[0].EmailAddress != "" {
		return e.ReplyTo[0].EmailAddress
	}
	if e.From != nil {
		return e.From.EmailAddress
	}
	return ""
}

func headerFrom(user *parsemail.User) string {
	if user == nil {
		return ""
	}
	name := strings.TrimSpace(strings.ReplaceAll(user.Name, ":", ""))
	if name == "" {
		return user.EmailAddress
	}
	return fmt.Sprintf("%s <%s>", name, user.EmailAddress)
}

func encodeBase64(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}
