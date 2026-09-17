package send

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	stdsmtp "net/smtp"
	"strings"
	"time"

	"github.com/Jinnrry/pmail/dto/parsemail"
	"github.com/Jinnrry/pmail/services/outbound"
	"github.com/Jinnrry/pmail/utils/context"
	log "github.com/sirupsen/logrus"
)

const relayIOTimeout = 30 * time.Second

// tryRelay is the outbound provider switch used before direct MX lookup.
// It handles authenticated SMTP relay and Tencent SES API modes, and returns
// handled=false only when PMail should continue with direct-to-MX delivery.
func tryRelay(ctx *context.Context, fromDomain string, data []byte, to []*parsemail.User, from string) (handled bool, deliveryErr error, domainErrors map[string]error) {
	cfg, err := outbound.Get()
	if err != nil {
		return true, fmt.Errorf("load outbound configuration: %w", err), map[string]error{"outbound": err}
	}

	if cfg.Mode == outbound.ModeTencentSES {
		return deliverTencentSESRaw(ctx, cfg, data, to, from)
	}
	if cfg.Mode != outbound.ModeRelay {
		return false, nil, nil
	}

	recipients := buildAddress(to)
	if len(recipients) == 0 {
		err := errors.New("SMTP relay has no recipients")
		return true, err, map[string]error{"relay": err}
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	log.WithContext(ctx).Infof("Outbound relay delivery: host=%s port=%d security=%s recipients=%d", cfg.Host, cfg.Port, cfg.Security, len(recipients))

	err = sendSMTPRelay(cfg.Host, addr, cfg.Username, cfg.Password, cfg.Security, from, recipients, data)
	domainErrors = relayDomainErrors(to, err)
	if err != nil {
		log.WithContext(ctx).Errorf("Outbound relay delivery failed: host=%s port=%d error=%v", cfg.Host, cfg.Port, err)
		return true, fmt.Errorf("SMTP relay delivery failed: %w", err), domainErrors
	}

	log.WithContext(ctx).Infof("Outbound relay accepted message: host=%s recipients=%d", cfg.Host, len(recipients))
	return true, nil, domainErrors
}

func deliverTencentSESRaw(ctx *context.Context, cfg outbound.Config, data []byte, recipients []*parsemail.User, envelopeFrom string) (bool, error, map[string]error) {
	if len(recipients) == 0 {
		err := errors.New("Tencent SES has no recipients")
		return true, err, map[string]error{"tencent_ses": err}
	}

	// Parse the already-built RFC message so the SES backend preserves the
	// compose subject/body/attachments. Envelope recipients are applied below
	// so Bcc remains available even though it is intentionally absent from the
	// RFC message headers.
	email := parsemail.NewEmailFromReader(nil, bytes.NewReader(data), len(data))
	if email.From == nil || strings.TrimSpace(email.From.EmailAddress) == "" {
		email.From = &parsemail.User{EmailAddress: envelopeFrom}
	}

	visible := map[string]struct{}{}
	for _, user := range append(append([]*parsemail.User{}, email.To...), email.Cc...) {
		if user != nil {
			visible[strings.ToLower(strings.TrimSpace(user.EmailAddress))] = struct{}{}
		}
	}
	email.Bcc = nil
	for _, user := range recipients {
		if user == nil || strings.TrimSpace(user.EmailAddress) == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(user.EmailAddress))
		if _, ok := visible[key]; !ok {
			email.Bcc = append(email.Bcc, user)
		}
	}

	log.WithContext(ctx).Infof("Tencent SES API delivery: region=%s recipients=%d", cfg.TencentRegion, len(recipients))
	err := sendTencentSES(ctx, cfg, email)
	domainErrors := relayDomainErrors(recipients, err)
	if err != nil {
		log.WithContext(ctx).Errorf("Tencent SES API delivery failed: region=%s error=%v", cfg.TencentRegion, err)
		return true, err, domainErrors
	}
	return true, nil, domainErrors
}

func sendSMTPRelay(host, addr, username, password, security, from string, to []string, msg []byte) error {
	var conn net.Conn
	var err error

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tlsConfig := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}

	switch security {
	case outbound.SecurityTLS:
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	case outbound.SecuritySTARTTLS:
		conn, err = dialer.Dial("tcp", addr)
	default:
		return fmt.Errorf("unsupported SMTP relay security: %s", security)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(relayIOTimeout))

	client, err := stdsmtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if security == outbound.SecuritySTARTTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return errors.New("SMTP relay does not advertise STARTTLS")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}

	if username != "" || password != "" {
		if username == "" || password == "" {
			return errors.New("SMTP relay username and password must both be configured")
		}
		auth := stdsmtp.PlainAuth("", username, password, host)
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP relay authentication failed: %w", err)
		}
	}

	if err = client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if strings.TrimSpace(recipient) == "" {
			continue
		}
		if err = client.Rcpt(recipient); err != nil {
			return fmt.Errorf("recipient %s rejected: %w", recipient, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(msg); err != nil {
		_ = writer.Close()
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}

	// DATA's final 250 means the relay accepted responsibility. Do not wait for
	// QUIT here; closing avoids turning a missing 221 into an accidental retry.
	return nil
}

func relayDomainErrors(to []*parsemail.User, deliveryErr error) map[string]error {
	result := map[string]error{}
	for _, recipient := range to {
		if recipient == nil {
			continue
		}
		_, domain := recipient.GetDomainAccount()
		if domain == "" {
			domain = "relay"
		}
		result[domain] = deliveryErr
	}
	return result
}

var _ io.Writer
